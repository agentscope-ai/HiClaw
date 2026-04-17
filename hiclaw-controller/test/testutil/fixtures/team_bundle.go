package fixtures

import (
	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

// TeamBundle is the fully-expanded set of CRs produced by a single
// user-level "create a team" intent. In production the REST API bundle
// endpoint (Stage 10) accepts a TeamBundleRequest and fans it out into
// exactly these CRs; this fixture lets integration and reconciler tests
// construct the same topology by directly seeding the objects into a
// fake / envtest client.
//
// Shape:
//   - Team:    the coordination CR (thin spec; admins are observed, not declared)
//   - Leader:  the sole Worker with spec.role=team_leader + teamRef=team.name
//   - Workers: team_worker Workers with teamRef=team.name
//   - Admins:  Human CRs with teamAccess[{team: team.name, role: admin}]
//
// All slices may be empty; integration tests routinely construct a
// leader-less or admin-less bundle to drive the Pending / Degraded
// phases of TeamReconciler.
type TeamBundle struct {
	Team    *v1beta1.Team
	Leader  *v1beta1.Worker
	Workers []*v1beta1.Worker
	Admins  []*v1beta1.Human
}

// TeamBundleOption is a functional option for NewTeamBundle.
type TeamBundleOption func(*teamBundleBuilder)

type teamBundleBuilder struct {
	teamOpts []TeamOption
	leader   *leaderSpec
	workers  []workerSpec
	admins   []adminSpec
}

type leaderSpec struct {
	name    string
	options []WorkerOption
}

type workerSpec struct {
	name    string
	options []WorkerOption
}

type adminSpec struct {
	name    string
	options []HumanOption
}

// NewTeamBundle constructs a TeamBundle with a thin Team CR, a leader
// Worker, any number of team_worker Workers, and any number of admin
// Humans. The function wires spec.teamRef + spec.role correctly across
// the expanded CRs so they satisfy the webhook invariants.
func NewTeamBundle(name string, opts ...TeamBundleOption) *TeamBundle {
	b := &teamBundleBuilder{}
	for _, opt := range opts {
		opt(b)
	}

	bundle := &TeamBundle{
		Team: NewTestTeam(name, b.teamOpts...),
	}

	if b.leader != nil {
		leaderOpts := append([]WorkerOption{
			WithRole(v1beta1.WorkerRoleTeamLeader),
			WithTeamRef(name),
		}, b.leader.options...)
		bundle.Leader = NewTestWorker(b.leader.name, leaderOpts...)
	}

	for _, w := range b.workers {
		workerOpts := append([]WorkerOption{
			WithRole(v1beta1.WorkerRoleTeamWorker),
			WithTeamRef(name),
		}, w.options...)
		bundle.Workers = append(bundle.Workers, NewTestWorker(w.name, workerOpts...))
	}

	for _, a := range b.admins {
		adminOpts := append([]HumanOption{
			WithTeamAccess(name, v1beta1.TeamAccessRoleAdmin),
		}, a.options...)
		bundle.Admins = append(bundle.Admins, NewTestHuman(a.name, adminOpts...))
	}

	return bundle
}

// WithBundleTeamOptions forwards extra TeamOption modifiers to the
// embedded Team fixture.
func WithBundleTeamOptions(opts ...TeamOption) TeamBundleOption {
	return func(b *teamBundleBuilder) {
		b.teamOpts = append(b.teamOpts, opts...)
	}
}

// WithBundleLeader declares the leader Worker. Only the last call takes
// effect — a TeamBundle has exactly one leader (webhook-enforced at
// runtime; fixture-enforced here).
func WithBundleLeader(name string, opts ...WorkerOption) TeamBundleOption {
	return func(b *teamBundleBuilder) {
		b.leader = &leaderSpec{name: name, options: opts}
	}
}

// WithBundleWorker appends a team_worker Worker.
func WithBundleWorker(name string, opts ...WorkerOption) TeamBundleOption {
	return func(b *teamBundleBuilder) {
		b.workers = append(b.workers, workerSpec{name: name, options: opts})
	}
}

// WithBundleAdmin appends an admin Human.
func WithBundleAdmin(name string, opts ...HumanOption) TeamBundleOption {
	return func(b *teamBundleBuilder) {
		b.admins = append(b.admins, adminSpec{name: name, options: opts})
	}
}

// AllObjects returns every CR in the bundle as a single slice suitable
// for WithObjects(...) on the controller-runtime fake client builder.
func (tb *TeamBundle) AllObjects() []any {
	out := make([]any, 0, 1+1+len(tb.Workers)+len(tb.Admins))
	if tb.Team != nil {
		out = append(out, tb.Team)
	}
	if tb.Leader != nil {
		out = append(out, tb.Leader)
	}
	for _, w := range tb.Workers {
		out = append(out, w)
	}
	for _, a := range tb.Admins {
		out = append(out, a)
	}
	return out
}
