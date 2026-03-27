package main

import (
	"context"
	"log"
	"net/http"

	"github.com/alibaba/hiclaw/orchestrator/api"
	authpkg "github.com/alibaba/hiclaw/orchestrator/auth"
	"github.com/alibaba/hiclaw/orchestrator/backend"
	"github.com/alibaba/hiclaw/orchestrator/credentials"
	"github.com/alibaba/hiclaw/orchestrator/proxy"
)

func main() {
	cfg := LoadConfig()

	// --- Cloud credentials (shared by SAE, APIG, STS, OSS) ---
	// Created once if any cloud config is present; nil otherwise.
	cloudCreds := buildCloudCredentials(cfg)

	// --- Auth ---
	var persister authpkg.KeyPersister
	if cloudCreds != nil && cfg.OSSBucket != "" {
		cred, err := cloudCreds.GetCredential()
		if err != nil {
			log.Printf("[WARN] Failed to get credentials for key persistence: %v", err)
		} else {
			persister = authpkg.NewOSSKeyPersister(cfg.Region, cfg.OSSBucket, cred)
		}
	}

	keyStore := authpkg.NewKeyStore(cfg.ManagerAPIKey, persister)
	if err := keyStore.Recover(context.Background()); err != nil {
		log.Printf("[WARN] Failed to recover worker keys: %v", err)
	}
	authMw := authpkg.NewMiddleware(keyStore)

	// --- Security validator (for Docker API passthrough) ---
	validator := proxy.NewSecurityValidator()
	proxyHandler := proxy.NewHandler(cfg.SocketPath, validator)

	// --- Backends (config-driven, no runtime string checks) ---
	workerBackends, gatewayBackends := buildBackends(cfg, cloudCreds)
	registry := backend.NewRegistry(workerBackends, gatewayBackends)

	// --- STS service (enabled if OIDC token file is configured) ---
	var stsService *credentials.STSService
	if cfg.OIDCTokenFile != "" {
		stsService = credentials.NewSTSService(cfg.STSConfig())
	}

	// --- API handlers ---
	workerHandler := api.NewWorkerHandler(registry, keyStore, cfg.OrchestratorURL)
	gatewayHandler := api.NewGatewayHandler(registry)
	stsHandler := credentials.NewHandler(stsService)

	// --- Route registration with auth ---
	mux := http.NewServeMux()

	// Worker lifecycle API — manager only
	mux.Handle("POST /workers", authMw.RequireManager(http.HandlerFunc(workerHandler.Create)))
	mux.Handle("GET /workers", authMw.RequireManager(http.HandlerFunc(workerHandler.List)))
	mux.Handle("GET /workers/{name}", authMw.RequireManager(http.HandlerFunc(workerHandler.Status)))
	mux.Handle("POST /workers/{name}/start", authMw.RequireManager(http.HandlerFunc(workerHandler.Start)))
	mux.Handle("POST /workers/{name}/stop", authMw.RequireManager(http.HandlerFunc(workerHandler.Stop)))
	mux.Handle("DELETE /workers/{name}", authMw.RequireManager(http.HandlerFunc(workerHandler.Delete)))

	// Worker readiness — workers report themselves as ready
	mux.Handle("POST /workers/{name}/ready", authMw.RequireWorker(http.HandlerFunc(workerHandler.Ready)))

	// Gateway API — manager only
	mux.Handle("POST /gateway/consumers", authMw.RequireManager(http.HandlerFunc(gatewayHandler.CreateConsumer)))
	mux.Handle("POST /gateway/consumers/{id}/bind", authMw.RequireManager(http.HandlerFunc(gatewayHandler.BindConsumer)))
	mux.Handle("DELETE /gateway/consumers/{id}", authMw.RequireManager(http.HandlerFunc(gatewayHandler.DeleteConsumer)))

	// STS token refresh — workers only
	mux.Handle("POST /credentials/sts", authMw.RequireWorker(http.HandlerFunc(stsHandler.RefreshToken)))

	// Docker API passthrough (catch-all) — manager only
	mux.Handle("/", authMw.RequireManager(proxyHandler))

	// --- Start server ---
	log.Printf("hiclaw-orchestrator listening on %s", cfg.ListenAddr)
	log.Printf("Backends: workers=%d, gateways=%d, STS=%v, auth=%v",
		len(workerBackends), len(gatewayBackends), stsService != nil, keyStore.AuthEnabled())
	if len(validator.AllowedRegistries) > 0 {
		log.Printf("Allowed registries: %v", validator.AllowedRegistries)
	}
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// buildCloudCredentials creates a cloud credential provider if any cloud-related
// config is present (SAE image, APIG gateway, OIDC token, OSS bucket).
func buildCloudCredentials(cfg *Config) backend.CloudCredentialProvider {
	if cfg.SAEWorkerImage != "" || cfg.GWGatewayID != "" || cfg.OIDCTokenFile != "" || cfg.OSSBucket != "" {
		return backend.NewDefaultCloudCredentialProvider()
	}
	return nil
}

// buildBackends creates all worker and gateway backends based on config.
// Each backend is registered if its required config is present.
func buildBackends(cfg *Config, cloudCreds backend.CloudCredentialProvider) ([]backend.WorkerBackend, []backend.GatewayBackend) {
	var workers []backend.WorkerBackend
	var gateways []backend.GatewayBackend

	// Docker backend — always registered; Available() checks socket at runtime
	workers = append(workers, backend.NewDockerBackend(cfg.DockerConfig(), cfg.ContainerPrefix))

	// SAE backend — registered if worker image is configured
	if cfg.SAEWorkerImage != "" && cloudCreds != nil {
		sae, err := backend.NewSAEBackend(cloudCreds, cfg.SAEConfig(), cfg.ContainerPrefix)
		if err != nil {
			log.Printf("[WARN] Failed to create SAE backend: %v", err)
		} else {
			workers = append(workers, sae)
		}
	}

	// APIG gateway backend — registered if gateway ID is configured
	if cfg.GWGatewayID != "" && cloudCreds != nil {
		apig, err := backend.NewAPIGBackend(cloudCreds, cfg.APIGConfig())
		if err != nil {
			log.Printf("[WARN] Failed to create APIG backend: %v", err)
		} else {
			gateways = append(gateways, apig)
		}
	}

	// Future: K8s backend
	// if cfg.K8sKubeconfig != "" { workers = append(workers, backend.NewK8sBackend(...)) }

	return workers, gateways
}
