package controller

import (
	"context"
	"encoding/json"
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/oss/ossfake"
	"github.com/hiclaw/hiclaw-controller/internal/service"
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

// registryEntry is the minimal subset of service.workersRegistry we need to
// inspect in tests — duplicated locally because the registry shape (and
// WorkerRegistryEntry fields we care about) are stable JSON contracts that
// Manager-side tooling also consumes. Keeping this in sync with the JSON
// tags in service.WorkerRegistryEntry is deliberate.
type registryEntry struct {
	MatrixUserID string   `json:"matrix_user_id"`
	RoomID       string   `json:"room_id"`
	Runtime      string   `json:"runtime"`
	Deployment   string   `json:"deployment"`
	Skills       []string `json:"skills"`
	Role         string   `json:"role"`
	TeamID       *string  `json:"team_id"`
	Image        *string  `json:"image"`
}

type registryFile struct {
	Version int                      `json:"version"`
	Workers map[string]registryEntry `json:"workers"`
}

func readRegistry(t *testing.T, fake *ossfake.Memory, managerName string) *registryFile {
	t.Helper()
	key := "agents/" + managerName + "/workers-registry.json"
	data, err := fake.GetObject(context.Background(), key)
	if err != nil {
		t.Fatalf("read registry %s: %v", key, err)
	}
	var out registryFile
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	return &out
}

func newTestLegacy(t *testing.T) (*service.LegacyCompat, *ossfake.Memory) {
	t.Helper()
	fake := ossfake.NewMemory()
	legacy := service.NewLegacyCompat(service.LegacyConfig{
		OSS:          fake,
		MatrixDomain: "matrix.local",
		ManagerName:  "manager",
		// Leave AgentFSDir empty so LegacyCompat skips the local shared-mount
		// write that would otherwise require creating a real directory.
		AgentFSDir: "",
	})
	return legacy, fake
}

// TestReconcileLegacyMember_BuildsEntry is the regression guard for the
// test-18 failure: TeamReconciler must populate workers-registry.json with
// role=team_leader / worker and team_id=<team name> for each team member so
// manager-side skills (find-worker.sh, push-worker-skills.sh, etc.) can
// continue to resolve team members by name.
func TestReconcileLegacyMember_BuildsEntry(t *testing.T) {
	legacy, fake := newTestLegacy(t)
	r := &TeamReconciler{Legacy: legacy}

	team := &v1beta1.Team{}
	team.Name = "team-a"
	team.Spec.Leader = v1beta1.LeaderSpec{Name: "lead"}

	leaderCtx := MemberContext{
		Name: "lead",
		Role: RoleTeamLeader,
		Spec: v1beta1.WorkerSpec{Runtime: "copaw"},
	}
	leaderStatus := &v1beta1.TeamMemberStatus{Name: "lead", RoomID: "!room-lead:matrix.local"}
	r.reconcileLegacyMember(context.Background(), team, leaderCtx, leaderStatus)

	workerCtx := MemberContext{
		Name: "dev",
		Role: RoleTeamWorker,
		Spec: v1beta1.WorkerSpec{
			Runtime: "copaw",
			Image:   "dev:v1",
			Skills:  []string{"refactor"},
		},
	}
	workerStatus := &v1beta1.TeamMemberStatus{Name: "dev", RoomID: "!room-dev:matrix.local"}
	r.reconcileLegacyMember(context.Background(), team, workerCtx, workerStatus)

	reg := readRegistry(t, fake, "manager")
	if reg.Version != 1 {
		t.Fatalf("registry version=%d, want 1", reg.Version)
	}

	leader, ok := reg.Workers["lead"]
	if !ok {
		t.Fatalf("leader entry missing from registry: %+v", reg.Workers)
	}
	if leader.Role != "team_leader" {
		t.Errorf("leader role=%q, want team_leader", leader.Role)
	}
	if leader.TeamID == nil || *leader.TeamID != "team-a" {
		t.Errorf("leader team_id=%v, want team-a", leader.TeamID)
	}
	if leader.Runtime != "copaw" {
		t.Errorf("leader runtime=%q, want copaw", leader.Runtime)
	}
	if leader.RoomID != "!room-lead:matrix.local" {
		t.Errorf("leader room_id=%q, want !room-lead:matrix.local", leader.RoomID)
	}
	if leader.MatrixUserID != "@lead:matrix.local" {
		t.Errorf("leader matrix_user_id=%q, want @lead:matrix.local", leader.MatrixUserID)
	}
	if leader.Deployment != "local" {
		t.Errorf("leader deployment=%q, want local", leader.Deployment)
	}
	if leader.Image != nil {
		t.Errorf("leader image=%v, want nil (leader spec has no image)", leader.Image)
	}

	worker, ok := reg.Workers["dev"]
	if !ok {
		t.Fatalf("worker entry missing from registry: %+v", reg.Workers)
	}
	if worker.Role != "worker" {
		t.Errorf("worker role=%q, want worker", worker.Role)
	}
	if worker.TeamID == nil || *worker.TeamID != "team-a" {
		t.Errorf("worker team_id=%v, want team-a", worker.TeamID)
	}
	if worker.Image == nil || *worker.Image != "dev:v1" {
		t.Errorf("worker image=%v, want dev:v1", worker.Image)
	}
	if len(worker.Skills) != 1 || worker.Skills[0] != "refactor" {
		t.Errorf("worker skills=%v, want [refactor]", worker.Skills)
	}
}

func TestReconcileLegacyMember_NoOpWhenLegacyNil(t *testing.T) {
	r := &TeamReconciler{Legacy: nil}
	team := &v1beta1.Team{}
	team.Name = "team-a"
	// Must not panic.
	r.reconcileLegacyMember(context.Background(), team, MemberContext{Name: "x", Role: RoleTeamLeader}, nil)
	r.removeLegacyMember(context.Background(), "x")
}

// TestRemoveLegacyMember_DeletesEntry covers the stale-cleanup and
// handleDelete paths: once removed, the entry disappears so manager-side
// skills no longer see a ghost worker.
func TestRemoveLegacyMember_DeletesEntry(t *testing.T) {
	legacy, fake := newTestLegacy(t)
	r := &TeamReconciler{Legacy: legacy}

	team := &v1beta1.Team{}
	team.Name = "team-a"
	team.Spec.Leader = v1beta1.LeaderSpec{Name: "lead"}
	r.reconcileLegacyMember(context.Background(), team,
		MemberContext{Name: "lead", Role: RoleTeamLeader, Spec: v1beta1.WorkerSpec{Runtime: "copaw"}},
		&v1beta1.TeamMemberStatus{Name: "lead"})

	if _, ok := readRegistry(t, fake, "manager").Workers["lead"]; !ok {
		t.Fatalf("precondition: lead should be present before removal")
	}

	r.removeLegacyMember(context.Background(), "lead")

	if _, ok := readRegistry(t, fake, "manager").Workers["lead"]; ok {
		t.Fatalf("lead still present after removeLegacyMember")
	}
}
