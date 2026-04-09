package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreateWorkerForTeamLeaderForcesTeamContext(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := NewResourceHandler(k8sClient, "default")

	body := []byte(`{"name":"alpha-temp","model":"qwen3.5-plus","team":"other-team","role":"team_leader","teamLeader":"wrong-lead"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workers", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), authpkg.CallerKeyForTest(), &authpkg.CallerIdentity{
		Role:     authpkg.RoleTeamLeader,
		Username: "alpha-lead",
		Team:     "alpha-team",
	}))
	rec := httptest.NewRecorder()

	handler.CreateWorker(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var worker v1beta1.Worker
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "alpha-temp", Namespace: "default"}, &worker); err != nil {
		t.Fatalf("get worker: %v", err)
	}

	if got := worker.Annotations["hiclaw.io/team"]; got != "alpha-team" {
		t.Fatalf("expected team annotation alpha-team, got %q", got)
	}
	if got := worker.Annotations["hiclaw.io/role"]; got != "worker" {
		t.Fatalf("expected role annotation worker, got %q", got)
	}
	if got := worker.Annotations["hiclaw.io/team-leader"]; got != "alpha-lead" {
		t.Fatalf("expected team leader annotation alpha-lead, got %q", got)
	}
}

func TestCreateAndUpdateTeamLeaderRuntimeConfig(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := NewResourceHandler(k8sClient, "default")

	createBody := []byte(`{
		"name":"alpha-team",
		"leader":{
			"name":"alpha-lead",
			"heartbeat":{"enabled":true,"every":"30m"},
			"workerIdleTimeout":"12h"
		},
		"workers":[]
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	handler.CreateTeam(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d: %s", http.StatusCreated, createRec.Code, createRec.Body.String())
	}

	var created v1beta1.Team
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "alpha-team", Namespace: "default"}, &created); err != nil {
		t.Fatalf("get created team: %v", err)
	}
	if created.Spec.Leader.Heartbeat == nil || !created.Spec.Leader.Heartbeat.Enabled || created.Spec.Leader.Heartbeat.Every != "30m" {
		t.Fatalf("unexpected heartbeat config after create: %#v", created.Spec.Leader.Heartbeat)
	}
	if created.Spec.Leader.WorkerIdleTimeout != "12h" {
		t.Fatalf("expected worker idle timeout 12h, got %q", created.Spec.Leader.WorkerIdleTimeout)
	}

	updateBody := []byte(`{
		"leader":{
			"heartbeat":{"enabled":true,"every":"45m"},
			"workerIdleTimeout":"24h"
		}
	}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/teams/alpha-team", bytes.NewReader(updateBody))
	updateReq.SetPathValue("name", "alpha-team")
	updateRec := httptest.NewRecorder()
	handler.UpdateTeam(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected update status %d, got %d: %s", http.StatusOK, updateRec.Code, updateRec.Body.String())
	}

	var updated v1beta1.Team
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "alpha-team", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("get updated team: %v", err)
	}
	if updated.Spec.Leader.Heartbeat == nil || updated.Spec.Leader.Heartbeat.Every != "45m" {
		t.Fatalf("unexpected heartbeat config after update: %#v", updated.Spec.Leader.Heartbeat)
	}
	if updated.Spec.Leader.WorkerIdleTimeout != "24h" {
		t.Fatalf("expected worker idle timeout 24h, got %q", updated.Spec.Leader.WorkerIdleTimeout)
	}

	var resp TeamResponse
	if err := json.Unmarshal(updateRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.LeaderHeartbeat == nil || resp.LeaderHeartbeat.Every != "45m" {
		t.Fatalf("unexpected response heartbeat: %#v", resp.LeaderHeartbeat)
	}
	if resp.WorkerIdleTimeout != "24h" {
		t.Fatalf("expected response worker idle timeout 24h, got %q", resp.WorkerIdleTimeout)
	}
}

func newServerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := v1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hiclaw scheme: %v", err)
	}
	return scheme
}
