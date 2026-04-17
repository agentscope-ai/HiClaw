package controller

import (
	"context"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileHumanLegacy writes a humans-registry.json entry for the Manager
// Agent skill scripts to consult. The registry shape mirrors the new
// HumanSpec directly (SuperAdmin / TeamAccess / WorkerAccess); skill
// scripts are updated in lockstep (see docs/design team-refactor Stage 13).
// Non-critical: Legacy nil / disabled short-circuits; errors logged.
func (r *HumanReconciler) reconcileHumanLegacy(ctx context.Context, s *humanScope) {
	if r.Legacy == nil || !r.Legacy.Enabled() {
		return
	}
	logger := log.FromContext(ctx)
	h := s.human

	entry := service.HumanRegistryEntry{
		Name:         h.Name,
		MatrixUserID: h.Status.MatrixUserID,
		DisplayName:  h.Spec.DisplayName,
		SuperAdmin:   h.Spec.SuperAdmin,
		TeamAccess:   convertTeamAccess(h.Spec.TeamAccess),
		WorkerAccess: append([]string(nil), h.Spec.WorkerAccess...),
	}
	if err := r.Legacy.UpdateHumansRegistry(entry); err != nil {
		logger.Error(err, "humans-registry update failed (non-fatal)", "human", h.Name)
	}
}

// convertTeamAccess projects the CR-level TeamAccessEntry list into the
// registry-level HumanTeamAccess list. Empty input yields nil so the
// JSON serializer can omit the key.
func convertTeamAccess(in []v1beta1.TeamAccessEntry) []service.HumanTeamAccess {
	if len(in) == 0 {
		return nil
	}
	out := make([]service.HumanTeamAccess, 0, len(in))
	for _, entry := range in {
		out = append(out, service.HumanTeamAccess{
			Team: entry.Team,
			Role: string(entry.Role),
		})
	}
	return out
}
