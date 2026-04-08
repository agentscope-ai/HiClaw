package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/agentconfig"
	"github.com/hiclaw/hiclaw-controller/internal/apiserver"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/config"
	"github.com/hiclaw/hiclaw-controller/internal/controller"
	"github.com/hiclaw/hiclaw-controller/internal/credentials"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	"github.com/hiclaw/hiclaw-controller/internal/gateway"
	"github.com/hiclaw/hiclaw-controller/internal/matrix"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	"github.com/hiclaw/hiclaw-controller/internal/server"
	"github.com/hiclaw/hiclaw-controller/internal/store"
	"github.com/hiclaw/hiclaw-controller/internal/watcher"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	ctrl.SetLogger(zap.New())
	logger := ctrl.Log.WithName("hiclaw-controller")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := config.LoadConfig()

	// --- Build scheme ---
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	if err := v1beta1.AddToScheme(scheme); err != nil {
		logger.Error(err, "failed to add hiclaw types to scheme")
		os.Exit(1)
	}

	// --- Backend infrastructure (shared by HTTP API and reconcilers) ---
	cloudCreds := buildCloudCredentials(cfg)
	workerBackends, gatewayBackends := buildBackends(cfg, cloudCreds)
	registry := backend.NewRegistry(workerBackends, gatewayBackends)

	// --- Auth (K8s SA Token + TokenReview) ---
	// k8sClient and authMw will be initialized after the REST config is available.
	var k8sClient kubernetes.Interface
	var authMw *authpkg.Middleware
	var restCfgForAuth *rest.Config

	// --- STS service ---
	var stsService *credentials.STSService
	if cfg.OIDCTokenFile != "" {
		stsService = credentials.NewSTSService(cfg.STSConfig())
	}

	// --- Executors (embedded mode shell scripts) ---
	shell := executor.NewShell(cfg.SkillsDir)
	packages := executor.NewPackageResolver("/tmp/import")

	// --- Go service clients ---
	matrixClient := matrix.NewTuwunelClient(cfg.MatrixConfig(), nil)
	gwClient := gateway.NewHigressClient(cfg.GatewayConfig(), nil)
	ossClient := oss.NewMinIOClient(cfg.OSSConfig())
	var ossAdminClient oss.StorageAdminClient
	if cfg.KubeMode == "embedded" {
		ossAdminClient = oss.NewMinIOAdminClient(cfg.OSSConfig())
	}
	agentGen := agentconfig.NewGenerator(cfg.AgentConfig())
	credStore := &controller.FileCredentialStore{Dir: envOrDefault("HICLAW_CREDS_DIR", "/data/worker-creds")}

	// --- Kube mode ---
	var mgr ctrl.Manager

	if cfg.KubeMode == "embedded" {
		logger.Info("starting embedded mode", "dataDir", cfg.DataDir, "configDir", cfg.ConfigDir)

		kineServer, err := store.StartKine(ctx, store.Config{
			DataDir:       cfg.DataDir,
			ListenAddress: "127.0.0.1:2379",
		})
		if err != nil {
			logger.Error(err, "failed to start kine")
			os.Exit(1)
		}
		logger.Info("kine started", "endpoints", kineServer.ETCDConfig.Endpoints)

		embeddedRestCfg, err := apiserver.Start(ctx, apiserver.Config{
			DataDir:    cfg.DataDir,
			EtcdURL:    "http://127.0.0.1:2379",
			BindAddr:   "127.0.0.1",
			SecurePort: "6443",
			CRDDir:     cfg.CRDDir,
		})
		if err != nil {
			logger.Error(err, "failed to start embedded kube-apiserver")
			os.Exit(1)
		}
		logger.Info("embedded kube-apiserver ready")
		restCfgForAuth = embeddedRestCfg

		mgr, err = ctrl.NewManager(embeddedRestCfg, ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
		})
		if err != nil {
			logger.Error(err, "failed to create controller manager")
			os.Exit(1)
		}

		fw := watcher.New(cfg.ConfigDir, mgr.GetClient())
		if err := fw.InitialSync(ctx); err != nil {
			logger.Error(err, "initial sync failed (non-fatal)")
		}
		go func() {
			if err := fw.Watch(ctx); err != nil && ctx.Err() == nil {
				logger.Error(err, "file watcher stopped unexpectedly")
			}
		}()
		logger.Info("file watcher started", "dir", cfg.ConfigDir)

	} else {
		logger.Info("starting in-cluster mode")

		inclusterRestCfg := ctrl.GetConfigOrDie()
		restCfgForAuth = inclusterRestCfg
		var mgrOpts ctrl.Options
		mgrOpts.Scheme = scheme
		if cfg.K8sNamespace != "" {
			mgrOpts.Cache.DefaultNamespaces = map[string]cache.Config{
				cfg.K8sNamespace: {},
			}
		}
		var err error
		mgr, err = ctrl.NewManager(inclusterRestCfg, mgrOpts)
		if err != nil {
			logger.Error(err, "failed to create controller manager")
			os.Exit(1)
		}
	}

	// --- Build K8s clientset and auth components ---
	namespace := cfg.K8sNamespace
	if namespace == "" {
		namespace = "default"
	}

	if restCfgForAuth != nil {
		var err error
		k8sClient, err = kubernetes.NewForConfig(restCfgForAuth)
		if err != nil {
			logger.Error(err, "failed to create kubernetes client for auth")
			os.Exit(1)
		}
		authenticator := authpkg.NewTokenReviewAuthenticator(k8sClient, cfg.AuthAudience)
		enricher := authpkg.NewCREnricher(mgr.GetClient(), namespace)
		authorizer := authpkg.NewAuthorizer()
		authMw = authpkg.NewMiddleware(authenticator, enricher, authorizer, mgr.GetClient(), namespace)
		logger.Info("K8s SA token authentication enabled", "audience", cfg.AuthAudience)
	} else {
		authMw = authpkg.NewMiddleware(nil, nil, authpkg.NewAuthorizer(), nil, namespace)
		logger.Info("authentication disabled (no REST config)")
	}

	// --- Register reconcilers ---
	sharedReconcilerFields := struct {
		matrix      matrix.Client
		gateway     gateway.Client
		oss         oss.StorageClient
		ossAdmin    oss.StorageAdminClient
		agentConfig *agentconfig.Generator
		backend     *backend.Registry
		creds       controller.CredentialStore
		kubeMode    string
	}{
		matrix:      matrixClient,
		gateway:     gwClient,
		oss:         ossClient,
		ossAdmin:    ossAdminClient,
		agentConfig: agentGen,
		backend:     registry,
		creds:       credStore,
		kubeMode:    cfg.KubeMode,
	}

	if err := (&controller.WorkerReconciler{
		Client:            mgr.GetClient(),
		Matrix:            sharedReconcilerFields.matrix,
		Gateway:           sharedReconcilerFields.gateway,
		OSS:               sharedReconcilerFields.oss,
		OSSAdmin:          sharedReconcilerFields.ossAdmin,
		AgentConfig:       sharedReconcilerFields.agentConfig,
		Backend:           sharedReconcilerFields.backend,
		Creds:             sharedReconcilerFields.creds,
		K8sClient:         k8sClient,
		Executor:          shell,
		Packages:          packages,
		KubeMode:          cfg.KubeMode,
		Namespace:         namespace,
		AuthAudience:      cfg.AuthAudience,
		ManagerConfigPath: envOrDefault("HICLAW_MANAGER_CONFIG_PATH", "/root/openclaw.json"),
		AgentFSDir:        envOrDefault("HICLAW_AGENT_FS_DIR", "/root/hiclaw-fs/agents"),
		WorkerAgentDir:    envOrDefault("HICLAW_WORKER_AGENT_DIR", "/opt/hiclaw/agent/worker-agent"),
		RegistryPath:      envOrDefault("HICLAW_REGISTRY_PATH", "/root/workers-registry.json"),
		StoragePrefix:     cfg.OSSStoragePrefix,
		MatrixDomain:      cfg.MatrixDomain,
		AdminUser:         cfg.MatrixAdminUser,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup WorkerReconciler")
		os.Exit(1)
	}

	if err := (&controller.TeamReconciler{
		Client:            mgr.GetClient(),
		Matrix:            sharedReconcilerFields.matrix,
		Gateway:           sharedReconcilerFields.gateway,
		OSS:               sharedReconcilerFields.oss,
		OSSAdmin:          sharedReconcilerFields.ossAdmin,
		AgentConfig:       sharedReconcilerFields.agentConfig,
		Backend:           sharedReconcilerFields.backend,
		Creds:             sharedReconcilerFields.creds,
		Executor:          shell,
		Packages:          packages,
		KubeMode:          cfg.KubeMode,
		ManagerConfigPath: envOrDefault("HICLAW_MANAGER_CONFIG_PATH", "/root/openclaw.json"),
		AgentFSDir:        envOrDefault("HICLAW_AGENT_FS_DIR", "/root/hiclaw-fs/agents"),
		WorkerAgentDir:    envOrDefault("HICLAW_WORKER_AGENT_DIR", "/opt/hiclaw/agent/worker-agent"),
		RegistryPath:      envOrDefault("HICLAW_REGISTRY_PATH", "/root/workers-registry.json"),
		StoragePrefix:     cfg.OSSStoragePrefix,
		MatrixDomain:      cfg.MatrixDomain,
		AdminUser:         cfg.MatrixAdminUser,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup TeamReconciler")
		os.Exit(1)
	}

	if err := (&controller.HumanReconciler{
		Client:       mgr.GetClient(),
		Matrix:       sharedReconcilerFields.matrix,
		MatrixDomain: cfg.MatrixDomain,
		Executor:     shell,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup HumanReconciler")
		os.Exit(1)
	}

	// --- Unified HTTP API server ---
	httpServer := server.NewHTTPServer(cfg.HTTPAddr, server.ServerDeps{
		Client:     mgr.GetClient(),
		Backend:    registry,
		Gateway:    gwClient,
		STS:        stsService,
		AuthMw:     authMw,
		KubeMode:   cfg.KubeMode,
		Namespace:  namespace,
		SocketPath: cfg.SocketPath,
	})

	go func() {
		if err := httpServer.Start(); err != nil {
			logger.Error(err, "HTTP server failed")
		}
	}()

	// --- Start controller manager (blocking) ---
	logger.Info("hiclaw-controller ready",
		"kubeMode", cfg.KubeMode,
		"httpAddr", cfg.HTTPAddr,
		"backends", len(workerBackends),
		"gateways", len(gatewayBackends),
		"sts", stsService != nil,
		"auth", k8sClient != nil,
	)
	fmt.Println("hiclaw-controller is running. Press Ctrl+C to stop.")

	if err := mgr.Start(ctx); err != nil {
		logger.Error(err, "controller manager exited with error")
		os.Exit(1)
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func buildCloudCredentials(cfg *config.Config) backend.CloudCredentialProvider {
	if cfg.SAEWorkerImage != "" || cfg.GWGatewayID != "" || cfg.OIDCTokenFile != "" || cfg.OSSBucket != "" {
		return backend.NewDefaultCloudCredentialProvider()
	}
	return nil
}

func buildBackends(cfg *config.Config, cloudCreds backend.CloudCredentialProvider) ([]backend.WorkerBackend, []backend.GatewayBackend) {
	var workers []backend.WorkerBackend
	var gateways []backend.GatewayBackend

	if cfg.KubeMode == "embedded" {
		workers = append(workers, backend.NewDockerBackend(cfg.DockerConfig(), cfg.ContainerPrefix))
	}

	switch cfg.WorkerBackend {
	case "k8s":
		if k8s, err := backend.NewK8sBackend(cfg.K8sConfig(), cfg.ContainerPrefix); err != nil {
			log.Printf("[WARN] Failed to create K8s backend: %v", err)
		} else {
			workers = append(workers, k8s)
		}
	case "sae":
		if cfg.SAEWorkerImage == "" || cloudCreds == nil {
			log.Printf("[WARN] SAE backend requested but config incomplete")
		} else {
			sae, err := backend.NewSAEBackend(cloudCreds, cfg.SAEConfig(), cfg.ContainerPrefix)
			if err != nil {
				log.Printf("[WARN] Failed to create SAE backend: %v", err)
			} else {
				workers = append(workers, sae)
			}
		}
	default:
		if cfg.SAEWorkerImage != "" && cloudCreds != nil {
			sae, err := backend.NewSAEBackend(cloudCreds, cfg.SAEConfig(), cfg.ContainerPrefix)
			if err != nil {
				log.Printf("[WARN] Failed to create SAE backend: %v", err)
			} else {
				workers = append(workers, sae)
			}
		} else if cfg.K8sNamespace != "" {
			if k8s, err := backend.NewK8sBackend(cfg.K8sConfig(), cfg.ContainerPrefix); err != nil {
				log.Printf("[WARN] Failed to create K8s backend: %v", err)
			} else {
				workers = append(workers, k8s)
			}
		}
	}

	if cfg.GWGatewayID != "" && cloudCreds != nil {
		apig, err := backend.NewAPIGBackend(cloudCreds, cfg.APIGConfig())
		if err != nil {
			log.Printf("[WARN] Failed to create APIG backend: %v", err)
		} else {
			gateways = append(gateways, apig)
		}
	}

	return workers, gateways
}
