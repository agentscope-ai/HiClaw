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

	// --- Cloud credentials (shared by SAE, APIG, STS, OSS key persistence) ---
	var cloudCreds backend.CloudCredentialProvider
	if cfg.Runtime == "aliyun" {
		cloudCreds = backend.NewDefaultCloudCredentialProvider()
	}

	// --- Auth ---
	var persister authpkg.KeyPersister
	if cfg.Runtime == "aliyun" && cloudCreds != nil && cfg.OSSBucket != "" {
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

	// --- Docker API passthrough handler ---
	proxyHandler := proxy.NewHandler(cfg.SocketPath, validator)

	// --- Worker backends ---
	var workerBackends []backend.WorkerBackend

	// Docker backend (always registered; Available() checks socket at runtime)
	dockerBackend := backend.NewDockerBackend(cfg.SocketPath, cfg.ContainerPrefix)
	workerBackends = append(workerBackends, dockerBackend)

	// SAE backend (cloud mode)
	var saeBackend *backend.SAEBackend
	if cfg.Runtime == "aliyun" && cloudCreds != nil {
		var err error
		saeBackend, err = backend.NewSAEBackend(cloudCreds, cfg.SAEConfig(), cfg.ContainerPrefix)
		if err != nil {
			log.Printf("[WARN] Failed to create SAE backend: %v", err)
		} else {
			workerBackends = append(workerBackends, saeBackend)
		}
	}

	// --- Gateway backends ---
	var gatewayBackends []backend.GatewayBackend
	if cfg.Runtime == "aliyun" && cloudCreds != nil {
		apigBackend, err := backend.NewAPIGBackend(cloudCreds, cfg.APIGConfig())
		if err != nil {
			log.Printf("[WARN] Failed to create APIG backend: %v", err)
		} else {
			gatewayBackends = append(gatewayBackends, apigBackend)
		}
	}

	registry := backend.NewRegistry(workerBackends, gatewayBackends)

	// --- STS service ---
	var stsService *credentials.STSService
	if cfg.Runtime == "aliyun" && cfg.OIDCTokenFile != "" {
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
	if cfg.Runtime == "aliyun" {
		log.Printf("Cloud mode: SAE=%v, APIG=%v, STS=%v", saeBackend != nil, len(gatewayBackends) > 0, stsService != nil)
	} else {
		log.Printf("Local mode: docker socket=%s", cfg.SocketPath)
	}
	if keyStore.AuthEnabled() {
		log.Printf("Auth: enabled (manager key configured)")
	}
	if len(validator.AllowedRegistries) > 0 {
		log.Printf("Allowed registries: %v", validator.AllowedRegistries)
	}
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
