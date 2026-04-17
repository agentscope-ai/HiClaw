package controller

import (
	"context"
	"fmt"
	"sort"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileManagerAllowFrom authoritatively computes the Matrix IDs that
// should appear in the Manager's groupAllowFrom beyond the default
// Manager + Admin entries. The computation replaces the pre-refactor
// model where each Worker pushed itself into the Manager's allow list
// through a legacy OSS mutation; now the Manager reconciler pulls the
// truth from observable CRs on every reconcile.
//
// Inclusion rules:
//
//   - Workers with role ∈ {standalone, team_leader} whose
//     status.MatrixUserID is populated. team_worker Workers are excluded
//     because they communicate upstream only through the team leader,
//     not directly with the Manager.
//   - Humans with spec.superAdmin=true whose status.MatrixUserID is
//     populated. Team-scoped (non-superAdmin) Humans are handled per
//     Worker via the Worker reconciler and never talk to the Manager
//     directly.
//
// Matrix IDs are sorted for deterministic output so repeated reconciles
// produce stable config content.
func (r *ManagerReconciler) reconcileManagerAllowFrom(ctx context.Context, s *managerScope) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	m := s.manager

	set := make(map[string]bool)

	// Workers that communicate directly with the Manager.
	var workers v1beta1.WorkerList
	if err := r.List(ctx, &workers, client.InNamespace(m.Namespace)); err != nil {
		return reconcile.Result{}, fmt.Errorf("list workers for allowFrom: %w", err)
	}
	for i := range workers.Items {
		w := &workers.Items[i]
		role := w.Spec.EffectiveRole()
		if role != v1beta1.WorkerRoleStandalone && role != v1beta1.WorkerRoleTeamLeader {
			continue
		}
		if w.Status.MatrixUserID == "" {
			continue
		}
		set[w.Status.MatrixUserID] = true
	}

	// Humans with global access.
	var humans v1beta1.HumanList
	if err := r.List(ctx, &humans, client.InNamespace(m.Namespace)); err != nil {
		return reconcile.Result{}, fmt.Errorf("list humans for allowFrom: %w", err)
	}
	for i := range humans.Items {
		h := &humans.Items[i]
		if !h.Spec.SuperAdmin {
			continue
		}
		if h.Status.MatrixUserID == "" {
			continue
		}
		set[h.Status.MatrixUserID] = true
	}

	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	s.effectiveAllowFrom = out

	logger.V(1).Info("manager allowFrom computed",
		"manager", m.Name,
		"workersMatched", len(set),
		"total", len(out))
	return reconcile.Result{}, nil
}
