package credentials

import (
	"log"
	"net/http"

	"github.com/alibaba/hiclaw/orchestrator/auth"
	"github.com/alibaba/hiclaw/orchestrator/internal/httputil"
)

// Handler handles /credentials/* HTTP requests.
type Handler struct {
	stsService *STSService
}

// NewHandler creates a credentials Handler.
func NewHandler(stsService *STSService) *Handler {
	return &Handler{stsService: stsService}
}

// RefreshToken handles POST /credentials/sts.
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	if h.stsService == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "STS service not available (not in cloud mode)")
		return
	}

	caller := auth.CallerFromContext(r.Context())
	workerName := ""
	if caller != nil {
		workerName = caller.WorkerName
	}
	if workerName == "" {
		httputil.WriteError(w, http.StatusBadRequest, "worker identity not found in request context")
		return
	}

	token, err := h.stsService.IssueWorkerToken(r.Context(), workerName)
	if err != nil {
		log.Printf("[ERROR] issue STS token for worker %s: %v", workerName, err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to issue STS token: "+err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, token)
}
