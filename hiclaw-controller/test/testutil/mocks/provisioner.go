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

	ProvisionWorkerFn      func(ctx context.Context, req service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error)
	DeprovisionWorkerFn    func(ctx context.Context, req service.WorkerDeprovisionRequest) error
	RefreshCredentialsFn   func(ctx context.Context, workerName string) (*service.RefreshResult, error)
	ReconcileMCPAuthFn     func(ctx context.Context, consumerName string, mcpServers []string) ([]string, error)
	ReconcileExposeFn      func(ctx context.Context, workerName string, desired []v1beta1.ExposePort, current []v1beta1.ExposedPortStatus) ([]v1beta1.ExposedPortStatus, error)
	EnsureServiceAccountFn func(ctx context.Context, workerName string) error
	DeleteServiceAccountFn func(ctx context.Context, workerName string) error
	DeleteCredentialsFn    func(ctx context.Context, workerName string) error
	RequestSATokenFn       func(ctx context.Context, workerName string) (string, error)
	DeactivateMatrixUserFn func(ctx context.Context, workerName string) error
	MatrixUserIDFn         func(name string) string

	Calls struct {
		ProvisionWorker      []service.WorkerProvisionRequest
		DeprovisionWorker    []service.WorkerDeprovisionRequest
		RefreshCredentials   []string
		ReconcileMCPAuth     []string
		ReconcileExpose      []string
		EnsureServiceAccount []string
		DeleteServiceAccount []string
		DeleteCredentials    []string
		RequestSAToken       []string
		DeactivateMatrixUser []string
	}
}

func NewMockProvisioner() *MockProvisioner {
	return &MockProvisioner{}
}

// Reset clears all Fn overrides and call records.
func (m *MockProvisioner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
	m.ProvisionWorkerFn = nil
	m.DeprovisionWorkerFn = nil
	m.RefreshCredentialsFn = nil
	m.ReconcileMCPAuthFn = nil
	m.ReconcileExposeFn = nil
	m.EnsureServiceAccountFn = nil
	m.DeleteServiceAccountFn = nil
	m.DeleteCredentialsFn = nil
	m.RequestSATokenFn = nil
	m.DeactivateMatrixUserFn = nil
	m.MatrixUserIDFn = nil
}

// ClearCalls resets call records only, preserving Fn overrides.
func (m *MockProvisioner) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
}

func (m *MockProvisioner) clearCallsLocked() {
	m.Calls = struct {
		ProvisionWorker      []service.WorkerProvisionRequest
		DeprovisionWorker    []service.WorkerDeprovisionRequest
		RefreshCredentials   []string
		ReconcileMCPAuth     []string
		ReconcileExpose      []string
		EnsureServiceAccount []string
		DeleteServiceAccount []string
		DeleteCredentials    []string
		RequestSAToken       []string
		DeactivateMatrixUser []string
	}{}
}

func (m *MockProvisioner) ProvisionWorker(ctx context.Context, req service.WorkerProvisionRequest) (*service.WorkerProvisionResult, error) {
	m.mu.Lock()
	m.Calls.ProvisionWorker = append(m.Calls.ProvisionWorker, req)
	fn := m.ProvisionWorkerFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
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
	fn := m.DeprovisionWorkerFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil
}

func (m *MockProvisioner) RefreshCredentials(ctx context.Context, workerName string) (*service.RefreshResult, error) {
	m.mu.Lock()
	m.Calls.RefreshCredentials = append(m.Calls.RefreshCredentials, workerName)
	fn := m.RefreshCredentialsFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName)
	}
	return &service.RefreshResult{
		MatrixToken:    "mock-token-" + workerName,
		GatewayKey:     "mock-gw-key-" + workerName,
		MinIOPassword:  "mock-minio-pw",
		MatrixPassword: "mock-matrix-pw",
	}, nil
}

func (m *MockProvisioner) ReconcileMCPAuth(ctx context.Context, consumerName string, mcpServers []string) ([]string, error) {
	m.mu.Lock()
	m.Calls.ReconcileMCPAuth = append(m.Calls.ReconcileMCPAuth, consumerName)
	fn := m.ReconcileMCPAuthFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, consumerName, mcpServers)
	}
	return mcpServers, nil
}

func (m *MockProvisioner) ReconcileExpose(ctx context.Context, workerName string, desired []v1beta1.ExposePort, current []v1beta1.ExposedPortStatus) ([]v1beta1.ExposedPortStatus, error) {
	m.mu.Lock()
	m.Calls.ReconcileExpose = append(m.Calls.ReconcileExpose, workerName)
	fn := m.ReconcileExposeFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName, desired, current)
	}
	return nil, nil
}

func (m *MockProvisioner) EnsureServiceAccount(ctx context.Context, workerName string) error {
	m.mu.Lock()
	m.Calls.EnsureServiceAccount = append(m.Calls.EnsureServiceAccount, workerName)
	fn := m.EnsureServiceAccountFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) DeleteServiceAccount(ctx context.Context, workerName string) error {
	m.mu.Lock()
	m.Calls.DeleteServiceAccount = append(m.Calls.DeleteServiceAccount, workerName)
	fn := m.DeleteServiceAccountFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) DeleteCredentials(ctx context.Context, workerName string) error {
	m.mu.Lock()
	m.Calls.DeleteCredentials = append(m.Calls.DeleteCredentials, workerName)
	fn := m.DeleteCredentialsFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) RequestSAToken(ctx context.Context, workerName string) (string, error) {
	m.mu.Lock()
	m.Calls.RequestSAToken = append(m.Calls.RequestSAToken, workerName)
	fn := m.RequestSATokenFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName)
	}
	return "mock-sa-token-" + workerName, nil
}

func (m *MockProvisioner) DeactivateMatrixUser(ctx context.Context, workerName string) error {
	m.mu.Lock()
	m.Calls.DeactivateMatrixUser = append(m.Calls.DeactivateMatrixUser, workerName)
	fn := m.DeactivateMatrixUserFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, workerName)
	}
	return nil
}

func (m *MockProvisioner) MatrixUserID(name string) string {
	if m.MatrixUserIDFn != nil {
		return m.MatrixUserIDFn(name)
	}
	return "@" + name + ":localhost"
}

// CallCounts returns a snapshot of call counts safe for concurrent use.
func (m *MockProvisioner) CallCounts() (provision, deprovision, refresh, deactivate int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls.ProvisionWorker),
		len(m.Calls.DeprovisionWorker),
		len(m.Calls.RefreshCredentials),
		len(m.Calls.DeactivateMatrixUser)
}

// ServiceAccountCallCounts returns EnsureServiceAccount and DeleteServiceAccount counts.
func (m *MockProvisioner) ServiceAccountCallCounts() (ensure, delete int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls.EnsureServiceAccount), len(m.Calls.DeleteServiceAccount)
}

var _ service.WorkerProvisioner = (*MockProvisioner)(nil)
