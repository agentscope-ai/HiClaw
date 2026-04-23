package controller

import (
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

// TestWorkerMemberContext_StampsControllerAndRoleLabels verifies that a
// standalone Worker CR's derived MemberContext carries hiclaw.io/controller
// and hiclaw.io/role=standalone so the resulting Pod is symmetric with
// Team-managed members and filterable by controller instance.
func TestWorkerMemberContext_StampsControllerAndRoleLabels(t *testing.T) {
	r := &WorkerReconciler{ControllerName: "ctl-x"}
	w := &v1beta1.Worker{}
	w.Name = "solo"
	w.Namespace = "hiclaw"

	mctx := r.workerMemberContext(w)

	if got := mctx.PodLabels[v1beta1.LabelController]; got != "ctl-x" {
		t.Fatalf("expected controller label ctl-x, got %q (labels=%v)", got, mctx.PodLabels)
	}
	if got := mctx.PodLabels["hiclaw.io/role"]; got != RoleStandalone.String() {
		t.Fatalf("expected role %q, got %q", RoleStandalone.String(), got)
	}
	if _, ok := mctx.PodLabels["hiclaw.io/team"]; ok {
		t.Fatalf("standalone worker must not carry hiclaw.io/team, got %v", mctx.PodLabels)
	}
}
