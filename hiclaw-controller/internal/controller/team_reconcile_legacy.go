package controller

import (
	"context"

	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileLegacy updates the legacy teams-registry.json entry for this
// team so that Manager agent skills depending on the old registry
// continue to function during the transition. Non-critical: Legacy being
// nil (incluster mode) or disabled short-circuits the phase; errors are
// logged but do not fail the reconcile.
//
// The entry is derived entirely from observations written into Team.status
// by prior phases — the Team CR spec is no longer a source of truth for
// leader / workers lists.
func (r *TeamReconciler) reconcileLegacy(ctx context.Context, s *teamScope) {
	if r.Legacy == nil || !r.Legacy.Enabled() {
		return
	}
	logger := log.FromContext(ctx)
	t := s.team

	workerNames := make([]string, 0, len(s.members))
	for _, m := range s.members {
		workerNames = append(workerNames, m.Name)
	}

	var leaderName string
	if s.leader != nil {
		leaderName = s.leader.Name
	}

	var admin *service.TeamAdminEntry
	if len(s.admins) > 0 {
		admin = &service.TeamAdminEntry{
			Name:         s.admins[0].Name,
			MatrixUserID: s.admins[0].MatrixUserID,
		}
	}

	entry := service.TeamRegistryEntry{
		Name:           t.Name,
		Leader:         leaderName,
		Workers:        workerNames,
		TeamRoomID:     t.Status.TeamRoomID,
		LeaderDMRoomID: t.Status.LeaderDMRoomID,
		Admin:          admin,
	}
	if err := r.Legacy.UpdateTeamsRegistry(entry); err != nil {
		logger.Error(err, "teams-registry update failed (non-fatal)", "team", t.Name)
	}
}
