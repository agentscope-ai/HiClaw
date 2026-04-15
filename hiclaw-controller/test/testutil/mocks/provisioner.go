package mocks

import (
	"context"
	"sync"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockProvisioner implements service.WorkerProvisioner for testing.
type MockProvisioner struct {
	mu sync.Mutex

	ProvisionWorkerFn    func(ctx context.Context, req service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error)
	DeprovisionWorkerFn  func(ctx context.Context, req service.WorkerDeprovisionRequest) error
	RefreshCredentialsFn func(ctx context.Context, workerName string) (*service.RefreshResult, error)
	ReconcileMCPAuthFn   func(ctx context.Context, consumerName string, mcpServers []string) ([]string, error)
	ReconcileExposeFn    func(ctx context.Context, workerName string, desired []v1beta1.ExposePort, current []v1beta1.ExposedPortStatus) ([]v1beta1.ExposedPortStatus, error)
	EnsureServiceAccountFn func(ctx context.Context, workerName string) error
	DeleteServiceAccountFn func(ctx context.Context, workerName string) error
	DeleteCredentialsFn    func(ctx context.Context, workerName string) error
	RequestSATokenFn       func(ctx context.Context, workerName string) (string, error)
	MatrixUserIDFn         func(name string) string

	Calls struct {
		ProvisionWorker   []service.WorkerProvisionRequest
		DeprovisionWorker []service.WorkerDeprovisionRequest
		RefreshCredentials []string
	}
}

func NewMockProvisioner() *MockProvisioner {
	return &MockProvisioner{}
}

func (m *MockProvisioner) ProvisionWorker(ctx context.Context, req service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error) {
	m.mu.Lock()
	m.Calls.ProvisionWorker = append(m.Calls.ProvisionWorker, req)
	m.mu.Unlock()
	if m.ProvisionWorkerFn != nil {
		return m.ProvisionWorkerFn(ctx, req)
	}
	return &service.WorkerProvisionResult{
		MatrixUserID:   "@" + req.Name + ":localhost",
		MatrixToken:    "mock-token-" + req.Name,
		RoomID:         "!room-" + req.Name + ":localhost",
		GatewayKey:     "mock-gw-key-" + req.Name,
		MinIOPassword:  "mock-minio-pw",
		MatrixPassword: "mock-matrix-pw",
	}, nil
}

func (m *MockProvisioner) DeprovisionWorker(ctx context.Context, req service.WorkerDeprovisionRequest) error {
	m.mu.Lock()
	m.Calls.DeprovisionWorker = append(m.Calls.DeprovisionWorker, req)
	m.mu.Unlock()
	if m.DeprovisionWorkerFn != nil {
		return m.DeprovisionWorkerFn(ctx, req)
	}
	return nil
}

func (m *MockProvisioner) RefreshCredentials(ctx context.Context, workerName string) (*service.RefreshResult, error) {
	m.mu.Lock()
	m.Calls.RefreshCredentials = append(m.Calls.RefreshCredentials, workerName)
	m.mu.Unlock()
	if m.RefreshCredentialsFn != nil {
		return m.RefreshCredentialsFn(ctx, workerName)
	}
	return &service.RefreshResult{
		MatrixToken:    "mock-token-" + workerName,
		GatewayKey:     "mock-gw-key-" + workerName,
		MinIOPassword:  "mock-minio-pw",
		MatrixPassword: "mock-matrix-pw",
	}, nil
}

func (m *MockProvisioner) ReconcileMCPAuth(ctx context.Context, consumerName string, mcpServers []string) ([]string, error) {
	if m.ReconcileMCPAuthFn != nil {
		return m.ReconcileMCPAuthFn(ctx, consumerName, mcpServers)
	}
	return mcpServers, nil
}

func (m *MockProvisioner) ReconcileExpose(ctx context.Context, workerName string, desired []v1beta1.ExposePort, current []v1beta1.ExposedPortStatus) ([]v1beta1.ExposedPortStatus, error) {
	if m.ReconcileExposeFn != nil {
		return m.ReconcileExposeFn(ctx, workerName, desired, current)
	}
	return nil, nil
}

func (m *MockProvisioner) EnsureServiceAccount(ctx context.Context, workerName string) error {
	if m.EnsureServiceAccountFn != nil {
		return m.EnsureServiceAccountFn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) DeleteServiceAccount(ctx context.Context, workerName string) error {
	if m.DeleteServiceAccountFn != nil {
		return m.DeleteServiceAccountFn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) DeleteCredentials(ctx context.Context, workerName string) error {
	if m.DeleteCredentialsFn != nil {
		return m.DeleteCredentialsFn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) RequestSAToken(ctx context.Context, workerName string) (string, error) {
	if m.RequestSATokenFn != nil {
		return m.RequestSATokenFn(ctx, workerName)
	}
	return "mock-sa-token-" + workerName, nil
}

func (m *MockProvisioner) MatrixUserID(name string) string {
	if m.MatrixUserIDFn != nil {
		return m.MatrixUserIDFn(name)
	}
	return "@" + name + ":localhost"
}

var _ service.WorkerProvisioner = (*MockProvisioner)(nil)
