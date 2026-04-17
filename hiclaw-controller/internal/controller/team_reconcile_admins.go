package controller

import (
	"context"
	"fmt"
	"sort"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileAdmins lists all Humans with a teamAccess entry targeting this
// Team with role=admin and writes the result into both the scope (for
// downstream Room membership use) and Team.status.admins. The phase is
// best-effort — a transient Human list failure surfaces as a returned
// error so that the caller can requeue, but it does not mutate the
// previously observed admins set until a fresh list succeeds.
func (r *TeamReconciler) reconcileAdmins(ctx context.Context, s *teamScope) (reconcile.Result, error) {
	t := s.team
	logger := log.FromContext(ctx)

	obs, err := r.Observer.ListTeamAdmins(ctx, t.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("list team admins: %w", err)
	}

	sort.Slice(obs, func(i, j int) bool { return obs[i].Name < obs[j].Name })
	s.admins = obs

	if len(obs) == 0 {
		t.Status.Admins = nil
	} else {
		out := make([]v1beta1.TeamAdminObservation, 0, len(obs))
		for _, a := range obs {
			out = append(out, v1beta1.TeamAdminObservation{
				HumanName:    a.Name,
				MatrixUserID: a.MatrixUserID,
			})
		}
		t.Status.Admins = out
	}

	logger.V(1).Info("admins observed", "team", t.Name, "count", len(obs))
	return reconcile.Result{}, nil
}
