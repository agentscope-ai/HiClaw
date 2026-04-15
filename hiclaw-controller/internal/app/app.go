package app

import (
	"context"
	"fmt"
	"log"

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
	"github.com/hiclaw/hiclaw-controller/internal/initializer"
	"github.com/hiclaw/hiclaw-controller/internal/matrix"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	"github.com/hiclaw/hiclaw-controller/internal/server"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"github.com/hiclaw/hiclaw-controller/internal/store"
	"github.com/hiclaw/hiclaw-controller/internal/watcher"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// App is the top-level application container. It centralizes dependency
// construction, wiring, and lifecycle management so that main.go stays minimal.
type App struct {
	cfg *config.Config
	mgr ctrl.Manager

	httpServer *server.HTTPServer

	// --- Build-time intermediates (populated during init*, consumed by later init* steps) ---
	scheme    *runtime.Scheme
	restCfg   *rest.Config
	k8sClient kubernetes.Interface
	authMw    *authpkg.Middleware
	namespace string

	// Executors
	shell    *executor.Shell
	packages *executor.PackageResolver

	// STS (optional, only when OIDC is configured)
	stsService *credentials.STSService

	// Infrastructure clients
	matrix   matrix.Client
	gateway  gateway.Client
	oss      oss.StorageClient
	ossAdmin oss.StorageAdminClient
	agentGen *agentconfig.Generator
	registry *backend.Registry

	// Service layer
	provisioner *service.Provisioner
	deployer    *service.Deployer
	envBuilder  *service.WorkerEnvBuilder
	legacy      *service.LegacyCompat
}

// New constructs the entire application dependency graph and wires everything
// together. It does NOT start any long-running goroutines — call Start for that.
func New(ctx context.Context, cfg *config.Config) (*App, error) {
	a := &App{cfg: cfg, namespace: cfg.Namespace()}

	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"scheme", a.initScheme},
		{"infra-clients", a.initInfraClients},
		{"backends", a.initBackends},
		{"controller-manager", a.initControllerManager},
		{"auth", a.initAuth},
		{"service-layer", a.initServiceLayer},
		{"reconcilers", a.initReconcilers},
		{"http-server", a.initHTTPServer},
	}

	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
	}

	return a, nil
}

// Start runs the HTTP server and controller manager. Blocks until ctx is cancelled.
func (a *App) Start(ctx context.Context) error {
	logger := ctrl.Log.WithName("app")

	go func() {
		if err := a.httpServer.Start(); err != nil {
			logger.Error(err, "HTTP server failed")
		}
	}()

	// Run cluster initialization in both embedded and incluster modes.
	{
		init := &initializer.Initializer{
			OSS:     a.oss,
			Matrix:  a.matrix,
			Gateway: a.gateway,
			RestCfg: a.restCfg,
			Config: initializer.Config{
				ManagerEnabled: a.cfg.ManagerEnabled,
				ManagerModel:   a.cfg.ManagerModel,
				ManagerRuntime: a.cfg.ManagerRuntime,
				ManagerImage:   a.cfg.ManagerImage,
				AdminUser:      a.cfg.MatrixAdminUser,
				AdminPassword:  a.cfg.MatrixAdminPassword,
				Namespace:      a.namespace,
				IsEmbedded:     a.cfg.KubeMode == "embedded",
			AgentFSDir:     a.cfg.AgentFSDir(),
				LLMProvider:    a.cfg.LLMProvider,
				LLMAPIKey:      a.cfg.LLMAPIKey,
				OpenAIBaseURL:  a.cfg.OpenAIBaseURL,
				TuwunelURL:     a.cfg.MatrixServerURL,
				ElementWebURL:  a.cfg.ElementWebURL,
			},
		}
		if err := init.Run(ctx); err != nil {
			logger.Error(err, "cluster initialization failed (non-fatal, continuing)")
		}
	}

	logger.Info("hiclaw-controller ready",
		"kubeMode", a.cfg.KubeMode,
		"httpAddr", a.cfg.HTTPAddr,
	)

	return a.mgr.Start(ctx)
}

// =========================================================================
// Initialization steps — called sequentially by New()
// =========================================================================

func (a *App) initScheme(_ context.Context) error {
	a.scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(a.scheme)
	if err := v1beta1.AddToScheme(a.scheme); err != nil {
		return fmt.Errorf("register CRD scheme: %w", err)
	}
	return nil
}

func (a *App) initInfraClients(_ context.Context) error {
	cfg := a.cfg
	a.matrix = matrix.NewTuwunelClient(cfg.MatrixConfig(), nil)
	a.gateway = gateway.NewHigressClient(cfg.GatewayConfig(), nil)
	a.oss = oss.NewMinIOClient(cfg.OSSConfig())
	if cfg.HasMinIOAdmin() {
		a.ossAdmin = oss.NewMinIOAdminClient(cfg.OSSConfig())
	}
	a.agentGen = agentconfig.NewGenerator(cfg.AgentConfig())

	a.shell = executor.NewShell(cfg.SkillsDir)
	a.packages = executor.NewPackageResolver("/tmp/import")

	if cfg.OIDCTokenFile != "" {
		a.stsService = credentials.NewSTSService(cfg.STSConfig())
	}
	return nil
}

func (a *App) initBackends(_ context.Context) error {
	cloudCreds := buildCloudCredentials(a.cfg)
	workerBackends, gatewayBackends := buildBackends(a.cfg, cloudCreds)
	a.registry = backend.NewRegistry(workerBackends, gatewayBackends)
	return nil
}

func (a *App) initControllerManager(ctx context.Context) error {
	var err error
	if a.cfg.KubeMode == "embedded" {
		a.restCfg, err = a.startEmbedded(ctx)
	} else {
		a.restCfg, err = a.startInCluster()
	}
	return err
}

func (a *App) initAuth(_ context.Context) error {
	logger := ctrl.Log.WithName("app")

	if a.restCfg != nil {
		var err error
		a.k8sClient, err = kubernetes.NewForConfig(a.restCfg)
		if err != nil {
			return fmt.Errorf("create kubernetes client: %w", err)
		}
		authenticator := authpkg.NewTokenReviewAuthenticator(a.k8sClient, a.cfg.AuthAudience)
		enricher := authpkg.NewCREnricher(a.mgr.GetClient(), a.namespace)
		authorizer := authpkg.NewAuthorizer()
		a.authMw = authpkg.NewMiddleware(authenticator, enricher, authorizer, a.mgr.GetClient(), a.namespace)
		logger.Info("K8s SA token authentication enabled", "audience", a.cfg.AuthAudience)
	} else {
		a.authMw = authpkg.NewMiddleware(nil, nil, authpkg.NewAuthorizer(), nil, a.namespace)
		logger.Info("authentication disabled (no REST config)")
	}
	return nil
}

func (a *App) initServiceLayer(_ context.Context) error {
	cfg := a.cfg

	var credStore service.CredentialStore
	if cfg.KubeMode == "incluster" && a.k8sClient != nil {
		credStore = &service.SecretCredentialStore{Client: a.k8sClient, Namespace: a.namespace}
	} else {
		credStore = &service.FileCredentialStore{Dir: cfg.CredsDir()}
	}

	a.provisioner = service.NewProvisioner(service.ProvisionerConfig{
		Matrix:            a.matrix,
		Gateway:           a.gateway,
		OSSAdmin:          a.ossAdmin,
		Creds:             credStore,
		K8sClient:         a.k8sClient,
		KubeMode:          cfg.KubeMode,
		Namespace:         a.namespace,
		AuthAudience:      cfg.AuthAudience,
		MatrixDomain:      cfg.MatrixDomain,
		AdminUser:         cfg.MatrixAdminUser,
		ManagerPassword:   cfg.ManagerPassword,
		ManagerGatewayKey: cfg.ManagerGatewayKey,
	})

	a.envBuilder = service.NewWorkerEnvBuilder(cfg.WorkerEnv)

	if cfg.KubeMode == "embedded" {
		a.legacy = service.NewLegacyCompat(service.LegacyConfig{
			OSS:          a.oss,
			MatrixDomain: cfg.MatrixDomain,
			AgentFSDir:   cfg.AgentFSDir(),
		})
	}

	a.deployer = service.NewDeployer(service.DeployerConfig{
		AgentConfig:    a.agentGen,
		OSS:            a.oss,
		Executor:       a.shell,
		Packages:       a.packages,
		Legacy:         a.legacy,
		AgentFSDir:     cfg.AgentFSDir(),
		WorkerAgentDir: cfg.WorkerAgentDir(),
		MatrixDomain:   cfg.MatrixDomain,
	})

	return nil
}

func (a *App) initReconcilers(_ context.Context) error {
	if err := (&controller.WorkerReconciler{
		Client:      a.mgr.GetClient(),
		Provisioner: a.provisioner,
		Deployer:    a.deployer,
		Backend:     a.registry,
		EnvBuilder:  a.envBuilder,
		Legacy:      a.legacy,
	}).SetupWithManager(a.mgr); err != nil {
		return fmt.Errorf("setup WorkerReconciler: %w", err)
	}

	if err := (&controller.TeamReconciler{
		Client:      a.mgr.GetClient(),
		Provisioner: a.provisioner,
		Deployer:    a.deployer,
		Legacy:      a.legacy,
		AgentFSDir:  a.cfg.AgentFSDir(),
	}).SetupWithManager(a.mgr); err != nil {
		return fmt.Errorf("setup TeamReconciler: %w", err)
	}

	if err := (&controller.HumanReconciler{
		Client: a.mgr.GetClient(),
		Matrix: a.matrix,
		Legacy: a.legacy,
	}).SetupWithManager(a.mgr); err != nil {
		return fmt.Errorf("setup HumanReconciler: %w", err)
	}

	mgrReconciler := &controller.ManagerReconciler{
		Client:           a.mgr.GetClient(),
		Provisioner:      a.provisioner,
		Deployer:         a.deployer,
		Backend:          a.registry,
		EnvBuilder:       a.envBuilder,
		ManagerResources: a.cfg.ManagerResources(),
	}
	if a.cfg.KubeMode == "embedded" {
		mgrReconciler.EmbeddedConfig = &controller.ManagerEmbeddedConfig{
			WorkspaceDir:       a.cfg.ManagerWorkspaceDir,
			HostShareDir:       a.cfg.HostShareDir,
			ExtraEnv:           a.cfg.ManagerAgentEnv(),
			ManagerConsolePort: a.cfg.ManagerConsolePort,
		}
	}
	if err := mgrReconciler.SetupWithManager(a.mgr); err != nil {
		return fmt.Errorf("setup ManagerReconciler: %w", err)
	}

	return nil
}

func (a *App) initHTTPServer(_ context.Context) error {
	a.httpServer = server.NewHTTPServer(a.cfg.HTTPAddr, server.ServerDeps{
		Client:     a.mgr.GetClient(),
		Backend:    a.registry,
		Gateway:    a.gateway,
		OSS:        a.oss,
		STS:        a.stsService,
		AuthMw:     a.authMw,
		KubeMode:   a.cfg.KubeMode,
		Namespace:  a.namespace,
		SocketPath: a.cfg.SocketPath,
	})
	return nil
}

// =========================================================================
// Controller-manager bootstrapping (embedded vs incluster)
// =========================================================================

func (a *App) startEmbedded(ctx context.Context) (*rest.Config, error) {
	logger := ctrl.Log.WithName("app")
	cfg := a.cfg
	logger.Info("starting embedded mode", "dataDir", cfg.DataDir, "configDir", cfg.ConfigDir)

	kineServer, err := store.StartKine(ctx, store.Config{
		DataDir:       cfg.DataDir,
		ListenAddress: "127.0.0.1:2379",
	})
	if err != nil {
		return nil, fmt.Errorf("start kine: %w", err)
	}
	logger.Info("kine started", "endpoints", kineServer.ETCDConfig.Endpoints)

	restCfg, err := apiserver.Start(ctx, apiserver.Config{
		DataDir:    cfg.DataDir,
		EtcdURL:    "http://127.0.0.1:2379",
		BindAddr:   "127.0.0.1",
		SecurePort: "6443",
		CRDDir:     cfg.CRDDir,
	})
	if err != nil {
		return nil, fmt.Errorf("start embedded kube-apiserver: %w", err)
	}
	logger.Info("embedded kube-apiserver ready")

	a.mgr, err = ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: a.scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create controller manager: %w", err)
	}

	fw := watcher.New(cfg.ConfigDir, a.mgr.GetClient())
	if err := fw.InitialSync(ctx); err != nil {
		logger.Error(err, "initial sync failed (non-fatal)")
	}
	go func() {
		if err := fw.Watch(ctx); err != nil && ctx.Err() == nil {
			logger.Error(err, "file watcher stopped unexpectedly")
		}
	}()
	logger.Info("file watcher started", "dir", cfg.ConfigDir)

	return restCfg, nil
}

func (a *App) startInCluster() (*rest.Config, error) {
	logger := ctrl.Log.WithName("app")
	logger.Info("starting in-cluster mode")

	restCfg := ctrl.GetConfigOrDie()
	opts := ctrl.Options{Scheme: a.scheme}
	if a.cfg.K8sNamespace != "" {
		opts.Cache.DefaultNamespaces = map[string]cache.Config{
			a.cfg.K8sNamespace: {},
		}
	}
	var err error
	a.mgr, err = ctrl.NewManager(restCfg, opts)
	if err != nil {
		return nil, fmt.Errorf("create controller manager: %w", err)
	}
	return restCfg, nil
}

// =========================================================================
// Backend construction
// =========================================================================

func buildCloudCredentials(cfg *config.Config) backend.CloudCredentialProvider {
	if cfg.GWGatewayID != "" || cfg.OIDCTokenFile != "" || cfg.OSSBucket != "" {
		return backend.NewDefaultCloudCredentialProvider()
	}
	return nil
}

func buildBackends(cfg *config.Config, cloudCreds backend.CloudCredentialProvider) ([]backend.WorkerBackend, []backend.GatewayBackend) {
	var workers []backend.WorkerBackend
	var gateways []backend.GatewayBackend

	// Embedded mode always has a Docker backend as the primary option.
	if cfg.KubeMode == "embedded" {
		workers = append(workers, backend.NewDockerBackend(cfg.DockerConfig(), cfg.ContainerPrefix))
	}

	// Explicit backend selection; "k8s" is the default for incluster mode.
	effectiveBackend := cfg.WorkerBackend
	if effectiveBackend == "" && cfg.KubeMode == "incluster" {
		effectiveBackend = "k8s"
	}

	switch effectiveBackend {
	case "k8s":
		if k8s, err := backend.NewK8sBackend(cfg.K8sConfig(), cfg.ContainerPrefix); err != nil {
			log.Printf("[WARN] Failed to create K8s backend: %v", err)
		} else {
			workers = append(workers, k8s)
		}
	}

	// Cloud API Gateway backend (optional, additive)
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
