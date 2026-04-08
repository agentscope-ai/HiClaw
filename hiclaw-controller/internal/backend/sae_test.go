package backend

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sae "github.com/alibabacloud-go/sae-20190506/v4/client"
	"github.com/alibabacloud-go/tea/tea"
)

// mockSAEClient implements SAEClient for testing.
type mockSAEClient struct {
	apps map[string]*mockSAEApp // appName -> app
}

type mockSAEApp struct {
	appID  string
	status string
	envs   string // JSON array
}

func newMockSAEClient() *mockSAEClient {
	return &mockSAEClient{apps: map[string]*mockSAEApp{}}
}

func (m *mockSAEClient) CreateApplication(req *sae.CreateApplicationRequest) (*sae.CreateApplicationResponse, error) {
	name := *req.AppName
	if _, exists := m.apps[name]; exists {
		return nil, fmt.Errorf("app %s already exists", name)
	}
	appID := "app-" + name
	m.apps[name] = &mockSAEApp{
		appID:  appID,
		status: "RUNNING",
		envs:   tea.StringValue(req.Envs),
	}
	return &sae.CreateApplicationResponse{
		Body: &sae.CreateApplicationResponseBody{
			Data: &sae.CreateApplicationResponseBodyData{
				AppId: tea.String(appID),
			},
		},
	}, nil
}

func (m *mockSAEClient) DeleteApplication(req *sae.DeleteApplicationRequest) (*sae.DeleteApplicationResponse, error) {
	for name, app := range m.apps {
		if app.appID == *req.AppId {
			delete(m.apps, name)
			return &sae.DeleteApplicationResponse{}, nil
		}
	}
	return &sae.DeleteApplicationResponse{}, nil
}

func (m *mockSAEClient) StartApplication(req *sae.StartApplicationRequest) (*sae.StartApplicationResponse, error) {
	for _, app := range m.apps {
		if app.appID == *req.AppId {
			app.status = "RUNNING"
			return &sae.StartApplicationResponse{}, nil
		}
	}
	return nil, fmt.Errorf("app not found")
}

func (m *mockSAEClient) StopApplication(req *sae.StopApplicationRequest) (*sae.StopApplicationResponse, error) {
	for _, app := range m.apps {
		if app.appID == *req.AppId {
			app.status = "STOPPED"
			return &sae.StopApplicationResponse{}, nil
		}
	}
	return nil, fmt.Errorf("app not found")
}

func (m *mockSAEClient) DescribeApplicationStatus(req *sae.DescribeApplicationStatusRequest) (*sae.DescribeApplicationStatusResponse, error) {
	for _, app := range m.apps {
		if app.appID == *req.AppId {
			return &sae.DescribeApplicationStatusResponse{
				Body: &sae.DescribeApplicationStatusResponseBody{
					Data: &sae.DescribeApplicationStatusResponseBodyData{
						CurrentStatus: tea.String(app.status),
					},
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("app not found")
}

func (m *mockSAEClient) ListApplications(req *sae.ListApplicationsRequest) (*sae.ListApplicationsResponse, error) {
	var apps []*sae.ListApplicationsResponseBodyDataApplications
	for name, app := range m.apps {
		// Filter by app_name if provided
		if req.AppName != nil && *req.AppName != "" && *req.AppName != name {
			continue
		}
		apps = append(apps, &sae.ListApplicationsResponseBodyDataApplications{
			AppId:   tea.String(app.appID),
			AppName: tea.String(name),
		})
	}
	return &sae.ListApplicationsResponse{
		Body: &sae.ListApplicationsResponseBody{
			Data: &sae.ListApplicationsResponseBodyData{
				Applications: apps,
			},
		},
	}, nil
}

func newTestSAEBackend(client SAEClient) *SAEBackend {
	return NewSAEBackendWithClient(client, SAEConfig{
		Region:      "cn-hangzhou",
		NamespaceID: "test-ns",
		WorkerImage: "hiclaw/worker:latest",
		VPCID:       "vpc-test",
		VSwitchID:   "vsw-test",
		SecurityGroupID: "sg-test",
	}, "hiclaw-worker-")
}

func TestSAECreate(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	result, err := b.Create(context.Background(), CreateRequest{
		Name:  "alice",
		Image: "custom:v1",
		Env:   map[string]string{"KEY": "VAL"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if result.Name != "alice" {
		t.Errorf("expected alice, got %s", result.Name)
	}
	if result.Backend != "sae" {
		t.Errorf("expected sae, got %s", result.Backend)
	}
	if result.AppID == "" {
		t.Error("expected non-empty app ID")
	}
}

func TestSAECreateInjectsCredentials(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	_, err := b.Create(context.Background(), CreateRequest{
		Name:            "cred-test",
		Image:           "custom:v1",
		Env:             map[string]string{"KEY": "VAL"},
		AuthToken:       "test-token-123",
		ControllerURL:   "http://controller:8090",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	app := mock.apps["hiclaw-worker-cred-test"]
	if app == nil {
		t.Fatal("expected app to exist")
	}
	envs := app.envs
	if !strings.Contains(envs, "HICLAW_RUNTIME") {
		t.Error("expected HICLAW_RUNTIME in env")
	}
	if !strings.Contains(envs, "test-token-123") {
		t.Error("expected HICLAW_AUTH_TOKEN value in env")
	}
	if !strings.Contains(envs, "http://controller:8090") {
		t.Error("expected HICLAW_CONTROLLER_URL value in env")
	}
}

func TestSAECreateConflict(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	b.Create(context.Background(), CreateRequest{Name: "alice", Image: "img:v1"})
	_, err := b.Create(context.Background(), CreateRequest{Name: "alice", Image: "img:v1"})
	if err == nil {
		t.Error("expected conflict error")
	}
}

func TestSAEDelete(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	b.Create(context.Background(), CreateRequest{Name: "bob", Image: "img:v1"})
	if err := b.Delete(context.Background(), "bob"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	result, _ := b.Status(context.Background(), "bob")
	if result.Status != StatusNotFound {
		t.Errorf("expected not_found after delete, got %s", result.Status)
	}
}

func TestSAEDeleteNotFound(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	if err := b.Delete(context.Background(), "ghost"); err != nil {
		t.Errorf("delete non-existent should not error, got: %v", err)
	}
}

func TestSAEStartStop(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	b.Create(context.Background(), CreateRequest{Name: "carol", Image: "img:v1"})

	if err := b.Start(context.Background(), "carol"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	result, _ := b.Status(context.Background(), "carol")
	if result.Status != StatusRunning {
		t.Errorf("expected running, got %s", result.Status)
	}

	if err := b.Stop(context.Background(), "carol"); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	result, _ = b.Status(context.Background(), "carol")
	if result.Status != StatusStopped {
		t.Errorf("expected stopped, got %s", result.Status)
	}
}

func TestSAEStartNotFound(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	err := b.Start(context.Background(), "ghost")
	if err == nil {
		t.Error("expected error for non-existent worker")
	}
}

func TestSAEStatus(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	result, _ := b.Status(context.Background(), "nonexistent")
	if result.Status != StatusNotFound {
		t.Errorf("expected not_found, got %s", result.Status)
	}
}

func TestSAEList(t *testing.T) {
	mock := newMockSAEClient()
	b := newTestSAEBackend(mock)

	workers, _ := b.List(context.Background())
	if len(workers) != 0 {
		t.Errorf("expected empty list, got %d", len(workers))
	}

	b.Create(context.Background(), CreateRequest{Name: "w1", Image: "img:v1"})
	b.Create(context.Background(), CreateRequest{Name: "w2", Image: "img:v1"})

	workers, _ = b.List(context.Background())
	if len(workers) != 2 {
		t.Errorf("expected 2 workers, got %d", len(workers))
	}
}

func TestNormalizeSAEStatus(t *testing.T) {
	cases := []struct {
		input    string
		expected WorkerStatus
	}{
		{"RUNNING", StatusRunning},
		{"STOPPED", StatusStopped},
		{"DEPLOYING", StatusStarting},
		{"UNKNOWN", StatusUnknown},
		{"", StatusUnknown},
	}
	for _, tc := range cases {
		got := normalizeSAEStatus(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeSAEStatus(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}
