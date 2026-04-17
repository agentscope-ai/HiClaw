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
	hiclawwebhook "github.com/hiclaw/hiclaw-controller/internal/webhook"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreateWorkerForTeamLeaderForcesTeamContext(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validators := hiclawwebhook.NewValidators(k8sClient)
	handler := NewResourceHandler(k8sClient, "default", validators)

	// The caller is team-leader of alpha-team. Even though the request body
	// asks for a team_leader role in other-team, the handler must force the
	// new Worker into the caller's own team as a plain team_worker.
	body := []byte(`{"name":"alpha-temp","model":"qwen3.5-plus","teamRef":"other-team","role":"team_leader"}`)
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

	if got := worker.Spec.TeamRef; got != "alpha-team" {
		t.Fatalf("expected spec.teamRef alpha-team, got %q", got)
	}
	if got := worker.Spec.Role; got != v1beta1.WorkerRoleTeamWorker {
		t.Fatalf("expected spec.role team_worker, got %q", got)
	}
	// Annotation-based wiring is gone — the reconciler syncs labels from spec.
	if got := worker.Annotations["hiclaw.io/team"]; got != "" {
		t.Fatalf("expected no team annotation, got %q", got)
	}
}

func TestCreateAndUpdateTeamRuntimeConfig(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validators := hiclawwebhook.NewValidators(k8sClient)
	handler := NewResourceHandler(k8sClient, "default", validators)

	createBody := []byte(`{
		"name":"alpha-team",
		"description":"alpha",
		"heartbeat":{"enabled":true,"every":"30m"},
		"workerIdleTimeout":"12h"
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
	if created.Spec.Heartbeat == nil || !created.Spec.Heartbeat.Enabled || created.Spec.Heartbeat.Every != "30m" {
		t.Fatalf("unexpected heartbeat config after create: %#v", created.Spec.Heartbeat)
	}
	if created.Spec.WorkerIdleTimeout != "12h" {
		t.Fatalf("expected worker idle timeout 12h, got %q", created.Spec.WorkerIdleTimeout)
	}

	updateBody := []byte(`{
		"heartbeat":{"enabled":true,"every":"45m"},
		"workerIdleTimeout":"24h"
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
	if updated.Spec.Heartbeat == nil || updated.Spec.Heartbeat.Every != "45m" {
		t.Fatalf("unexpected heartbeat config after update: %#v", updated.Spec.Heartbeat)
	}
	if updated.Spec.WorkerIdleTimeout != "24h" {
		t.Fatalf("expected worker idle timeout 24h, got %q", updated.Spec.WorkerIdleTimeout)
	}

	var resp TeamResponse
	if err := json.Unmarshal(updateRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Heartbeat == nil || resp.Heartbeat.Every != "45m" {
		t.Fatalf("unexpected response heartbeat: %#v", resp.Heartbeat)
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
