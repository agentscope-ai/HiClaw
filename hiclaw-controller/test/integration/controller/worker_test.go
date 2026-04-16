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
// Existing tests (Phase 1-3)
// ---------------------------------------------------------------------------

func TestWorkerCreate_HappyPath(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-create")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q, want Running", w.Status.Phase)
		}
		return nil
	})

	var w v1beta1.Worker
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
		t.Fatalf("failed to get Worker: %v", err)
	}

	if w.Status.ObservedGeneration != w.Generation {
		t.Errorf("ObservedGeneration=%d, want %d", w.Status.ObservedGeneration, w.Generation)
	}
	if w.Status.MatrixUserID == "" {
		t.Error("MatrixUserID should be set after creation")
	}
	if w.Status.RoomID == "" {
		t.Error("RoomID should be set after creation")
	}
	provCount, _, _, _ := mockProv.CallCounts()
	if provCount == 0 {
		t.Error("ProvisionWorker should have been called")
	}
	_, _, deployConfigCount, _, _ := mockDeploy.CallCounts()
	if deployConfigCount == 0 {
		t.Error("DeployWorkerConfig should have been called")
	}
}

func TestWorkerCreate_ProvisionFailure_SetsFailedPhase(t *testing.T) {
	resetMocks()

	mockProv.ProvisionWorkerFn = func(_ context.Context, _ service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error) {
		return nil, fmt.Errorf("simulated provision failure")
	}

	workerName := fixtures.UniqueName("test-fail")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Failed" {
			return fmt.Errorf("phase=%q, want Failed", w.Status.Phase)
		}
		if w.Status.Message == "" {
			return fmt.Errorf("message should contain failure reason")
		}
		return nil
	})
}

func TestWorkerDelete_CleansUpAll(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-delete")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}

	waitForRunning(t, worker)

	mockProv.ClearCalls()
	mockDeploy.ClearCalls()

	if err := k8sClient.Delete(ctx, worker); err != nil {
		t.Fatalf("failed to delete Worker CR: %v", err)
	}

	assertEventually(t, func() error {
		var w v1beta1.Worker
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w)
		if err == nil {
			return fmt.Errorf("worker still exists (phase=%q)", w.Status.Phase)
		}
		return client.IgnoreNotFound(err)
	})

	_, deprovCount, _, deactivateCount := mockProv.CallCounts()
	if deactivateCount == 0 {
		t.Error("DeactivateMatrixUser should have been called")
	}
	if deprovCount == 0 {
		t.Error("DeprovisionWorker should have been called")
	}
	_, _, _, _, cleanupCount := mockDeploy.CallCounts()
	if cleanupCount == 0 {
		t.Error("CleanupOSSData should have been called")
	}
}

func TestWorkerFinalizer_AddedOnCreate(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-finalizer")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		for _, f := range w.Finalizers {
			if f == "hiclaw.io/cleanup" {
				return nil
			}
		}
		return fmt.Errorf("finalizer hiclaw.io/cleanup not found in %v", w.Finalizers)
	})
}

func TestWorkerUpdate_SpecChange_RecreatesPod(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-update")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	// Reset fully: clear state so we can use StatusFn to simulate pre-existing pod.
	// This test predates stateful mock; keep using StatusFn for explicit control.
	mockBackend.Reset()
	mockBackend.StatusFn = func(_ context.Context, _ string) (*backend.WorkerResult, error) {
		return &backend.WorkerResult{Status: backend.StatusRunning}, nil
	}

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.Model = "claude-sonnet-4-20250514"
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.ObservedGeneration != w.Generation {
			return fmt.Errorf("ObservedGeneration=%d, want %d", w.Status.ObservedGeneration, w.Generation)
		}
		return nil
	})

	creates, deletes, _, _, _ := mockBackend.CallSnapshot()
	if len(deletes) == 0 {
		t.Error("backend.Delete should have been called to remove old container")
	}
	if len(creates) == 0 {
		t.Error("backend.Create should have been called to create new container")
	}
}

// ---------------------------------------------------------------------------
// Phase 4: Lifecycle state change tests
// ---------------------------------------------------------------------------

func TestWorkerStateChange_StopAndResume(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-stop-resume")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	// --- Running → Stopped ---
	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.State = ptrString("Stopped")
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Stopped" {
			return fmt.Errorf("phase=%q, want Stopped", w.Status.Phase)
		}
		return nil
	})

	_, deletes, _, stops, _ := mockBackend.CallSnapshot()
	if len(stops)+len(deletes) == 0 {
		t.Error("backend.Stop or Delete should have been called when transitioning to Stopped")
	}

	// --- Stopped → Running ---
	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.State = nil
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q, want Running", w.Status.Phase)
		}
		return nil
	})

	creates, _, _, _, _ := mockBackend.CallSnapshot()
	if len(creates) == 0 {
		t.Error("backend.Create should have been called when resuming from Stopped")
	}
}

func TestWorkerStateChange_SleepAndWake(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-sleep-wake")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	// --- Running → Sleeping ---
	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.State = ptrString("Sleeping")
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Sleeping" {
			return fmt.Errorf("phase=%q, want Sleeping", w.Status.Phase)
		}
		return nil
	})

	_, _, _, stops, _ := mockBackend.CallSnapshot()
	if len(stops) == 0 {
		t.Error("backend.Stop should have been called when transitioning to Sleeping")
	}

	// --- Sleeping → Running ---
	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.State = nil
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q, want Running", w.Status.Phase)
		}
		return nil
	})

	creates, _, _, _, _ := mockBackend.CallSnapshot()
	if len(creates) == 0 {
		t.Error("backend.Create should have been called when waking from Sleeping")
	}
}

// ---------------------------------------------------------------------------
// Phase 4: Pod resilience tests
// ---------------------------------------------------------------------------

func TestWorkerPodDeleted_Recreates(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-pod-deleted")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	// Simulate external pod deletion
	mockBackend.SimulatePodDeletion(workerName)
	mockBackend.ClearCalls()

	triggerReconcile(t, worker)

	assertEventually(t, func() error {
		creates, _, _, _, _ := mockBackend.CallSnapshot()
		if len(creates) == 0 {
			return fmt.Errorf("waiting for backend.Create to be called (pod recreation)")
		}
		return nil
	})

	// Phase should still be Running after recreation
	var w v1beta1.Worker
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
		t.Fatalf("failed to get Worker: %v", err)
	}
	if w.Status.Phase != "Running" {
		t.Errorf("phase=%q after pod recreation, want Running", w.Status.Phase)
	}
}

// ---------------------------------------------------------------------------
// Phase 4: Idempotency and convergence tests
// ---------------------------------------------------------------------------

func TestWorkerCreate_Idempotent_NoDoubleProvision(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-idempotent")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	provCountBefore, _, refreshCountBefore, _ := mockProv.CallCounts()

	// Trigger another reconcile via annotation update (no spec change)
	triggerReconcile(t, worker)

	// Wait for the new reconcile to complete — RefreshCredentials count increases
	assertEventually(t, func() error {
		_, _, refreshCount, _ := mockProv.CallCounts()
		if refreshCount <= refreshCountBefore {
			return fmt.Errorf("RefreshCredentials count=%d, want >%d (reconcile not triggered yet)",
				refreshCount, refreshCountBefore)
		}
		return nil
	})

	provCountAfter, _, _, _ := mockProv.CallCounts()
	if provCountAfter != provCountBefore {
		t.Errorf("ProvisionWorker called %d times, want %d (should not re-provision after Running)",
			provCountAfter, provCountBefore)
	}
}

func TestWorkerUpdate_NoInfiniteRecreate(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-no-loop")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.Model = "gpt-4o-mini"
	})

	// Wait for reconcile to complete
	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.ObservedGeneration != w.Generation {
			return fmt.Errorf("ObservedGeneration=%d, want %d", w.Status.ObservedGeneration, w.Generation)
		}
		return nil
	})

	// Wait a grace period — if there's an infinite loop, Creates will keep growing
	time.Sleep(3 * time.Second)

	creates, _, _, _, _ := mockBackend.CallSnapshot()
	// Expect exactly 1 Create from the update (delete old + create new).
	// With stateful mock, after the recreate cycle Status returns Running,
	// Generation == ObservedGeneration, so no further Creates should occur.
	if len(creates) == 0 {
		t.Error("expected at least 1 Create from spec update")
	}
	if len(creates) > 2 {
		t.Errorf("Create called %d times — possible infinite recreate loop (want <=2)", len(creates))
	}
}

// ---------------------------------------------------------------------------
// Phase 4: Simultaneous state + spec change
// ---------------------------------------------------------------------------

func TestWorkerStateChange_SimultaneousSpecAndState(t *testing.T) {
	resetMocks()

	workerName := fixtures.UniqueName("test-simul")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, worker)
	})

	waitForRunning(t, worker)

	// --- Simultaneously change state to Stopped AND model ---
	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.State = ptrString("Stopped")
		w.Spec.Model = "claude-sonnet-4-20250514"
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Stopped" {
			return fmt.Errorf("phase=%q, want Stopped", w.Status.Phase)
		}
		return nil
	})

	// While Stopped, no new pod should be created despite model change
	creates, _, _, _, _ := mockBackend.CallSnapshot()
	if len(creates) > 0 {
		t.Errorf("backend.Create called %d times while Stopped — should not create pod in Stopped state", len(creates))
	}

	// --- Resume to Running with new config ---
	mockBackend.ClearCalls()

	updateSpecField(t, worker, func(w *v1beta1.Worker) {
		w.Spec.State = nil
	})

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q, want Running", w.Status.Phase)
		}
		return nil
	})

	creates, _, _, _, _ = mockBackend.CallSnapshot()
	if len(creates) == 0 {
		t.Error("backend.Create should have been called when resuming with new config")
	}
}

// ---------------------------------------------------------------------------
// Phase 4: Deletion of a failed worker
// ---------------------------------------------------------------------------

func TestWorkerDelete_ProvisionFailed_StillCleans(t *testing.T) {
	resetMocks()

	mockProv.ProvisionWorkerFn = func(_ context.Context, _ service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error) {
		return nil, fmt.Errorf("simulated provision failure")
	}

	workerName := fixtures.UniqueName("test-del-fail")
	worker := fixtures.NewTestWorker(workerName)

	if err := k8sClient.Create(ctx, worker); err != nil {
		t.Fatalf("failed to create Worker CR: %v", err)
	}

	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Failed" {
			return fmt.Errorf("phase=%q, want Failed", w.Status.Phase)
		}
		return nil
	})

	mockProv.ClearCalls()
	mockDeploy.ClearCalls()

	if err := k8sClient.Delete(ctx, worker); err != nil {
		t.Fatalf("failed to delete Worker CR: %v", err)
	}

	assertEventually(t, func() error {
		var w v1beta1.Worker
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w)
		if err == nil {
			return fmt.Errorf("worker still exists (phase=%q)", w.Status.Phase)
		}
		return client.IgnoreNotFound(err)
	})

	_, deprovCount2, _, _ := mockProv.CallCounts()
	if deprovCount2 == 0 {
		t.Error("DeprovisionWorker should have been called even for a failed worker")
	}
	_, _, _, _, cleanupCount2 := mockDeploy.CallCounts()
	if cleanupCount2 == 0 {
		t.Error("CleanupOSSData should have been called even for a failed worker")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func ptrString(s string) *string {
	return &s
}

// clearAllCalls resets call records on all mocks without clearing Fn overrides or state.
func clearAllCalls() {
	mockProv.ClearCalls()
	mockDeploy.ClearCalls()
	mockBackend.ClearCalls()
	mockEnv.ClearCalls()
}

// waitForRunning blocks until the Worker reaches Phase "Running".
func waitForRunning(t *testing.T, worker *v1beta1.Worker) {
	t.Helper()
	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q message=%q gen=%d obsGen=%d, want Running",
				w.Status.Phase, w.Status.Message, w.Generation, w.Status.ObservedGeneration)
		}
		return nil
	})
}

// updateSpecField performs a read-modify-write on the Worker spec using the
// given mutator function. Retries on conflict (stale resourceVersion).
func updateSpecField(t *testing.T, worker *v1beta1.Worker, mutate func(w *v1beta1.Worker)) {
	t.Helper()
	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		mutate(&w)
		return k8sClient.Update(ctx, &w)
	})
}

// triggerReconcile forces a reconcile by adding/updating an annotation.
// This does NOT increment Generation (metadata-only change).
func triggerReconcile(t *testing.T, worker *v1beta1.Worker) {
	t.Helper()
	assertEventually(t, func() error {
		var w v1beta1.Worker
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w); err != nil {
			return err
		}
		if w.Annotations == nil {
			w.Annotations = map[string]string{}
		}
		w.Annotations["hiclaw.io/reconcile-trigger"] = fmt.Sprintf("%d", time.Now().UnixNano())
		return k8sClient.Update(ctx, &w)
	})
}

// assertEventually polls condFn until it returns nil or the timeout expires.
func assertEventually(t *testing.T, condFn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = condFn()
		if lastErr == nil {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("condition not met within %v: %v", timeout, lastErr)
}
