package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileStorage idempotently ensures the team's shared storage prefix
// exists in OSS (embedded mode only; no-op when no OSS client is wired).
// Non-critical: a transient failure is logged and reconciliation
// continues — shared storage is orthogonal to the rest of Team operation.
func (r *TeamReconciler) reconcileStorage(ctx context.Context, s *teamScope) {
	if err := r.Provisioner.EnsureTeamStorage(ctx, s.team.Name); err != nil {
		log.FromContext(ctx).Error(err, "team shared storage ensure failed (non-fatal)",
			"team", s.team.Name)
	}
}
