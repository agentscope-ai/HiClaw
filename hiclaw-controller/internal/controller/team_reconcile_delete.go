package controller

import (
	"context"

	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileTeamDelete handles the Team CR finalizer path. It cleans up
// only Team-scoped resources (shared storage; teams-registry entry).
// Matrix Rooms are intentionally preserved — re-creating a Team with the
// same name can then reuse the existing rooms through the idempotent
// EnsureTeamRooms path.
//
// Critically: Worker CRs are never deleted from this path. The user-facing
// "cascading delete" (hiclaw delete team) is implemented at the REST API
// bundle layer, not here. kubectl delete team / client-go delete Team
// deliberately leaves Workers alive as orphans to be explicitly handled
// by the operator.
func (r *TeamReconciler) reconcileTeamDelete(ctx context.Context, s *teamScope) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	t := s.team
	logger.Info("deleting team", "name", t.Name)

	if err := r.Provisioner.CleanupTeamInfra(ctx, service.TeamCleanupRequest{
		TeamName:       t.Name,
		TeamRoomID:     t.Status.TeamRoomID,
		LeaderDMRoomID: t.Status.LeaderDMRoomID,
	}); err != nil {
		logger.Error(err, "team infra cleanup failed (non-fatal)", "team", t.Name)
	}

	if r.Legacy != nil && r.Legacy.Enabled() {
		if err := r.Legacy.RemoveFromTeamsRegistry(ctx, t.Name); err != nil {
			logger.Error(err, "teams-registry remove failed (non-fatal)", "team", t.Name)
		}
	}

	controllerutil.RemoveFinalizer(t, finalizerName)
	if err := r.Update(ctx, t); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("team deleted", "name", t.Name)
	return reconcile.Result{}, nil
}
