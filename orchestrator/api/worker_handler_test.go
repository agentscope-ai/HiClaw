package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alibaba/hiclaw/orchestrator/auth"
	"github.com/alibaba/hiclaw/orchestrator/backend"
)

// mockBackend implements backend.WorkerBackend for handler tests.
type mockBackend struct {
	name      string
	available bool
	workers   map[string]*backend.WorkerResult

	createErr error
	startErr  error
	stopErr   error
	deleteErr error
}

func newMockBackend() *mockBackend {
	return &mockBackend{
		name:      "mock",
		available: true,
		workers:   map[string]*backend.WorkerResult{},
	}
}

func (m *mockBackend) Name() string                          { return m.name }
func (m *mockBackend) DeploymentMode() string                 { return backend.DeployLocal }
func (m *mockBackend) Available(_ context.Context) bool      { return m.available }
func (m *mockBackend) NeedsCredentialInjection() bool        { return false }

func (m *mockBackend) Create(_ context.Context, req backend.CreateRequest) (*backend.WorkerResult, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	r := &backend.WorkerResult{
		Name:           req.Name,
		Backend:        "mock",
		DeploymentMode: backend.DeployLocal,
		Status:         backend.StatusRunning,
		ContainerID:    "mock-" + req.Name,
		RawStatus:      "running",
	}
	m.workers[req.Name] = r
	return r, nil
}

func (m *mockBackend) Delete(_ context.Context, name string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.workers, name)
	return nil
}

func (m *mockBackend) Start(_ context.Context, name string) error {
	if m.startErr != nil {
		return m.startErr
	}
	if w, ok := m.workers[name]; ok {
		w.Status = backend.StatusRunning
		return nil
	}
	return backend.ErrNotFound
}

func (m *mockBackend) Stop(_ context.Context, name string) error {
	if m.stopErr != nil {
		return m.stopErr
	}
	if w, ok := m.workers[name]; ok {
		w.Status = backend.StatusStopped
		return nil
	}
	return backend.ErrNotFound
}

func (m *mockBackend) Status(_ context.Context, name string) (*backend.WorkerResult, error) {
	if w, ok := m.workers[name]; ok {
		return w, nil
	}
	return &backend.WorkerResult{
		Name:    name,
		Backend: "mock",
		Status:  backend.StatusNotFound,
	}, nil
}

func (m *mockBackend) List(_ context.Context) ([]backend.WorkerResult, error) {
	results := make([]backend.WorkerResult, 0, len(m.workers))
	for _, w := range m.workers {
		results = append(results, *w)
	}
	return results, nil
}

func setupHandler(mb *mockBackend) (*WorkerHandler, *http.ServeMux) {
	reg := backend.NewRegistry([]backend.WorkerBackend{mb}, nil)
	ks := auth.NewKeyStore("", nil) // auth disabled for handler tests
	h := NewWorkerHandler(reg, ks, "")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /workers", h.Create)
	mux.HandleFunc("GET /workers", h.List)
	mux.HandleFunc("GET /workers/{name}", h.Status)
	mux.HandleFunc("POST /workers/{name}/ready", h.Ready)
	mux.HandleFunc("POST /workers/{name}/start", h.Start)
	mux.HandleFunc("POST /workers/{name}/stop", h.Stop)
	mux.HandleFunc("DELETE /workers/{name}", h.Delete)
	return h, mux
}

func TestCreateWorker(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{
		Name:  "alice",
		Image: "hiclaw/worker-agent:latest",
	})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Name != "alice" {
		t.Errorf("expected name alice, got %s", resp.Name)
	}
	if resp.Status != backend.StatusRunning {
		t.Errorf("expected status running, got %s", resp.Status)
	}
	if resp.Backend != "mock" {
		t.Errorf("expected backend mock, got %s", resp.Backend)
	}
	if resp.DeploymentMode != backend.DeployLocal {
		t.Errorf("expected deployment_mode local, got %s", resp.DeploymentMode)
	}
}

func TestCreateWorkerMissingName(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateWorkerMissingImage(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "alice"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Image is optional — backend provides default
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
}

func TestCreateWorkerInvalidJSON(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateWorkerConflict(t *testing.T) {
	mb := newMockBackend()
	mb.createErr = backend.ErrConflict
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "alice", Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkerBackendUnavailable(t *testing.T) {
	mb := newMockBackend()
	mb.available = false
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "alice", Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestListWorkersEmpty(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WorkerListResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Workers) != 0 {
		t.Errorf("expected empty list, got %d", len(resp.Workers))
	}
}

func TestListWorkersNoBackend(t *testing.T) {
	mb := newMockBackend()
	mb.available = false
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even with no backend, got %d", w.Code)
	}

	var resp WorkerListResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Workers) != 0 {
		t.Errorf("expected empty list, got %d", len(resp.Workers))
	}
}

func TestStatusWorker(t *testing.T) {
	mb := newMockBackend()
	mb.workers["alice"] = &backend.WorkerResult{
		Name: "alice", Backend: "mock", Status: backend.StatusRunning, ContainerID: "mock-alice",
	}
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodGet, "/workers/alice", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusRunning {
		t.Errorf("expected running, got %s", resp.Status)
	}
}

func TestStatusWorkerNotFound(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodGet, "/workers/ghost", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusNotFound {
		t.Errorf("expected not_found, got %s", resp.Status)
	}
}

func TestStartWorkerNotFound(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodPost, "/workers/ghost/start", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStopWorkerNotFound(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodPost, "/workers/ghost/stop", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteWorker(t *testing.T) {
	mb := newMockBackend()
	mb.workers["alice"] = &backend.WorkerResult{Name: "alice"}
	_, mux := setupHandler(mb)

	req := httptest.NewRequest(http.MethodDelete, "/workers/alice", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if _, exists := mb.workers["alice"]; exists {
		t.Error("expected worker to be deleted")
	}
}

func TestCreateWorkerGenericError(t *testing.T) {
	mb := newMockBackend()
	mb.createErr = errors.New("something broke")
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "alice", Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestGatewayNoBackend(t *testing.T) {
	reg := backend.NewRegistry(nil, nil) // no gateway backends
	h := NewGatewayHandler(reg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gateway/consumers", h.CreateConsumer)
	mux.HandleFunc("POST /gateway/consumers/{id}/bind", h.BindConsumer)
	mux.HandleFunc("DELETE /gateway/consumers/{id}", h.DeleteConsumer)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/gateway/consumers"},
		{http.MethodPost, "/gateway/consumers/test-id/bind"},
		{http.MethodDelete, "/gateway/consumers/test-id"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: expected 501, got %d", ep.method, ep.path, w.Code)
		}
	}
}

// --- Readiness tests ---

func TestReadyEndpoint(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "alice", Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Status should be "running" before ready report
	req = httptest.NewRequest(http.MethodGet, "/workers/alice", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusRunning {
		t.Errorf("expected running before ready, got %s", resp.Status)
	}

	// Report ready
	req = httptest.NewRequest(http.MethodPost, "/workers/alice/ready", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("ready: expected 204, got %d", w.Code)
	}

	// Status should now be "ready"
	req = httptest.NewRequest(http.MethodGet, "/workers/alice", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusReady {
		t.Errorf("expected ready after report, got %s", resp.Status)
	}
}

func TestReadyOnlyUpgradesRunning(t *testing.T) {
	mb := newMockBackend()
	mb.workers["bob"] = &backend.WorkerResult{
		Name: "bob", Backend: "mock", Status: backend.StatusStopped,
	}
	h, mux := setupHandler(mb)
	h.setReady("bob", true)

	req := httptest.NewRequest(http.MethodGet, "/workers/bob", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusStopped {
		t.Errorf("expected stopped (ready should not upgrade non-running), got %s", resp.Status)
	}
}

func TestReadyClearedOnStop(t *testing.T) {
	mb := newMockBackend()
	_, mux := setupHandler(mb)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "carol", Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodPost, "/workers/carol/ready", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodPost, "/workers/carol/stop", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodPost, "/workers/carol/start", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodGet, "/workers/carol", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusRunning {
		t.Errorf("expected running after stop+start (readiness cleared), got %s", resp.Status)
	}
}

func TestReadyClearedOnCreate(t *testing.T) {
	mb := newMockBackend()
	h, mux := setupHandler(mb)
	h.setReady("dave", true)

	body, _ := json.Marshal(CreateWorkerRequest{Name: "dave", Image: "img:latest"})
	req := httptest.NewRequest(http.MethodPost, "/workers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodGet, "/workers/dave", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var resp WorkerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != backend.StatusRunning {
		t.Errorf("expected running (stale readiness cleared on create), got %s", resp.Status)
	}
}

func TestReadyForbiddenCrossWorker(t *testing.T) {
	mb := newMockBackend()
	h, _ := setupHandler(mb)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /workers/{name}/ready", h.Ready)

	req := httptest.NewRequest(http.MethodPost, "/workers/bob/ready", nil)
	ctx := context.WithValue(req.Context(), auth.CallerKeyForTest(), &auth.CallerIdentity{
		Role: auth.RoleWorker, WorkerName: "alice",
	})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cross-worker ready report, got %d", w.Code)
	}
}
