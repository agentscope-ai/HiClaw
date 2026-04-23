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

// TestWorkerMemberContext_MergesMetadataAndSpecLabels verifies the
// three-layer merge: CR metadata.labels, CR spec.labels, and the
// controller-forced system labels. spec.labels wins over metadata.labels
// on collision (per project decision — per-CR spec beats per-CR
// metadata) while non-conflicting entries from both layers survive.
func TestWorkerMemberContext_MergesMetadataAndSpecLabels(t *testing.T) {
	r := &WorkerReconciler{ControllerName: "ctl-x"}
	w := &v1beta1.Worker{}
	w.Name = "solo"
	w.Namespace = "hiclaw"
	w.ObjectMeta.Labels = map[string]string{
		"owner": "alice",
		"team":  "a",
	}
	w.Spec.Labels = map[string]string{
		"env":  "prod",
		"team": "b", // overrides metadata.labels["team"]
	}

	mctx := r.workerMemberContext(w)

	if got := mctx.PodLabels["owner"]; got != "alice" {
		t.Fatalf("metadata.labels[owner] not propagated: %v", mctx.PodLabels)
	}
	if got := mctx.PodLabels["env"]; got != "prod" {
		t.Fatalf("spec.labels[env] not propagated: %v", mctx.PodLabels)
	}
	if got := mctx.PodLabels["team"]; got != "b" {
		t.Fatalf("spec.labels must override metadata.labels on key collision, got team=%q", got)
	}
}

// TestWorkerMemberContext_SystemLabelsOverrideUser verifies reserved
// keys are silently overridden by controller system labels. Users
// cannot spoof hiclaw.io/controller or hiclaw.io/role by stuffing them
// into metadata.labels or spec.labels — this is the "reserved-override"
// contract.
func TestWorkerMemberContext_SystemLabelsOverrideUser(t *testing.T) {
	r := &WorkerReconciler{ControllerName: "real-ctl"}
	w := &v1beta1.Worker{}
	w.Name = "solo"
	w.ObjectMeta.Labels = map[string]string{
		v1beta1.LabelController: "metadata-attacker",
	}
	w.Spec.Labels = map[string]string{
		v1beta1.LabelController: "spec-attacker",
		"hiclaw.io/role":        "evil",
	}

	mctx := r.workerMemberContext(w)

	if got := mctx.PodLabels[v1beta1.LabelController]; got != "real-ctl" {
		t.Fatalf("system controller label must win over user, got %q (labels=%v)", got, mctx.PodLabels)
	}
	if got := mctx.PodLabels["hiclaw.io/role"]; got != RoleStandalone.String() {
		t.Fatalf("system role label must win over user, got %q", got)
	}
}

// TestWorkerMemberContext_NilLabelsSafe ensures the merge helper
// handles the common case of a Worker CR that has neither
// metadata.labels nor spec.labels without panicking or emitting stray
// empty-map entries.
func TestWorkerMemberContext_NilLabelsSafe(t *testing.T) {
	r := &WorkerReconciler{ControllerName: "ctl-x"}
	w := &v1beta1.Worker{}
	w.Name = "solo"

	mctx := r.workerMemberContext(w)

	if mctx.PodLabels[v1beta1.LabelController] != "ctl-x" {
		t.Fatalf("controller label missing on nil-labels Worker: %v", mctx.PodLabels)
	}
	if len(mctx.PodLabels) != 2 {
		t.Fatalf("expected exactly the 2 system labels on nil-labels Worker, got %v", mctx.PodLabels)
	}
}
