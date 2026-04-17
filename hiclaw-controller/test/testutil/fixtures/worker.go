package fixtures

import (
	"fmt"
	"math/rand"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultNamespace is used for test resources.
const DefaultNamespace = "default"

// WorkerOption is the functional-options pattern for composing Worker
// CRs in tests. Each option applies a focused tweak (role, team ref,
// skill list, etc.) so tests can stay compact and intention-revealing.
type WorkerOption func(*v1beta1.Worker)

// NewTestWorker creates a minimal Worker CR for testing. Optional modifiers
// can be composed to attach role / team / expose / state / skills etc.
func NewTestWorker(name string, opts ...WorkerOption) *v1beta1.Worker {
	w := &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: DefaultNamespace,
		},
		Spec: v1beta1.WorkerSpec{
			Model:   "gpt-4o",
			Runtime: "openclaw",
			Role:    v1beta1.WorkerRoleStandalone,
		},
	}
	for _, opt := range opts {
		opt(w)
	}
	// Mirror spec.role / spec.teamRef into labels so Team/Manager reconcilers
	// can list by MatchingLabels exactly as the controller's syncWorkerLabels
	// does at runtime. Tests that seed Workers directly into a fake client
	// get the same observable shape as production.
	if w.Labels == nil {
		w.Labels = map[string]string{}
	}
	w.Labels[v1beta1.LabelRole] = w.Spec.EffectiveRole()
	if w.Spec.TeamRef != "" {
		w.Labels[v1beta1.LabelTeam] = w.Spec.TeamRef
	} else {
		delete(w.Labels, v1beta1.LabelTeam)
	}
	return w
}

// NewTestWorkerWithPhase creates a Worker CR with a pre-set status phase.
func NewTestWorkerWithPhase(name, phase string, opts ...WorkerOption) *v1beta1.Worker {
	w := NewTestWorker(name, opts...)
	w.Status.Phase = phase
	return w
}

// NewTestWorkerWithAnnotations creates a Worker CR with annotations.
// Retained for compatibility with pre-refactor tests that used annotations
// to carry team context; new tests should prefer WithRole / WithTeamRef.
func NewTestWorkerWithAnnotations(name string, annotations map[string]string, opts ...WorkerOption) *v1beta1.Worker {
	w := NewTestWorker(name, opts...)
	w.Annotations = annotations
	return w
}

// WithRole sets spec.role and mirrors the label. For leaders and team
// workers, pair with WithTeamRef; for standalone this is the default.
func WithRole(role string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.Role = role
	}
}

// WithTeamRef sets spec.teamRef and mirrors the label. Callers are
// responsible for pairing this with WithRole("team_leader" or "team_worker");
// leaving teamRef set on a standalone Worker will fail webhook validation
// at runtime but may be useful for constructing negative-case fixtures.
func WithTeamRef(team string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.TeamRef = team
	}
}

// WithModel overrides the default model.
func WithModel(model string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.Model = model
	}
}

// WithRuntime overrides the default runtime.
func WithRuntime(runtime string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.Runtime = runtime
	}
}

// WithWorkerSkills sets spec.skills.
func WithWorkerSkills(skills ...string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.Skills = skills
	}
}

// WithMcpServers sets spec.mcpServers.
func WithMcpServers(servers ...string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.McpServers = servers
	}
}

// WithWorkerExpose sets spec.expose.
func WithWorkerExpose(ports ...v1beta1.ExposePort) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.Expose = ports
	}
}

// WithWorkerState sets spec.state (Running | Sleeping | Stopped).
func WithWorkerState(state string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Spec.State = &state
	}
}

// WithWorkerStatus pre-seeds status fields useful for reconcile-aware tests.
// A ready team member Worker typically has Phase="Running" and a non-empty
// MatrixUserID so the TeamReconciler's classifier marks it as Ready.
func WithWorkerStatus(phase, matrixUserID, roomID string) WorkerOption {
	return func(w *v1beta1.Worker) {
		w.Status.Phase = phase
		w.Status.MatrixUserID = matrixUserID
		w.Status.RoomID = roomID
	}
}

// UniqueName returns a unique test name with a random suffix.
func UniqueName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, randString(6))
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz0123456789")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
