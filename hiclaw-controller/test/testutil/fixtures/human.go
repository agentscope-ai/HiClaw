package fixtures

import (
	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HumanOption composes Human CR fixtures via the functional-options
// pattern. All refactor-era access declarations (superAdmin / teamAccess
// / workerAccess) have dedicated modifiers here to discourage tests
// from handwriting the spec layout.
type HumanOption func(*v1beta1.Human)

// NewTestHuman creates a minimal Human CR with a default displayName of
// "Test <name>" for testing.
func NewTestHuman(name string, opts ...HumanOption) *v1beta1.Human {
	h := &v1beta1.Human{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: DefaultNamespace,
		},
		Spec: v1beta1.HumanSpec{
			DisplayName: "Test " + name,
		},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithDisplayName overrides the default displayName.
func WithDisplayName(displayName string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Spec.DisplayName = displayName
	}
}

// WithEmail sets spec.email.
func WithEmail(email string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Spec.Email = email
	}
}

// WithSuperAdmin sets spec.superAdmin=true. Webhook validation rejects
// this combined with teamAccess or workerAccess; tests that need the
// rejection path should still apply WithTeamAccess / WithWorkerAccess
// after this to construct the negative-case fixture.
func WithSuperAdmin() HumanOption {
	return func(h *v1beta1.Human) {
		h.Spec.SuperAdmin = true
	}
}

// WithTeamAccess appends a single teamAccess entry. Call multiple times
// to declare membership in multiple teams. Role must be one of
// v1beta1.TeamAccessRoleAdmin or v1beta1.TeamAccessRoleMember.
func WithTeamAccess(team, role string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Spec.TeamAccess = append(h.Spec.TeamAccess, v1beta1.TeamAccessEntry{
			Team: team,
			Role: role,
		})
	}
}

// WithWorkerAccess sets spec.workerAccess to the provided list. Later
// calls replace rather than append.
func WithWorkerAccess(workers ...string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Spec.WorkerAccess = workers
	}
}

// WithNote sets spec.note.
func WithNote(note string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Spec.Note = note
	}
}

// WithHumanStatus pre-seeds the status for reconciler-bypassing tests:
// a Human that is already "Active" with a known MatrixUserID can be
// treated as an admin observation by the Team reconciler without
// requiring HumanReconciler to run.
func WithHumanStatus(phase, matrixUserID string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Status.Phase = phase
		h.Status.MatrixUserID = matrixUserID
	}
}

// WithHumanRooms pre-seeds status.Rooms with a list of Matrix room IDs,
// useful for asserting leave/join diffs in HumanReconciler tests.
func WithHumanRooms(rooms ...string) HumanOption {
	return func(h *v1beta1.Human) {
		h.Status.Rooms = rooms
	}
}
