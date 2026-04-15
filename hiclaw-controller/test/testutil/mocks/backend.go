package mocks

import (
	"context"
	"sync"

	"github.com/hiclaw/hiclaw-controller/internal/backend"
)

// MockWorkerBackend implements backend.WorkerBackend for testing.
type MockWorkerBackend struct {
	mu sync.Mutex

	CreateFn func(ctx context.Context, req backend.CreateRequest) (*backend.WorkerResult, error)
	DeleteFn func(ctx context.Context, name string) error
	StartFn  func(ctx context.Context, name string) error
	StopFn   func(ctx context.Context, name string) error
	StatusFn func(ctx context.Context, name string) (*backend.WorkerResult, error)
	ListFn   func(ctx context.Context) ([]backend.WorkerResult, error)

	Calls struct {
		Create []string
		Delete []string
	}
}

func NewMockWorkerBackend() *MockWorkerBackend {
	return &MockWorkerBackend{}
}

func (m *MockWorkerBackend) Name() string           { return "mock" }
func (m *MockWorkerBackend) DeploymentMode() string  { return backend.DeployLocal }
func (m *MockWorkerBackend) Available(_ context.Context) bool { return true }
func (m *MockWorkerBackend) NeedsCredentialInjection() bool  { return false }

func (m *MockWorkerBackend) Create(ctx context.Context, req backend.CreateRequest) (*backend.WorkerResult, error) {
	m.mu.Lock()
	m.Calls.Create = append(m.Calls.Create, req.Name)
	m.mu.Unlock()
	if m.CreateFn != nil {
		return m.CreateFn(ctx, req)
	}
	return &backend.WorkerResult{
		Name:    req.Name,
		Backend: "mock",
		Status:  backend.StatusStarting,
	}, nil
}

func (m *MockWorkerBackend) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	m.Calls.Delete = append(m.Calls.Delete, name)
	m.mu.Unlock()
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, name)
	}
	return nil
}

func (m *MockWorkerBackend) Start(ctx context.Context, name string) error {
	if m.StartFn != nil {
		return m.StartFn(ctx, name)
	}
	return nil
}

func (m *MockWorkerBackend) Stop(ctx context.Context, name string) error {
	if m.StopFn != nil {
		return m.StopFn(ctx, name)
	}
	return nil
}

func (m *MockWorkerBackend) Status(ctx context.Context, name string) (*backend.WorkerResult, error) {
	if m.StatusFn != nil {
		return m.StatusFn(ctx, name)
	}
	return &backend.WorkerResult{
		Name:    name,
		Backend: "mock",
		Status:  backend.StatusRunning,
	}, nil
}

func (m *MockWorkerBackend) List(ctx context.Context) ([]backend.WorkerResult, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

var _ backend.WorkerBackend = (*MockWorkerBackend)(nil)
