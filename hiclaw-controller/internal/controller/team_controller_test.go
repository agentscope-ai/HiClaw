package controller

import (
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestSummarizeTeamWorkerReadiness(t *testing.T) {
	workers := []v1beta1.Worker{
		{ObjectMeta: newWorkerObjectMeta("alpha-lead"), Status: v1beta1.WorkerStatus{Phase: "Ready"}},
		{ObjectMeta: newWorkerObjectMeta("alpha-dev"), Status: v1beta1.WorkerStatus{Phase: "Running"}},
		{ObjectMeta: newWorkerObjectMeta("alpha-qa"), Status: v1beta1.WorkerStatus{Phase: "Sleeping"}},
	}

	readyWorkers, leaderReady := summarizeTeamWorkerReadiness(workers, "alpha-lead")
	if !leaderReady {
		t.Fatal("expected leader to be ready")
	}
	if readyWorkers != 1 {
		t.Fatalf("expected 1 ready worker, got %d", readyWorkers)
	}
}

func newWorkerObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}
