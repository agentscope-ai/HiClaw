package fixtures

import (
	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TeamOption composes Team CR test fixtures via the functional-options
// pattern, paralleling WorkerOption. Team.spec is intentionally thin in
// the refactor — most test variation happens at the Worker and Human
// level rather than here.
type TeamOption func(*v1beta1.Team)

// NewTestTeam creates a minimal Team CR for testing. The caller typically
// pairs this with separately-applied Worker + Human fixtures to construct
// a complete topology.
func NewTestTeam(name string, opts ...TeamOption) *v1beta1.Team {
	t := &v1beta1.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: DefaultNamespace,
		},
		Spec: v1beta1.TeamSpec{
			Description: "test team " + name,
		},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// WithTeamDescription overrides the default description.
func WithTeamDescription(desc string) TeamOption {
	return func(t *v1beta1.Team) {
		t.Spec.Description = desc
	}
}

// WithPeerMentions sets the peerMentions field. Default when unset is
// true (matches effectivePeerMentions behaviour in the controller).
func WithPeerMentions(enabled bool) TeamOption {
	return func(t *v1beta1.Team) {
		t.Spec.PeerMentions = &enabled
	}
}

// WithTeamChannelPolicy sets the team-wide channel policy defaults.
func WithTeamChannelPolicy(policy *v1beta1.ChannelPolicySpec) TeamOption {
	return func(t *v1beta1.Team) {
		t.Spec.ChannelPolicy = policy
	}
}

// WithHeartbeat configures the leader heartbeat. The every string must
// be a valid Go duration (e.g. "15m") to pass webhook validation.
func WithHeartbeat(enabled bool, every string) TeamOption {
	return func(t *v1beta1.Team) {
		t.Spec.Heartbeat = &v1beta1.TeamHeartbeatSpec{
			Enabled: enabled,
			Every:   every,
		}
	}
}

// WithWorkerIdleTimeout sets spec.workerIdleTimeout as a Go duration string.
func WithWorkerIdleTimeout(timeout string) TeamOption {
	return func(t *v1beta1.Team) {
		t.Spec.WorkerIdleTimeout = timeout
	}
}

// WithTeamStatus pre-seeds the observed status so tests that bypass the
// reconciler (pure unit tests operating on a loaded CR) can exercise
// code paths that read status.Leader / status.Members / status.Rooms.
func WithTeamStatus(phase, teamRoomID, leaderDMRoomID string) TeamOption {
	return func(t *v1beta1.Team) {
		t.Status.Phase = phase
		t.Status.TeamRoomID = teamRoomID
		t.Status.LeaderDMRoomID = leaderDMRoomID
	}
}

// WithTeamLeader seeds the observed leader into Team.status. Useful for
// reconciler tests that need to simulate an already-resolved team so
// rooms-phase short-circuits and leader-broadcast phase fires.
func WithTeamLeader(name, matrixUserID string, ready bool) TeamOption {
	return func(t *v1beta1.Team) {
		t.Status.Leader = &v1beta1.TeamLeaderObservation{
			Name:         name,
			MatrixUserID: matrixUserID,
			Ready:        ready,
		}
	}
}

// WithTeamMember seeds a single team_worker observation into
// Team.status.Members. Can be applied multiple times to append.
func WithTeamMember(name, matrixUserID string, ready bool) TeamOption {
	return func(t *v1beta1.Team) {
		t.Status.Members = append(t.Status.Members, v1beta1.TeamMemberObservation{
			Name:         name,
			Role:         v1beta1.WorkerRoleTeamWorker,
			MatrixUserID: matrixUserID,
			Ready:        ready,
		})
		t.Status.TotalMembers = len(t.Status.Members)
		if ready {
			t.Status.ReadyMembers++
		}
	}
}

// WithTeamAdmin seeds a single admin observation into Team.status.Admins.
func WithTeamAdmin(humanName, matrixUserID string) TeamOption {
	return func(t *v1beta1.Team) {
		t.Status.Admins = append(t.Status.Admins, v1beta1.TeamAdminObservation{
			HumanName:    humanName,
			MatrixUserID: matrixUserID,
		})
	}
}
