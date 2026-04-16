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
// Manager Create tests
// ---------------------------------------------------------------------------

func TestManagerCreate_HappyPath(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-create")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q, want Running", m.Status.Phase)
		}
		return nil
	})

	var m v1beta1.Manager
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
		t.Fatalf("failed to get Manager: %v", err)
	}

	if m.Status.ObservedGeneration != m.Generation {
		t.Errorf("ObservedGeneration=%d, want %d", m.Status.ObservedGeneration, m.Generation)
	}
	if m.Status.MatrixUserID == "" {
		t.Error("MatrixUserID should be set after creation")
	}
	if m.Status.RoomID == "" {
		t.Error("RoomID should be set after creation")
	}
	provCount, _, _, _ := mockMgrProv.CallCounts()
	if provCount == 0 {
		t.Error("ProvisionManager should have been called")
	}
	_, deployConfigCount, _, _ := mockMgrDeploy.CallCounts()
	if deployConfigCount == 0 {
		t.Error("DeployManagerConfig should have been called")
	}
}

func TestManagerCreate_ProvisionFailure_SetsFailedPhase(t *testing.T) {
	resetManagerMocks()

	mockMgrProv.ProvisionManagerFn = func(_ context.Context, _ service.ManagerProvisionRequest) (*service.ManagerProvisionResult, error) {
		return nil, fmt.Errorf("simulated provision failure")
	}

	mgrName := fixtures.UniqueName("test-mgr-fail")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.Phase != "Failed" {
			return fmt.Errorf("phase=%q, want Failed", m.Status.Phase)
		}
		if m.Status.Message == "" {
			return fmt.Errorf("message should contain failure reason")
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Manager Delete tests
// ---------------------------------------------------------------------------

func TestManagerDelete_CleansUpAll(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-delete")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}

	waitForManagerRunning(t, mgr)

	mockMgrProv.ClearCalls()
	mockMgrDeploy.ClearCalls()

	if err := k8sClient.Delete(ctx, mgr); err != nil {
		t.Fatalf("failed to delete Manager CR: %v", err)
	}

	assertEventually(t, func() error {
		var m v1beta1.Manager
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m)
		if err == nil {
			return fmt.Errorf("manager still exists (phase=%q)", m.Status.Phase)
		}
		return client.IgnoreNotFound(err)
	})

	_, deprovCount, _, deactivateCount := mockMgrProv.CallCounts()
	if deactivateCount == 0 {
		t.Error("DeactivateMatrixUser should have been called")
	}
	if deprovCount == 0 {
		t.Error("DeprovisionManager should have been called")
	}
	_, _, _, cleanupCount := mockMgrDeploy.CallCounts()
	if cleanupCount == 0 {
		t.Error("CleanupOSSData should have been called")
	}
}

// ---------------------------------------------------------------------------
// Manager Finalizer test
// ---------------------------------------------------------------------------

func TestManagerFinalizer_AddedOnCreate(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-fin")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		for _, f := range m.Finalizers {
			if f == "hiclaw.io/cleanup" {
				return nil
			}
		}
		return fmt.Errorf("finalizer hiclaw.io/cleanup not found in %v", m.Finalizers)
	})
}

// ---------------------------------------------------------------------------
// Manager Update test
// ---------------------------------------------------------------------------

func TestManagerUpdate_SpecChange_RecreatesContainer(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-update")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	waitForManagerRunning(t, mgr)

	mockMgrBackend.Reset()
	mockMgrBackend.StatusFn = func(_ context.Context, _ string) (*backend.WorkerResult, error) {
		return &backend.WorkerResult{Status: backend.StatusRunning}, nil
	}

	updateManagerSpecField(t, mgr, func(m *v1beta1.Manager) {
		m.Spec.Model = "claude-sonnet-4-20250514"
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.ObservedGeneration != m.Generation {
			return fmt.Errorf("ObservedGeneration=%d, want %d", m.Status.ObservedGeneration, m.Generation)
		}
		return nil
	})

	creates, deletes, _, _, _ := mockMgrBackend.CallSnapshot()
	if len(deletes) == 0 {
		t.Error("backend.Delete should have been called to remove old container")
	}
	if len(creates) == 0 {
		t.Error("backend.Create should have been called to create new container")
	}
}

// ---------------------------------------------------------------------------
// Manager Idempotency test
// ---------------------------------------------------------------------------

func TestManagerCreate_Idempotent_NoDoubleProvision(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-idemp")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	waitForManagerRunning(t, mgr)

	provCountBefore, _, refreshCountBefore, _ := mockMgrProv.CallCounts()

	triggerManagerReconcile(t, mgr)

	assertEventually(t, func() error {
		_, _, refreshCount, _ := mockMgrProv.CallCounts()
		if refreshCount <= refreshCountBefore {
			return fmt.Errorf("RefreshManagerCredentials count=%d, want >%d",
				refreshCount, refreshCountBefore)
		}
		return nil
	})

	provCountAfter, _, _, _ := mockMgrProv.CallCounts()
	if provCountAfter != provCountBefore {
		t.Errorf("ProvisionManager called %d times, want %d (should not re-provision after Running)",
			provCountAfter, provCountBefore)
	}
}

// ---------------------------------------------------------------------------
// Manager Lifecycle state change tests
// ---------------------------------------------------------------------------

func TestManagerStateChange_StopAndResume(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-stop")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	waitForManagerRunning(t, mgr)

	// Running -> Stopped
	mockMgrBackend.ClearCalls()

	updateManagerSpecField(t, mgr, func(m *v1beta1.Manager) {
		m.Spec.State = ptrString("Stopped")
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.Phase != "Stopped" {
			return fmt.Errorf("phase=%q, want Stopped", m.Status.Phase)
		}
		return nil
	})

	_, deletes, _, stops, _ := mockMgrBackend.CallSnapshot()
	if len(stops)+len(deletes) == 0 {
		t.Error("backend.Stop or Delete should have been called when transitioning to Stopped")
	}

	// Stopped -> Running
	mockMgrBackend.ClearCalls()

	updateManagerSpecField(t, mgr, func(m *v1beta1.Manager) {
		m.Spec.State = nil
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q, want Running", m.Status.Phase)
		}
		return nil
	})

	creates, _, _, _, _ := mockMgrBackend.CallSnapshot()
	if len(creates) == 0 {
		t.Error("backend.Create should have been called when resuming from Stopped")
	}
}

// ---------------------------------------------------------------------------
// Manager Delete of failed manager
// ---------------------------------------------------------------------------

func TestManagerDelete_ProvisionFailed_StillCleans(t *testing.T) {
	resetManagerMocks()

	mockMgrProv.ProvisionManagerFn = func(_ context.Context, _ service.ManagerProvisionRequest) (*service.ManagerProvisionResult, error) {
		return nil, fmt.Errorf("simulated provision failure")
	}

	mgrName := fixtures.UniqueName("test-mgr-delfail")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.Phase != "Failed" {
			return fmt.Errorf("phase=%q, want Failed", m.Status.Phase)
		}
		return nil
	})

	mockMgrProv.ClearCalls()
	mockMgrDeploy.ClearCalls()

	if err := k8sClient.Delete(ctx, mgr); err != nil {
		t.Fatalf("failed to delete Manager CR: %v", err)
	}

	assertEventually(t, func() error {
		var m v1beta1.Manager
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m)
		if err == nil {
			return fmt.Errorf("manager still exists (phase=%q)", m.Status.Phase)
		}
		return client.IgnoreNotFound(err)
	})

	_, deprovCount, _, _ := mockMgrProv.CallCounts()
	if deprovCount == 0 {
		t.Error("DeprovisionManager should have been called even for a failed manager")
	}
	_, _, _, cleanupCount := mockMgrDeploy.CallCounts()
	if cleanupCount == 0 {
		t.Error("CleanupOSSData should have been called even for a failed manager")
	}
}

// ---------------------------------------------------------------------------
// Manager no infinite recreate loop
// ---------------------------------------------------------------------------

func TestManagerUpdate_NoInfiniteRecreate(t *testing.T) {
	resetManagerMocks()

	mgrName := fixtures.UniqueName("test-mgr-noloop")
	mgr := fixtures.NewTestManager(mgrName)

	if err := k8sClient.Create(ctx, mgr); err != nil {
		t.Fatalf("failed to create Manager CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, mgr)
	})

	waitForManagerRunning(t, mgr)

	mockMgrBackend.ClearCalls()

	updateManagerSpecField(t, mgr, func(m *v1beta1.Manager) {
		m.Spec.Model = "gpt-4o-mini"
	})

	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.ObservedGeneration != m.Generation {
			return fmt.Errorf("ObservedGeneration=%d, want %d", m.Status.ObservedGeneration, m.Generation)
		}
		return nil
	})

	time.Sleep(3 * time.Second)

	creates, _, _, _, _ := mockMgrBackend.CallSnapshot()
	if len(creates) == 0 {
		t.Error("expected at least 1 Create from spec update")
	}
	if len(creates) > 2 {
		t.Errorf("Create called %d times -- possible infinite recreate loop (want <=2)", len(creates))
	}
}

// ---------------------------------------------------------------------------
// Manager test helpers
// ---------------------------------------------------------------------------

func waitForManagerRunning(t *testing.T, mgr *v1beta1.Manager) {
	t.Helper()
	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Status.Phase != "Running" {
			return fmt.Errorf("phase=%q message=%q gen=%d obsGen=%d, want Running",
				m.Status.Phase, m.Status.Message, m.Generation, m.Status.ObservedGeneration)
		}
		return nil
	})
}

func updateManagerSpecField(t *testing.T, mgr *v1beta1.Manager, mutate func(m *v1beta1.Manager)) {
	t.Helper()
	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		mutate(&m)
		return k8sClient.Update(ctx, &m)
	})
}

func triggerManagerReconcile(t *testing.T, mgr *v1beta1.Manager) {
	t.Helper()
	assertEventually(t, func() error {
		var m v1beta1.Manager
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgr), &m); err != nil {
			return err
		}
		if m.Annotations == nil {
			m.Annotations = map[string]string{}
		}
		m.Annotations["hiclaw.io/reconcile-trigger"] = fmt.Sprintf("%d", time.Now().UnixNano())
		return k8sClient.Update(ctx, &m)
	})
}
