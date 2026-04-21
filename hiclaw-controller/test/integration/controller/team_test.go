//go:build integration

package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"github.com/hiclaw/hiclaw-controller/test/testutil/fixtures"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// Team lifecycle — happy path
// ---------------------------------------------------------------------------

func TestTeamCreate_ProvisionsLeaderAndWorkers(t *testing.T) {
	resetMocks()

	name := fixtures.UniqueName("t-create")
	team := fixtures.NewTestTeam(name, name+"-lead", name+"-dev", name+"-qa")

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = deleteAndWait(t, team) })

	waitForTeamPhase(t, team, "Active")

	var got v1beta1.Team
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
		t.Fatalf("get team: %v", err)
	}

	if got.Status.TeamRoomID == "" {
		t.Error("TeamRoomID should be populated")
	}
	if got.Status.LeaderDMRoomID == "" {
		t.Error("LeaderDMRoomID should be populated")
	}
	if got.Status.TotalWorkers != 2 {
		t.Errorf("TotalWorkers=%d, want 2", got.Status.TotalWorkers)
	}
	if !got.Status.LeaderReady {
		t.Error("LeaderReady should be true after convergence")
	}
	if got.Status.ReadyWorkers != 2 {
		t.Errorf("ReadyWorkers=%d, want 2", got.Status.ReadyWorkers)
	}

	wantObserved := map[string]bool{
		name + "-lead": true,
		name + "-dev":  true,
		name + "-qa":   true,
	}
	for _, n := range got.Status.ObservedMembers {
		if !wantObserved[n] {
			t.Errorf("unexpected observed member %q", n)
		}
		delete(wantObserved, n)
	}
	if len(wantObserved) > 0 {
		t.Errorf("missing observed members: %v", wantObserved)
	}

	if len(mockProv.Calls.ProvisionTeamRooms) == 0 {
		t.Error("ProvisionTeamRooms should have been called")
	}
	if len(mockDeploy.Calls.EnsureTeamStorage) == 0 {
		t.Error("EnsureTeamStorage should have been called")
	}
	if len(mockDeploy.Calls.InjectCoordinationContext) == 0 {
		t.Error("InjectCoordinationContext should have been called for leader")
	}
	// 1 leader + 2 workers = 3 ProvisionWorker calls on the first convergence
	if got := len(mockProv.Calls.ProvisionWorker); got < 3 {
		t.Errorf("ProvisionWorker count=%d, want >=3 (leader + 2 workers)", got)
	}
}

// ---------------------------------------------------------------------------
// Team — stale member cleanup
// ---------------------------------------------------------------------------

func TestTeamUpdate_RemovesStaleWorker(t *testing.T) {
	resetMocks()

	name := fixtures.UniqueName("t-stale")
	team := fixtures.NewTestTeam(name, name+"-lead", name+"-w1", name+"-w2")

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = deleteAndWait(t, team) })

	waitForTeamPhase(t, team, "Active")

	mockProv.ClearCalls()
	mockDeploy.ClearCalls()
	mockBackend.ClearCalls()

	// Drop w2 from the spec.
	updateTeamSpec(t, team, func(tt *v1beta1.Team) {
		tt.Spec.Workers = []v1beta1.TeamWorkerSpec{
			{Name: name + "-w1", Model: "gpt-4o"},
		}
	})

	assertEventually(t, func() error {
		var got v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
			return err
		}
		if got.Status.TotalWorkers != 1 {
			return fmt.Errorf("TotalWorkers=%d, want 1", got.Status.TotalWorkers)
		}
		for _, n := range got.Status.ObservedMembers {
			if n == name+"-w2" {
				return fmt.Errorf("observed still contains %s", n)
			}
		}
		return nil
	})

	// Stale member should have been deprovisioned.
	found := false
	for _, req := range mockProv.Calls.DeprovisionWorker {
		if req.Name == name+"-w2" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DeprovisionWorker should have been called for stale %s-w2", name)
	}
}

// ---------------------------------------------------------------------------
// Team — deletion
// ---------------------------------------------------------------------------

func TestTeamDelete_CleansUpAllMembers(t *testing.T) {
	resetMocks()

	name := fixtures.UniqueName("t-delete")
	team := fixtures.NewTestTeam(name, name+"-lead", name+"-w1")

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}

	waitForTeamPhase(t, team, "Active")

	mockProv.ClearCalls()
	mockDeploy.ClearCalls()

	if err := k8sClient.Delete(ctx, team); err != nil {
		t.Fatalf("delete team: %v", err)
	}

	assertEventually(t, func() error {
		var got v1beta1.Team
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got)
		if err == nil {
			return fmt.Errorf("team still exists (phase=%q)", got.Status.Phase)
		}
		return client.IgnoreNotFound(err)
	})

	deprovisioned := make(map[string]bool)
	for _, req := range mockProv.Calls.DeprovisionWorker {
		deprovisioned[req.Name] = true
	}
	if !deprovisioned[name+"-lead"] {
		t.Errorf("DeprovisionWorker should have been called for leader %s-lead", name)
	}
	if !deprovisioned[name+"-w1"] {
		t.Errorf("DeprovisionWorker should have been called for worker %s-w1", name)
	}
	if len(mockDeploy.Calls.CleanupOSSData) < 2 {
		t.Errorf("CleanupOSSData count=%d, want >=2 (leader + worker)", len(mockDeploy.Calls.CleanupOSSData))
	}
}

// ---------------------------------------------------------------------------
// Team — provision failure is surfaced as Failed phase
// ---------------------------------------------------------------------------

func TestTeamCreate_ProvisionRoomsFailure_SetsFailed(t *testing.T) {
	resetMocks()

	mockProv.ProvisionTeamRoomsFn = func(_ context.Context, _ service.TeamRoomRequest) (*service.TeamRoomResult, error) {
		return nil, fmt.Errorf("simulated room failure")
	}

	name := fixtures.UniqueName("t-fail")
	team := fixtures.NewTestTeam(name, name+"-lead", name+"-w1")

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = deleteAndWait(t, team) })

	assertEventually(t, func() error {
		var got v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
			return err
		}
		if got.Status.Phase != "Failed" {
			return fmt.Errorf("phase=%q, want Failed", got.Status.Phase)
		}
		if got.Status.Message == "" {
			return fmt.Errorf("message should contain failure reason")
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Team — member-level provision failure marks team Degraded, not Failed
// ---------------------------------------------------------------------------

func TestTeamCreate_WorkerProvisionFailure_Degraded(t *testing.T) {
	resetMocks()

	name := fixtures.UniqueName("t-degrade")
	badWorker := name + "-bad"

	mockProv.ProvisionWorkerFn = func(_ context.Context, req service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error) {
		if req.Name == badWorker {
			return nil, fmt.Errorf("simulated worker failure")
		}
		return &service.WorkerProvisionResult{
			MatrixUserID:   "@" + req.Name + ":localhost",
			MatrixToken:    "mock-token-" + req.Name,
			RoomID:         "!room-" + req.Name + ":localhost",
			GatewayKey:     "mock-gw-key-" + req.Name,
			MinIOPassword:  "mock-minio-pw",
			MatrixPassword: "mock-matrix-pw",
		}, nil
	}

	team := fixtures.NewTestTeam(name, name+"-lead", name+"-ok", badWorker)

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = deleteAndWait(t, team) })

	assertEventually(t, func() error {
		var got v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
			return err
		}
		if got.Status.Phase != "Degraded" {
			return fmt.Errorf("phase=%q, want Degraded", got.Status.Phase)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Team — backend readiness dictates Active vs Pending
// ---------------------------------------------------------------------------

func TestTeamCreate_PartialReadiness_RemainsPending(t *testing.T) {
	resetMocks()

	name := fixtures.UniqueName("t-partial")
	leaderName := name + "-lead"

	// Leader reports Running; worker reports Starting (pod exists but not ready).
	// Using Starting avoids triggering recreate loops in the reconciler, which
	// would happen if we returned StatusNotFound.
	mockBackend.StatusFn = func(_ context.Context, workerName string) (*backend.WorkerResult, error) {
		if workerName == leaderName {
			return &backend.WorkerResult{Status: backend.StatusRunning}, nil
		}
		return &backend.WorkerResult{Status: backend.StatusStarting}, nil
	}

	team := fixtures.NewTestTeam(name, leaderName, name+"-w1")

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = deleteAndWait(t, team) })

	assertEventually(t, func() error {
		var got v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
			return err
		}
		if got.Status.Phase == "Active" {
			return fmt.Errorf("team reached Active too early")
		}
		if !got.Status.LeaderReady {
			return fmt.Errorf("LeaderReady should be true")
		}
		if got.Status.ReadyWorkers != 0 {
			return fmt.Errorf("ReadyWorkers=%d, want 0 (worker still Starting)", got.Status.ReadyWorkers)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Team — finalizer is added on first reconcile
// ---------------------------------------------------------------------------

func TestTeamFinalizer_AddedOnCreate(t *testing.T) {
	resetMocks()

	name := fixtures.UniqueName("t-final")
	team := fixtures.NewTestTeam(name, name+"-lead", name+"-w1")

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = deleteAndWait(t, team) })

	assertEventually(t, func() error {
		var got v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
			return err
		}
		for _, f := range got.Finalizers {
			if f == "hiclaw.io/cleanup" {
				return nil
			}
		}
		return fmt.Errorf("finalizer not found in %v", got.Finalizers)
	})
}

// ---------------------------------------------------------------------------
// Team — helpers
// ---------------------------------------------------------------------------

func waitForTeamPhase(t *testing.T, team *v1beta1.Team, phase string) {
	t.Helper()
	assertEventually(t, func() error {
		var got v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got); err != nil {
			return err
		}
		if got.Status.Phase != phase {
			return fmt.Errorf("phase=%q want %q (leaderReady=%v ready=%d total=%d msg=%q)",
				got.Status.Phase, phase, got.Status.LeaderReady,
				got.Status.ReadyWorkers, got.Status.TotalWorkers, got.Status.Message)
		}
		return nil
	})
}

func updateTeamSpec(t *testing.T, team *v1beta1.Team, mutate func(*v1beta1.Team)) {
	t.Helper()
	assertEventually(t, func() error {
		var cur v1beta1.Team
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &cur); err != nil {
			return err
		}
		mutate(&cur)
		return k8sClient.Update(ctx, &cur)
	})
}

func deleteAndWait(t *testing.T, team *v1beta1.Team) error {
	if err := k8sClient.Delete(ctx, team); err != nil {
		return client.IgnoreNotFound(err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var got v1beta1.Team
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(team), &got)
		if err != nil {
			return client.IgnoreNotFound(err)
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("team %s not deleted within timeout", team.Name)
}
