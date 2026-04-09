package server

import (
	"errors"
	"log"
	"net/http"
	"sync"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/httputil"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LifecycleHandler handles imperative worker lifecycle operations.
type LifecycleHandler struct {
	k8s       client.Client
	registry  *backend.Registry
	namespace string

	readyMu sync.RWMutex
	ready   map[string]bool
}

func NewLifecycleHandler(k8s client.Client, registry *backend.Registry, namespace string) *LifecycleHandler {
	return &LifecycleHandler{
		k8s:       k8s,
		registry:  registry,
		namespace: namespace,
		ready:     make(map[string]bool),
	}
}

// Wake handles POST /api/v1/workers/{name}/wake
func (h *LifecycleHandler) Wake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	var worker v1beta1.Worker
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &worker); err != nil {
		writeK8sError(w, "get worker", err)
		return
	}

	b := h.registry.DetectWorkerBackend(r.Context())
	if b == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "no worker backend available")
		return
	}

	h.setReady(name, false)

	if err := b.Start(r.Context(), name); err != nil {
		log.Printf("[ERROR] wake worker %s: %v", name, err)
		writeBackendError(w, err)
		return
	}

	worker.Status.Phase = "Running"
	worker.Status.Message = ""
	if err := h.k8s.Status().Update(r.Context(), &worker); err != nil {
		log.Printf("[WARN] failed to update worker status after wake: %v", err)
	}

	httputil.WriteJSON(w, http.StatusOK, WorkerLifecycleResponse{Name: name, Phase: "Running"})
}

// Sleep handles POST /api/v1/workers/{name}/sleep
func (h *LifecycleHandler) Sleep(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	var worker v1beta1.Worker
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &worker); err != nil {
		writeK8sError(w, "get worker", err)
		return
	}

	b := h.registry.DetectWorkerBackend(r.Context())
	if b == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "no worker backend available")
		return
	}

	h.setReady(name, false)

	if err := b.Stop(r.Context(), name); err != nil {
		log.Printf("[ERROR] sleep worker %s: %v", name, err)
		writeBackendError(w, err)
		return
	}

	worker.Status.Phase = "Sleeping"
	worker.Status.Message = ""
	if err := h.k8s.Status().Update(r.Context(), &worker); err != nil {
		log.Printf("[WARN] failed to update worker status after sleep: %v", err)
	}

	httputil.WriteJSON(w, http.StatusOK, WorkerLifecycleResponse{Name: name, Phase: "Sleeping"})
}

// EnsureReady handles POST /api/v1/workers/{name}/ensure-ready
func (h *LifecycleHandler) EnsureReady(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	var worker v1beta1.Worker
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &worker); err != nil {
		writeK8sError(w, "get worker", err)
		return
	}

	b := h.registry.DetectWorkerBackend(r.Context())
	if b == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "no worker backend available")
		return
	}

	if worker.Status.Phase == "Stopped" || worker.Status.Phase == "Sleeping" {
		h.setReady(name, false)
		if err := b.Start(r.Context(), name); err != nil {
			log.Printf("[ERROR] ensure-ready start worker %s: %v", name, err)
			writeBackendError(w, err)
			return
		}
		worker.Status.Phase = "Running"
		worker.Status.Message = ""
		_ = h.k8s.Status().Update(r.Context(), &worker)
	}

	phase := worker.Status.Phase
	if phase == "Running" && h.isReady(name) {
		phase = "Ready"
	}

	httputil.WriteJSON(w, http.StatusOK, WorkerLifecycleResponse{Name: name, Phase: phase})
}

// Ready handles POST /api/v1/workers/{name}/ready — worker self-reports readiness.
func (h *LifecycleHandler) Ready(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	// Authorization (self-only for workers) is enforced by RequireAuthz middleware.
	h.setReady(name, true)
	log.Printf("[READY] Worker %s reported ready", name)
	w.WriteHeader(http.StatusNoContent)
}

// GetWorkerRuntimeStatus handles GET /api/v1/workers/{name}/status — aggregates CR + backend state.
func (h *LifecycleHandler) GetWorkerRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker name is required")
		return
	}

	var worker v1beta1.Worker
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Name: name, Namespace: h.namespace}, &worker); err != nil {
		writeK8sError(w, "get worker", err)
		return
	}

	resp := workerToResponse(&worker)

	b := h.registry.DetectWorkerBackend(r.Context())
	if b != nil {
		result, err := b.Status(r.Context(), name)
		if err == nil && result != nil {
			resp.Message = "backend=" + result.Backend + " status=" + string(result.Status)
			resp.ContainerState = string(result.Status)
			if result.Status == backend.StatusRunning && h.isReady(name) {
				resp.Phase = "Ready"
			}
		}
	}

	httputil.WriteJSON(w, http.StatusOK, resp)
}

// --- readiness helpers ---

func (h *LifecycleHandler) setReady(name string, ready bool) {
	h.readyMu.Lock()
	defer h.readyMu.Unlock()
	if ready {
		h.ready[name] = true
	} else {
		delete(h.ready, name)
	}
}

func (h *LifecycleHandler) isReady(name string) bool {
	h.readyMu.RLock()
	defer h.readyMu.RUnlock()
	return h.ready[name]
}

func writeBackendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backend.ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, backend.ErrConflict):
		httputil.WriteError(w, http.StatusConflict, err.Error())
	default:
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
	}
}
