package backend

import (
	"context"
	"testing"
)

// mockWorkerBackend implements WorkerBackend for testing.
type mockWorkerBackend struct {
	name      string
	available bool
}

func (m *mockWorkerBackend) Name() string                                          { return m.name }
func (m *mockWorkerBackend) DeploymentMode() string                                 { return DeployLocal }
func (m *mockWorkerBackend) Available(_ context.Context) bool                      { return m.available }
func (m *mockWorkerBackend) NeedsCredentialInjection() bool                        { return false }
func (m *mockWorkerBackend) Create(_ context.Context, _ CreateRequest) (*WorkerResult, error) { return nil, nil }
func (m *mockWorkerBackend) Delete(_ context.Context, _ string) error              { return nil }
func (m *mockWorkerBackend) Start(_ context.Context, _ string) error               { return nil }
func (m *mockWorkerBackend) Stop(_ context.Context, _ string) error                { return nil }
func (m *mockWorkerBackend) Status(_ context.Context, _ string) (*WorkerResult, error) { return nil, nil }
func (m *mockWorkerBackend) List(_ context.Context) ([]WorkerResult, error)        { return nil, nil }

// mockGatewayBackend implements GatewayBackend for testing.
type mockGatewayBackend struct {
	name      string
	available bool
}

func (m *mockGatewayBackend) Name() string                                                  { return m.name }
func (m *mockGatewayBackend) Available(_ context.Context) bool                              { return m.available }
func (m *mockGatewayBackend) CreateConsumer(_ context.Context, _ ConsumerRequest) (*ConsumerResult, error) { return nil, nil }
func (m *mockGatewayBackend) BindConsumer(_ context.Context, _ BindRequest) error           { return nil }
func (m *mockGatewayBackend) DeleteConsumer(_ context.Context, _ string) error              { return nil }

func TestDetectWorkerBackend_Priority(t *testing.T) {
	docker := &mockWorkerBackend{name: "docker", available: true}
	k8s := &mockWorkerBackend{name: "k8s", available: true}

	reg := NewRegistry([]WorkerBackend{docker, k8s}, nil)
	got := reg.DetectWorkerBackend(context.Background())
	if got == nil || got.Name() != "docker" {
		t.Errorf("expected docker backend (first available), got %v", got)
	}
}

func TestDetectWorkerBackend_Fallback(t *testing.T) {
	docker := &mockWorkerBackend{name: "docker", available: false}
	k8s := &mockWorkerBackend{name: "k8s", available: true}

	reg := NewRegistry([]WorkerBackend{docker, k8s}, nil)
	got := reg.DetectWorkerBackend(context.Background())
	if got == nil || got.Name() != "k8s" {
		t.Errorf("expected k8s backend (fallback), got %v", got)
	}
}

func TestDetectWorkerBackend_None(t *testing.T) {
	docker := &mockWorkerBackend{name: "docker", available: false}

	reg := NewRegistry([]WorkerBackend{docker}, nil)
	got := reg.DetectWorkerBackend(context.Background())
	if got != nil {
		t.Errorf("expected nil when no backend available, got %v", got.Name())
	}
}

func TestGetWorkerBackend_ByName(t *testing.T) {
	docker := &mockWorkerBackend{name: "docker", available: true}
	k8s := &mockWorkerBackend{name: "k8s", available: false}

	reg := NewRegistry([]WorkerBackend{docker, k8s}, nil)

	got, err := reg.GetWorkerBackend(context.Background(), "k8s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "k8s" {
		t.Errorf("expected k8s, got %s", got.Name())
	}
}

func TestGetWorkerBackend_UnknownName(t *testing.T) {
	docker := &mockWorkerBackend{name: "docker", available: true}

	reg := NewRegistry([]WorkerBackend{docker}, nil)

	_, err := reg.GetWorkerBackend(context.Background(), "k8s")
	if err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestGetWorkerBackend_AutoDetect(t *testing.T) {
	docker := &mockWorkerBackend{name: "docker", available: true}

	reg := NewRegistry([]WorkerBackend{docker}, nil)

	got, err := reg.GetWorkerBackend(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "docker" {
		t.Errorf("expected docker, got %s", got.Name())
	}
}

func TestDetectGatewayBackend(t *testing.T) {
	higress := &mockGatewayBackend{name: "higress", available: false}
	apig := &mockGatewayBackend{name: "apig", available: true}

	reg := NewRegistry(nil, []GatewayBackend{higress, apig})
	got := reg.DetectGatewayBackend(context.Background())
	if got == nil || got.Name() != "apig" {
		t.Errorf("expected apig backend, got %v", got)
	}
}
