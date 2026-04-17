package controller

import (
	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// teamScope carries per-reconcile state through the TeamReconciler phases.
// It is allocated at the top of Reconcile and passed by pointer to each
// phase so that observations made in one phase (e.g. the leader Worker
// identified in reconcileMembers) are reusable without redundant list
// calls. Never cache across reconciliations.
type teamScope struct {
	team      *v1beta1.Team
	patchBase client.Patch

	// Populated by reconcileMembers.
	leader         *service.WorkerObservation   // nil when no leader / multiple leaders
	members        []service.WorkerObservation  // workers with role=team_worker
	multipleLeader bool                         // true when >1 team_leader observed

	// Populated by reconcileAdmins.
	admins []service.HumanObservation
}
