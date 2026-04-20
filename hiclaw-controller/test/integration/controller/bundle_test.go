//go:build integration

package controller_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/server"
	"github.com/hiclaw/hiclaw-controller/test/testutil/fixtures"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// newBundleHandler constructs a fresh BundleHandler bound to the shared
// envtest k8sClient. Each test gets its own handler to avoid cross-test
// state in the handler (there isn't any, but this keeps the pattern
// forward-compatible).
func newBundleHandler() *server.BundleHandler {
	return server.NewBundleHandler(k8sClient, fixtures.DefaultNamespace)
}

// invokeCreateBundle sends a POST-equivalent request directly into the
// handler, bypassing the HTTP router. Returns the decoded BundleResponse
// and the HTTP status code.
func invokeCreateBundle(t *testing.T, h *server.BundleHandler, req server.TeamBundleRequest) (int, server.BundleResponse) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal bundle req: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/bundles/team", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateTeamBundle(w, r)

	var resp server.BundleResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode bundle resp (status=%d body=%q): %v", w.Code, w.Body.String(), err)
	}
	return w.Code, resp
}

// invokeDeleteBundle sends a DELETE-equivalent request. Uses a custom
// request with PathValue for path parameters since we bypass the router.
func invokeDeleteBundle(t *testing.T, h *server.BundleHandler, name string) (int, server.BundleResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/bundles/team/"+name, nil)
	r.SetPathValue("name", name)
	w := httptest.NewRecorder()
	h.DeleteTeamBundle(w, r)

	var resp server.BundleResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode bundle resp (status=%d body=%q): %v", w.Code, w.Body.String(), err)
	}
	return w.Code, resp
}

func findBundleItem(items []server.BundleResultItem, kind, name string) *server.BundleResultItem {
	for i := range items {
		if items[i].Kind == kind && items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Test B1: Happy path — POST creates Team + Leader + 2 Members; reconcilers
// converge the topology to Active within envtest.
// ---------------------------------------------------------------------------

func TestBundle_Create_HappyPath(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-happy")
	leaderName := teamName + "-lead"
	worker1 := teamName + "-dev1"
	worker2 := teamName + "-dev2"

	h := newBundleHandler()

	req := server.TeamBundleRequest{
		Name: teamName,
		Leader: server.TeamBundleLeader{
			Name:  leaderName,
			Model: "claude-sonnet-4-20250514",
		},
		Workers: []server.TeamBundleWorker{
			{Name: worker1, Model: "gpt-4o-mini"},
			{Name: worker2, Model: "gpt-4o-mini"},
		},
	}

	t.Cleanup(func() {
		// Best-effort cleanup; DeleteBundle exercises its own test case.
		for _, name := range []string{leaderName, worker1, worker2} {
			_ = k8sClient.Delete(ctx, &v1beta1.Worker{})
			_ = k8sClient.Delete(ctx, workerByName(name))
		}
		_ = k8sClient.Delete(ctx, teamByName(teamName))
	})

	status, resp := invokeCreateBundle(t, h, req)
	if status != http.StatusMultiStatus {
		t.Fatalf("status=%d, want %d; items=%+v", status, http.StatusMultiStatus, resp.Items)
	}
	if item := findBundleItem(resp.Items, "team", teamName); item == nil || item.Status != "created" {
		t.Errorf("team item=%+v, want created", item)
	}
	if item := findBundleItem(resp.Items, "worker", leaderName); item == nil || item.Status != "created" {
		t.Errorf("leader item=%+v, want created", item)
	}
	for _, w := range []string{worker1, worker2} {
		if item := findBundleItem(resp.Items, "worker", w); item == nil || item.Status != "created" {
			t.Errorf("worker %s item=%+v, want created", w, item)
		}
	}

	// Let the reconcilers converge.
	var team v1beta1.Team
	team.Name = teamName
	team.Namespace = fixtures.DefaultNamespace
	waitForTeamPhase(t, &team, "Active")
	waitForTeamMembers(t, &team, 2)

	// All workers should be Running.
	for _, name := range []string{leaderName, worker1, worker2} {
		w := workerByName(name)
		assertEventually(t, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(w), w); err != nil {
				return err
			}
			if w.Status.Phase != "Running" {
				return fmt.Errorf("%s phase=%q, want Running", name, w.Status.Phase)
			}
			return nil
		})
	}
}

// ---------------------------------------------------------------------------
// Test B2: Validation failure on any sub-resource -> 400 with no k8s writes.
// ---------------------------------------------------------------------------

func TestBundle_Create_ValidationFailure_NoWrites(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-invalid")
	h := newBundleHandler()

	// Leader name has uppercase; DNS-1123 validation should reject.
	req := server.TeamBundleRequest{
		Name: teamName,
		Leader: server.TeamBundleLeader{
			Name:  "INVALID_NAME",
			Model: "claude-sonnet-4-20250514",
		},
	}

	status, resp := invokeCreateBundle(t, h, req)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d; items=%+v", status, http.StatusBadRequest, resp.Items)
	}
	if len(resp.Items) == 0 {
		t.Fatal("expected validation items, got none")
	}
	for _, item := range resp.Items {
		if item.Kind != "validation" {
			t.Errorf("item.Kind=%q, want validation", item.Kind)
		}
	}

	// Neither the team nor the leader should exist.
	var team v1beta1.Team
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: teamName, Namespace: fixtures.DefaultNamespace}, &team); err == nil {
		t.Error("team was created despite validation failure")
		_ = k8sClient.Delete(ctx, &team)
	}
}

// ---------------------------------------------------------------------------
// Test B3: Non-existent admin Human is reported as a warning; other
// resources still come up.
// ---------------------------------------------------------------------------

func TestBundle_Create_MissingAdmin_WarningOnly(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-ghost")
	leaderName := teamName + "-lead"
	ghostName := "ghost-" + fixtures.UniqueName("x")

	h := newBundleHandler()
	req := server.TeamBundleRequest{
		Name:   teamName,
		Admins: []string{ghostName},
		Leader: server.TeamBundleLeader{Name: leaderName, Model: "gpt-4o-mini"},
	}

	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, workerByName(leaderName))
		_ = k8sClient.Delete(ctx, teamByName(teamName))
	})

	status, resp := invokeCreateBundle(t, h, req)
	if status != http.StatusMultiStatus {
		t.Fatalf("status=%d, want %d; items=%+v", status, http.StatusMultiStatus, resp.Items)
	}
	if item := findBundleItem(resp.Items, "team", teamName); item == nil || item.Status != "created" {
		t.Errorf("team item=%+v, want created", item)
	}
	if item := findBundleItem(resp.Items, "worker", leaderName); item == nil || item.Status != "created" {
		t.Errorf("leader item=%+v, want created", item)
	}
	ghostItem := findBundleItem(resp.Items, "human", ghostName)
	if ghostItem == nil {
		t.Fatalf("ghost human item missing; items=%+v", resp.Items)
	}
	if ghostItem.Status != "not_found" {
		t.Errorf("ghost item.Status=%q, want not_found", ghostItem.Status)
	}
	if !ghostItem.Warning {
		t.Error("ghost item.Warning=false, want true")
	}
}

// ---------------------------------------------------------------------------
// Test B4: Existing Human admin is patched — teamAccess list grows.
// ---------------------------------------------------------------------------

func TestBundle_Create_AdminHumanExists_Patched(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-adm")
	leaderName := teamName + "-lead"
	humanName := fixtures.UniqueName("b-adm-h")

	// Pre-create the Human with no teamAccess.
	human := fixtures.NewTestHuman(humanName)
	if err := k8sClient.Create(ctx, human); err != nil {
		t.Fatalf("create human: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, human) })
	waitForHumanPhase(t, human, "Active")

	h := newBundleHandler()
	req := server.TeamBundleRequest{
		Name:   teamName,
		Admins: []string{humanName},
		Leader: server.TeamBundleLeader{Name: leaderName, Model: "gpt-4o-mini"},
	}

	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, workerByName(leaderName))
		_ = k8sClient.Delete(ctx, teamByName(teamName))
	})

	status, resp := invokeCreateBundle(t, h, req)
	if status != http.StatusMultiStatus {
		t.Fatalf("status=%d, want %d; items=%+v", status, http.StatusMultiStatus, resp.Items)
	}
	humanItem := findBundleItem(resp.Items, "human", humanName)
	if humanItem == nil || humanItem.Status != "patched" {
		t.Fatalf("human item=%+v, want patched", humanItem)
	}

	// Verify teamAccess was actually written.
	var got v1beta1.Human
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(human), &got); err != nil {
		t.Fatalf("get human: %v", err)
	}
	found := false
	for _, entry := range got.Spec.TeamAccess {
		if entry.Team == teamName && entry.Role == v1beta1.TeamAccessRoleAdmin {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("human.TeamAccess=%+v, missing {%s, admin}", got.Spec.TeamAccess, teamName)
	}
}

// ---------------------------------------------------------------------------
// Test B5: Admin already attached -> bundle reports skipped, no double entry.
// ---------------------------------------------------------------------------

func TestBundle_Create_AdminAlreadyAttached_Skipped(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-skip")
	leaderName := teamName + "-lead"
	humanName := fixtures.UniqueName("b-skip-h")

	// Pre-create the Human WITH the admin entry already present.
	human := fixtures.NewTestHuman(humanName,
		fixtures.WithTeamAccess(teamName, v1beta1.TeamAccessRoleAdmin))
	if err := k8sClient.Create(ctx, human); err != nil {
		t.Fatalf("create human: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, human) })
	waitForHumanPhase(t, human, "Active")

	h := newBundleHandler()
	req := server.TeamBundleRequest{
		Name:   teamName,
		Admins: []string{humanName},
		Leader: server.TeamBundleLeader{Name: leaderName, Model: "gpt-4o-mini"},
	}

	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, workerByName(leaderName))
		_ = k8sClient.Delete(ctx, teamByName(teamName))
	})

	status, resp := invokeCreateBundle(t, h, req)
	if status != http.StatusMultiStatus {
		t.Fatalf("status=%d, want %d; items=%+v", status, http.StatusMultiStatus, resp.Items)
	}
	humanItem := findBundleItem(resp.Items, "human", humanName)
	if humanItem == nil || humanItem.Status != "skipped" {
		t.Fatalf("human item=%+v, want skipped", humanItem)
	}

	// teamAccess should still contain exactly one admin entry for this team.
	var got v1beta1.Human
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(human), &got); err != nil {
		t.Fatalf("get human: %v", err)
	}
	count := 0
	for _, entry := range got.Spec.TeamAccess {
		if entry.Team == teamName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("teamAccess entries for %q=%d, want 1", teamName, count)
	}
}

// ---------------------------------------------------------------------------
// Test B6: Duplicate Team name -> 207 with team error, no workers created.
// ---------------------------------------------------------------------------

func TestBundle_Create_DuplicateTeam_Conflict(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-dup")
	leaderName := teamName + "-lead"

	// Pre-create the Team so the bundle's Team create collides.
	team := fixtures.NewTestTeam(teamName)
	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("pre-create team: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, team)
		_ = k8sClient.Delete(ctx, workerByName(leaderName))
	})

	h := newBundleHandler()
	req := server.TeamBundleRequest{
		Name:   teamName,
		Leader: server.TeamBundleLeader{Name: leaderName, Model: "gpt-4o-mini"},
	}

	status, resp := invokeCreateBundle(t, h, req)
	if status != http.StatusMultiStatus {
		t.Fatalf("status=%d, want %d; items=%+v", status, http.StatusMultiStatus, resp.Items)
	}
	teamItem := findBundleItem(resp.Items, "team", teamName)
	if teamItem == nil || teamItem.Status != "error" {
		t.Fatalf("team item=%+v, want error (AlreadyExists)", teamItem)
	}

	// Leader worker MUST NOT have been created (handler short-circuits on team failure).
	var wk v1beta1.Worker
	err := k8sClient.Get(ctx, client.ObjectKey{Name: leaderName, Namespace: fixtures.DefaultNamespace}, &wk)
	if err == nil {
		t.Errorf("leader worker %q was created despite team conflict", leaderName)
	}
}

// ---------------------------------------------------------------------------
// Test B7: DELETE bundle cascades Workers + patches Humans + deletes Team.
// ---------------------------------------------------------------------------

func TestBundle_Delete_CascadesWorkersAndPatchesHumans(t *testing.T) {
	resetAllMocks()

	teamName := fixtures.UniqueName("b-del")
	leaderName := teamName + "-lead"
	workerName := teamName + "-dev"
	humanName := fixtures.UniqueName("b-del-h")

	// Pre-create Human with teamAccess to this team, so DELETE should strip it.
	human := fixtures.NewTestHuman(humanName,
		fixtures.WithTeamAccess(teamName, v1beta1.TeamAccessRoleAdmin))
	if err := k8sClient.Create(ctx, human); err != nil {
		t.Fatalf("create human: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, human) })
	waitForHumanPhase(t, human, "Active")

	h := newBundleHandler()

	// Create the bundle.
	createReq := server.TeamBundleRequest{
		Name:   teamName,
		Leader: server.TeamBundleLeader{Name: leaderName, Model: "gpt-4o-mini"},
		Workers: []server.TeamBundleWorker{
			{Name: workerName, Model: "gpt-4o-mini"},
		},
	}
	if code, resp := invokeCreateBundle(t, h, createReq); code != http.StatusMultiStatus {
		t.Fatalf("create bundle failed: status=%d resp=%+v", code, resp)
	}

	var team v1beta1.Team
	team.Name = teamName
	team.Namespace = fixtures.DefaultNamespace
	waitForTeamPhase(t, &team, "Active")
	waitForTeamMembers(t, &team, 1)

	// Now DELETE the bundle.
	status, resp := invokeDeleteBundle(t, h, teamName)
	if status != http.StatusMultiStatus {
		t.Fatalf("delete status=%d, want %d; resp=%+v", status, http.StatusMultiStatus, resp)
	}
	if item := findBundleItem(resp.Items, "worker", leaderName); item == nil || item.Status != "deleted" {
		t.Errorf("leader item=%+v, want deleted", item)
	}
	if item := findBundleItem(resp.Items, "worker", workerName); item == nil || item.Status != "deleted" {
		t.Errorf("member item=%+v, want deleted", item)
	}
	if item := findBundleItem(resp.Items, "team", teamName); item == nil || item.Status != "deleted" {
		t.Errorf("team item=%+v, want deleted", item)
	}

	// Verify all gone.
	assertEventually(t, func() error {
		for _, name := range []string{leaderName, workerName} {
			err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: fixtures.DefaultNamespace}, &v1beta1.Worker{})
			if err == nil {
				return fmt.Errorf("worker %q still exists", name)
			}
			if client.IgnoreNotFound(err) != nil {
				return err
			}
		}
		var tm v1beta1.Team
		err := k8sClient.Get(ctx, client.ObjectKey{Name: teamName, Namespace: fixtures.DefaultNamespace}, &tm)
		if err == nil {
			return fmt.Errorf("team %q still exists", teamName)
		}
		return client.IgnoreNotFound(err)
	})

	// Human must have lost the teamAccess entry.
	assertEventually(t, func() error {
		var got v1beta1.Human
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(human), &got); err != nil {
			return err
		}
		for _, entry := range got.Spec.TeamAccess {
			if entry.Team == teamName {
				return fmt.Errorf("human.TeamAccess still contains %q: %+v", teamName, got.Spec.TeamAccess)
			}
		}
		return nil
	})

	// Bundle response should mention the Human patch.
	if item := findBundleItem(resp.Items, "human", humanName); item == nil || item.Status != "patched" {
		t.Errorf("human item=%+v, want patched", item)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func workerByName(name string) *v1beta1.Worker {
	w := &v1beta1.Worker{}
	w.Name = name
	w.Namespace = fixtures.DefaultNamespace
	return w
}

func teamByName(name string) *v1beta1.Team {
	t := &v1beta1.Team{}
	t.Name = name
	t.Namespace = fixtures.DefaultNamespace
	return t
}
