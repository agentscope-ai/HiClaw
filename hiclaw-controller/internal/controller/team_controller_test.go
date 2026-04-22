package controller

import (
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

func TestLeaderHeartbeatEvery(t *testing.T) {
	team := &v1beta1.Team{}
	if got := leaderHeartbeatEvery(team); got != "" {
		t.Fatalf("expected empty heartbeat interval, got %q", got)
	}

	team.Spec.Leader.Heartbeat = &v1beta1.TeamLeaderHeartbeatSpec{
		Enabled: true,
		Every:   "30m",
	}
	if got := leaderHeartbeatEvery(team); got != "30m" {
		t.Fatalf("expected heartbeat interval 30m, got %q", got)
	}
}

func TestBuildDesiredMembers_LeaderAndWorkers(t *testing.T) {
	team := &v1beta1.Team{}
	team.Name = "alpha"
	team.Spec.Leader = v1beta1.LeaderSpec{Name: "alpha-lead", Model: "gpt-4o"}
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{
		{Name: "alpha-dev", Model: "gpt-4o"},
		{Name: "alpha-qa", Model: "gpt-4o"},
	}
	team.Status.Members = []v1beta1.TeamMemberStatus{
		{Name: "alpha-lead", Role: RoleTeamLeader.String(), Observed: true},
		{Name: "alpha-dev", Role: RoleTeamWorker.String(), Observed: true},
	}

	members := buildDesiredMembers(team)
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}
	if members[0].Role != RoleTeamLeader || members[0].Name != "alpha-lead" {
		t.Fatalf("members[0]=%+v, want leader alpha-lead", members[0])
	}
	if !members[0].IsUpdate {
		t.Errorf("leader should be IsUpdate=true (observed in Status.Members)")
	}
	if !members[1].IsUpdate {
		t.Errorf("alpha-dev should be IsUpdate=true (observed in Status.Members)")
	}
	if members[2].IsUpdate {
		t.Errorf("alpha-qa should be IsUpdate=false (not observed in Status.Members)")
	}
	for _, m := range members {
		if m.PodLabels["hiclaw.io/team"] != "alpha" {
			t.Errorf("member %s missing hiclaw.io/team label: %v", m.Name, m.PodLabels)
		}
		if m.Spec.Runtime != "copaw" {
			t.Errorf("member %s runtime=%q, want copaw", m.Name, m.Spec.Runtime)
		}
	}
}

// TestBuildDesiredMembers_SpecChangedDetection locks in the per-member
// spec-change detection that prevents unnecessary container recreation. It
// covers three cases on the same reconcile:
//   - leader with a matching stored hash   → SpecChanged=false
//   - worker whose spec was mutated         → SpecChanged=true
//   - worker with no stored hash (brand new) → SpecChanged=false (initial
//       creation is driven by the backend.StatusNotFound branch, not by
//       SpecChanged — see memberSpecChanged doc for why)
//
// This is the regression guard for the bug where TeamReconciler tore down
// every pod on every reconcile because MemberContext.ObservedGeneration was
// always 0 for team members.
func TestBuildDesiredMembers_SpecChangedDetection(t *testing.T) {
	team := &v1beta1.Team{}
	team.Name = "alpha"
	team.Spec.Leader = v1beta1.LeaderSpec{Name: "alpha-lead", Model: "gpt-4o"}
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{
		{Name: "alpha-dev", Model: "gpt-4o"},
		{Name: "alpha-qa", Model: "gpt-4o"},
	}

	// Leader's stored hash matches current source spec → unchanged.
	leaderHash := hashMemberSourceSpec(team, RoleTeamLeader, "alpha-lead")

	// alpha-dev previously stored at model=gpt-3.5 → now hashed against
	// the current gpt-4o spec → should report changed.
	priorTeam := team.DeepCopy()
	priorTeam.Spec.Workers[0].Model = "gpt-3.5"
	devHashOld := hashMemberSourceSpec(priorTeam, RoleTeamWorker, "alpha-dev")

	team.Status.Members = []v1beta1.TeamMemberStatus{
		{Name: "alpha-lead", Role: RoleTeamLeader.String(), SpecHash: leaderHash},
		{Name: "alpha-dev", Role: RoleTeamWorker.String(), SpecHash: devHashOld},
	}

	members := buildDesiredMembers(team)
	byName := map[string]MemberContext{}
	for _, m := range members {
		byName[m.Name] = m
	}
	if byName["alpha-lead"].SpecChanged {
		t.Errorf("leader spec unchanged, want SpecChanged=false, got true")
	}
	if !byName["alpha-dev"].SpecChanged {
		t.Errorf("alpha-dev spec mutated (gpt-3.5→gpt-4o), want SpecChanged=true")
	}
	if byName["alpha-qa"].SpecChanged {
		t.Errorf("alpha-qa has no stored hash (brand new), want SpecChanged=false so initial Create via StatusNotFound is not preempted by a transient Delete")
	}
}

// TestHashMemberSourceSpec_IgnoresPeerChanges is the specific guard for the
// live-cluster bug: adding a worker rewrites every member's *derived*
// ChannelPolicy (peer mentions + admin injection), but the user-authored
// source spec is unchanged, so the hash must stay the same.
func TestHashMemberSourceSpec_IgnoresPeerChanges(t *testing.T) {
	base := &v1beta1.Team{}
	base.Name = "alpha"
	base.Spec.Leader = v1beta1.LeaderSpec{Name: "alpha-lead", Model: "gpt-4o"}
	base.Spec.Workers = []v1beta1.TeamWorkerSpec{
		{Name: "alpha-dev", Model: "gpt-4o"},
	}

	after := base.DeepCopy()
	after.Spec.Workers = append(after.Spec.Workers, v1beta1.TeamWorkerSpec{
		Name: "alpha-qa", Model: "gpt-4o",
	})
	after.Spec.Admin = &v1beta1.TeamAdminSpec{Name: "alice", MatrixUserID: "@alice:example.com"}

	if hashMemberSourceSpec(base, RoleTeamLeader, "alpha-lead") !=
		hashMemberSourceSpec(after, RoleTeamLeader, "alpha-lead") {
		t.Errorf("leader hash changed after adding worker+admin; expected stable (no user-authored change)")
	}
	if hashMemberSourceSpec(base, RoleTeamWorker, "alpha-dev") !=
		hashMemberSourceSpec(after, RoleTeamWorker, "alpha-dev") {
		t.Errorf("alpha-dev hash changed after adding peer+admin; expected stable")
	}

	// Sanity: a real source change DOES flip the hash.
	mutated := base.DeepCopy()
	mutated.Spec.Workers[0].Model = "gpt-3.5"
	if hashMemberSourceSpec(base, RoleTeamWorker, "alpha-dev") ==
		hashMemberSourceSpec(mutated, RoleTeamWorker, "alpha-dev") {
		t.Errorf("alpha-dev hash unchanged after model mutation; expected different")
	}
}
