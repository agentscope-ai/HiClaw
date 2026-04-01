package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/alibaba/hiclaw/orchestrator/backend"
	"github.com/alibaba/hiclaw/orchestrator/internal/httputil"
)

// GatewayHandler handles /gateway/* HTTP requests.
type GatewayHandler struct {
	registry *backend.Registry
}

// NewGatewayHandler creates a GatewayHandler.
func NewGatewayHandler(registry *backend.Registry) *GatewayHandler {
	return &GatewayHandler{registry: registry}
}

// CreateConsumer handles POST /gateway/consumers.
func (h *GatewayHandler) CreateConsumer(w http.ResponseWriter, r *http.Request) {
	b, err := h.registry.GetGatewayBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusNotImplemented, "no gateway backend available")
		return
	}

	var req CreateConsumerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	result, err := b.CreateConsumer(r.Context(), backend.ConsumerRequest{Name: req.Name})
	if err != nil {
		log.Printf("[ERROR] create consumer %s: %v", req.Name, err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, ConsumerResponse{
		Name:       result.Name,
		ConsumerID: result.ConsumerID,
		APIKey:     result.APIKey,
		Status:     result.Status,
	})
}

// BindConsumer handles POST /gateway/consumers/{id}/bind.
func (h *GatewayHandler) BindConsumer(w http.ResponseWriter, r *http.Request) {
	b, err := h.registry.GetGatewayBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusNotImplemented, "no gateway backend available")
		return
	}

	consumerID := r.PathValue("id")
	if consumerID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "consumer ID is required")
		return
	}

	var req BindConsumerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	err = b.BindConsumer(r.Context(), backend.BindRequest{
		ConsumerID: consumerID,
		ModelAPIID: req.ModelAPIID,
		EnvID:      req.EnvID,
	})
	if err != nil {
		log.Printf("[ERROR] bind consumer %s: %v", consumerID, err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteConsumer handles DELETE /gateway/consumers/{id}.
func (h *GatewayHandler) DeleteConsumer(w http.ResponseWriter, r *http.Request) {
	b, err := h.registry.GetGatewayBackend(r.Context(), "")
	if err != nil {
		httputil.WriteError(w, http.StatusNotImplemented, "no gateway backend available")
		return
	}

	consumerID := r.PathValue("id")
	if consumerID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "consumer ID is required")
		return
	}

	if err := b.DeleteConsumer(r.Context(), consumerID); err != nil {
		log.Printf("[ERROR] delete consumer %s: %v", consumerID, err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
