package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/alibaba/hiclaw/orchestrator/auth"
	"github.com/alibaba/hiclaw/orchestrator/backend"
	"github.com/alibaba/hiclaw/orchestrator/internal/httputil"
)

// WorkerHandler handles /workers/* HTTP requests.
type WorkerHandler struct {
	registry        *backend.Registry
	keyStore        *auth.KeyStore
	orchestratorURL string

	// Readiness tracking — workers report ready via POST /workers/{name}/ready
	readyMu sync.RWMutex
	ready   map[string]bool
}

// NewWorkerHandler creates a WorkerHandler.
func NewWorkerHandler(registry *backend.Registry, keyStore *auth.KeyStore, orchestratorURL string) *WorkerHandler {
	return &WorkerHandler{
		registry:        registry,
		keyStore:        keyStore,
		orchestratorURL: orchestratorURL,
		ready:           make(map[string]bool),
	}
}

// Create handles POST /workers.
func (h *WorkerHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateWorkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !backend.ValidRuntime(req.Runtime) {
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid runtime %q, supported: openclaw, copaw", req.Runtime))
		return
	}

	b, err := h.registry.GetWorkerBackend(r.Context(), req.Backend)
	if err != nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Generate API key for backends that need orchestrator-mediated credentials
	var apiKey string
	if b.NeedsCredentialInjection() && h.keyStore != nil && h.keyStore.AuthEnabled() {
		apiKey = h.keyStore.GenerateWorkerKey(req.Name)
	}

	// Clear any stale readiness state
	h.setReady(req.Name, false)

	result, err := b.Create(r.Context(), backend.CreateRequest{
		Name:            req.Name,
		Image:           req.Image,
		Runtime:         req.Runtime,
		Env:             req.Env,
		Network:         req.Network,
		ExtraHosts:      req.ExtraHosts,
		WorkingDir:      req.WorkingDir,
		OrchestratorURL: h.orchestratorURL,
		WorkerAPIKey:    apiKey,
	})
	if err != nil {
		log.Printf("[ERROR] create worker %s: %v", req.Name, err)
		if apiKey != "" {
			h.keyStore.RemoveWorkerKey(req.Name)
		}
		writeBackendError(w, err)
		return
	}

	resp := toWorkerResponse(result)
	resp.APIKey = apiKey
	httputil.WriteJSON(w, http.StatusCreated, resp)
}

// List handles GET /workers.
func (h *WorkerHandler) List(w http.ResponseWriter, r *http.Request) {
	b, err := h.registry.GetWorkerBackend(r.Context(), "")
	if err != nil {
		httputil.WriteJSON(w, http.StatusOK, WorkerListResponse{Workers: []WorkerResponse{}})
		return
	}

	results, err := b.List(r.Context())
	if err != nil {
		log.Printf("[ERROR] list workers: %v", err)
		writeBackendError(w, err)
		return
	}

	workers := make([]WorkerResponse, 0, len(results))
	for _, r := range results {
		resp := toWorkerResponse(&r)
		resp.Status = h.mergeReadiness(r.Name, resp.Status)
		workers = append(workers, resp)
	}
	httputil.WriteJSON(w, http.StatusOK, WorkerListResponse{Workers: workers})
}

// Status handles GET /workers/{name}.
func (h *WorkerHandler) Status(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	b, err := h.registry.GetWorkerBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	result, err := b.Status(r.Context(), name)
	if err != nil {
		log.Printf("[ERROR] status worker %s: %v", name, err)
		writeBackendError(w, err)
		return
	}

	resp := toWorkerResponse(result)
	resp.Status = h.mergeReadiness(name, resp.Status)
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// Ready handles POST /workers/{name}/ready — worker reports itself as ready.
func (h *WorkerHandler) Ready(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	// Verify the caller is the worker itself.
	// When auth is disabled (local mode), caller is nil — allow any caller
	// since the network is trusted (Docker bridge).
	caller := auth.CallerFromContext(r.Context())
	if caller != nil && caller.WorkerName != name {
		httputil.WriteError(w, http.StatusForbidden, "workers can only report their own readiness")
		return
	}

	h.setReady(name, true)
	log.Printf("[READY] Worker %s reported ready", name)
	w.WriteHeader(http.StatusNoContent)
}

// Start handles POST /workers/{name}/start.
func (h *WorkerHandler) Start(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	b, err := h.registry.GetWorkerBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Clear readiness on restart
	h.setReady(name, false)

	if err := b.Start(r.Context(), name); err != nil {
		log.Printf("[ERROR] start worker %s: %v", name, err)
		writeBackendError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Stop handles POST /workers/{name}/stop.
func (h *WorkerHandler) Stop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	b, err := h.registry.GetWorkerBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Clear readiness on stop
	h.setReady(name, false)

	if err := b.Stop(r.Context(), name); err != nil {
		log.Printf("[ERROR] stop worker %s: %v", name, err)
		writeBackendError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Delete handles DELETE /workers/{name}.
func (h *WorkerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	b, err := h.registry.GetWorkerBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	if err := b.Delete(r.Context(), name); err != nil {
		log.Printf("[ERROR] delete worker %s: %v", name, err)
		writeBackendError(w, err)
		return
	}

	if h.keyStore != nil {
		h.keyStore.RemoveWorkerKey(name)
	}
	h.setReady(name, false)

	w.WriteHeader(http.StatusNoContent)
}

// --- readiness helpers ---

func (h *WorkerHandler) setReady(name string, ready bool) {
	h.readyMu.Lock()
	defer h.readyMu.Unlock()
	if ready {
		h.ready[name] = true
	} else {
		delete(h.ready, name)
	}
}

func (h *WorkerHandler) isReady(name string) bool {
	h.readyMu.RLock()
	defer h.readyMu.RUnlock()
	return h.ready[name]
}

// mergeReadiness upgrades "running" to "ready" if the worker has reported ready.
func (h *WorkerHandler) mergeReadiness(name string, status backend.WorkerStatus) backend.WorkerStatus {
	if status == backend.StatusRunning && h.isReady(name) {
		return backend.StatusReady
	}
	return status
}

// --- response helpers ---

func toWorkerResponse(r *backend.WorkerResult) WorkerResponse {
	return WorkerResponse{
		Name:        r.Name,
		Backend:     r.Backend,
		Status:      r.Status,
		ContainerID: r.ContainerID,
		AppID:       r.AppID,
		RawStatus:   r.RawStatus,
	}
}

func writeBackendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backend.ErrConflict):
		httputil.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, backend.ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, err.Error())
	default:
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
	}
}
