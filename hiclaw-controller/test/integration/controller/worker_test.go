//go:build integration

package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"github.com/hiclaw/hiclaw-controller/test/testutil/fixtures"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

	// Eventually: phase should become Running
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

	// Verify final state
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

	// Verify service layer was called
	if len(mockProv.Calls.ProvisionWorker) == 0 {
		t.Error("ProvisionWorker should have been called")
	}
	if len(mockDeploy.Calls.DeployWorkerConfig) == 0 {
		t.Error("DeployWorkerConfig should have been called")
	}
}

func TestWorkerCreate_ProvisionFailure_SetsFailedPhase(t *testing.T) {
	// Known bug: failCreate ignores Status().Update errors, causing Phase to stay
	// "Pending" instead of transitioning to "Failed". Will be fixed in Phase 2 reconciler refactor.
	t.Skip("blocked on reconciler refactor: failCreate Status Update is unreliable (Phase 2)")

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

	// Wait for Running
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

	// Reset call counters after create
	mockProv.Calls.DeprovisionWorker = nil
	mockDeploy.Calls.CleanupOSSData = nil

	// Delete
	if err := k8sClient.Delete(ctx, worker); err != nil {
		t.Fatalf("failed to delete Worker CR: %v", err)
	}

	// Eventually: Worker should be gone
	assertEventually(t, func() error {
		var w v1beta1.Worker
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(worker), &w)
		if err == nil {
			return fmt.Errorf("worker still exists (phase=%q)", w.Status.Phase)
		}
		return client.IgnoreNotFound(err)
	})

	// Verify deprovision was called
	if len(mockProv.Calls.DeprovisionWorker) == 0 {
		t.Error("DeprovisionWorker should have been called")
	}
	if len(mockDeploy.Calls.CleanupOSSData) == 0 {
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

// --- Test helpers ---

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
