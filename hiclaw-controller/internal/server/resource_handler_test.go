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

// Post-refactor contract: team leaders cannot create team members via
// /api/v1/workers. They must use /api/v1/teams. The handler must return 409.
func TestCreateWorkerRejectsTeamLeaderCaller(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	body := []byte(`{"name":"alpha-temp","model":"qwen3.5-plus"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workers", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), authpkg.CallerKeyForTest(), &authpkg.CallerIdentity{
		Role:     authpkg.RoleTeamLeader,
		Username: "alpha-lead",
		Team:     "alpha-team",
	}))
	rec := httptest.NewRecorder()

	handler.CreateWorker(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
}

// When the worker name is a member of an existing Team, CreateWorker must
// return 409 regardless of caller role.
func TestCreateWorkerRejectsExistingTeamMemberName(t *testing.T) {
	scheme := newServerTestScheme(t)
	team := &v1beta1.Team{}
	team.Name = "alpha-team"
	team.Namespace = "default"
	team.Spec.Leader.Name = "alpha-lead"
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{{Name: "alpha-dev"}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	body := []byte(`{"name":"alpha-dev","model":"qwen3.5-plus"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.CreateWorker(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
}

// /api/v1/workers/{name} must synthesize a response for a team member even
// though no Worker CR exists. The synthesized response MUST carry the
// RoomID + MatrixUserID recorded in Team.Status.Members so that clients like
// the Manager Agent and `hiclaw get workers <name> -o json | jq .roomID`
// (exercised by test-21-team-project-dag) can resolve a member's room.
//
// This is the regression guard for the PR #666 bug where teamMemberToResponse
// synthesized an empty RoomID because Team.Status had no per-member RoomID
// field.
func TestGetWorkerSynthesizesTeamMember(t *testing.T) {
	scheme := newServerTestScheme(t)
	team := &v1beta1.Team{}
	team.Name = "alpha-team"
	team.Namespace = "default"
	team.Spec.Leader = v1beta1.LeaderSpec{Name: "alpha-lead", Model: "qwen3.5-plus"}
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{{Name: "alpha-dev", Model: "qwen3.5-plus"}}
	team.Status.Members = []v1beta1.TeamMemberStatus{
		{
			Name:         "alpha-dev",
			Role:         "worker",
			RoomID:       "!dev-room:example.com",
			MatrixUserID: "@alpha-dev:example.com",
			Observed:     true,
		},
		{
			Name:         "alpha-lead",
			Role:         "team_leader",
			RoomID:       "!lead-room:example.com",
			MatrixUserID: "@alpha-lead:example.com",
			Observed:     true,
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/alpha-dev", nil)
	req.SetPathValue("name", "alpha-dev")
	rec := httptest.NewRecorder()
	handler.GetWorker(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var resp WorkerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Team != "alpha-team" || resp.Name != "alpha-dev" || resp.Role != "worker" {
		t.Fatalf("unexpected synthesized response: %+v", resp)
	}
	if resp.RoomID != "!dev-room:example.com" {
		t.Errorf("RoomID=%q, want %q (not propagated from Team.Status.Members)", resp.RoomID, "!dev-room:example.com")
	}
	if resp.MatrixUserID != "@alpha-dev:example.com" {
		t.Errorf("MatrixUserID=%q, want %q", resp.MatrixUserID, "@alpha-dev:example.com")
	}
}

// /api/v1/workers must list standalone workers and synthetic team members.
// Workers with team annotations (legacy CRs) must NOT be duplicated.
func TestListWorkersAggregatesTeamMembers(t *testing.T) {
	scheme := newServerTestScheme(t)

	standalone := &v1beta1.Worker{}
	standalone.Name = "solo"
	standalone.Namespace = "default"

	team := &v1beta1.Team{}
	team.Name = "alpha-team"
	team.Namespace = "default"
	team.Spec.Leader = v1beta1.LeaderSpec{Name: "alpha-lead", Model: "qwen3.5-plus"}
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{{Name: "alpha-dev", Model: "qwen3.5-plus"}}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(standalone, team).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	rec := httptest.NewRecorder()
	handler.ListWorkers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var list WorkerListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list.Total != 3 {
		t.Fatalf("expected 3 workers (solo + leader + dev), got %d: %+v", list.Total, list.Workers)
	}
	names := map[string]bool{}
	for _, w := range list.Workers {
		names[w.Name] = true
	}
	for _, want := range []string{"solo", "alpha-lead", "alpha-dev"} {
		if !names[want] {
			t.Errorf("missing %q in aggregated list: %+v", want, list.Workers)
		}
	}
}

func TestUpdateWorkerRejectsTeamMember(t *testing.T) {
	scheme := newServerTestScheme(t)
	team := &v1beta1.Team{}
	team.Name = "alpha-team"
	team.Namespace = "default"
	team.Spec.Leader.Name = "alpha-lead"
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{{Name: "alpha-dev"}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/workers/alpha-dev", bytes.NewReader([]byte(`{"model":"new-model"}`)))
	req.SetPathValue("name", "alpha-dev")
	rec := httptest.NewRecorder()
	handler.UpdateWorker(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
}

func TestDeleteWorkerRejectsTeamMember(t *testing.T) {
	scheme := newServerTestScheme(t)
	team := &v1beta1.Team{}
	team.Name = "alpha-team"
	team.Namespace = "default"
	team.Spec.Leader.Name = "alpha-lead"
	team.Spec.Workers = []v1beta1.TeamWorkerSpec{{Name: "alpha-dev"}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workers/alpha-dev", nil)
	req.SetPathValue("name", "alpha-dev")
	rec := httptest.NewRecorder()
	handler.DeleteWorker(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
}

func TestCreateAndUpdateTeamLeaderRuntimeConfig(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

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

// CreateTeam must accept a payload that omits `workers` entirely (leader-only
// team). The CRD no longer lists `workers` in its required-properties set and
// both TeamSpec.Workers / CreateTeamRequest.Workers carry `omitempty`, so a
// caller posting just {name, leader} should get a 201 and the stored CR must
// have Spec.Workers == nil (no implicit empty-slice conversion).
func TestCreateTeam_WithoutWorkers(t *testing.T) {
	scheme := newServerTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := NewResourceHandler(k8sClient, "default", nil)

	body := []byte(`{"name":"leader-only-team","leader":{"name":"lead","model":"qwen3.5-plus"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.CreateTeam(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var resp TeamResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "leader-only-team" {
		t.Errorf("response Name=%q, want %q", resp.Name, "leader-only-team")
	}
	if resp.LeaderName != "lead" {
		t.Errorf("response LeaderName=%q, want %q", resp.LeaderName, "lead")
	}
	if len(resp.WorkerNames) != 0 {
		t.Errorf("response WorkerNames=%+v, want empty", resp.WorkerNames)
	}
	if resp.TotalWorkers != 0 {
		t.Errorf("response TotalWorkers=%d, want 0", resp.TotalWorkers)
	}

	var stored v1beta1.Team
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "leader-only-team", Namespace: "default"}, &stored); err != nil {
		t.Fatalf("get stored team: %v", err)
	}
	if stored.Spec.Workers != nil {
		t.Errorf("stored Spec.Workers=%+v, want nil (no implicit [] from handler)", stored.Spec.Workers)
	}
	if stored.Spec.Leader.Name != "lead" {
		t.Errorf("stored Leader.Name=%q, want %q", stored.Spec.Leader.Name, "lead")
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
