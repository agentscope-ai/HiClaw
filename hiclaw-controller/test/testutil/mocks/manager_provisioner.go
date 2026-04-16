package mocks

import (
	"context"
	"sync"

	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockManagerProvisioner implements service.ManagerProvisioner for testing.
type MockManagerProvisioner struct {
	mu sync.Mutex

	ProvisionManagerFn           func(ctx context.Context, req service.ManagerProvisionRequest) (*service.ManagerProvisionResult, error)
	DeprovisionManagerFn         func(ctx context.Context, name string, mcpServers []string) error
	RefreshCredentialsFn         func(ctx context.Context, name string) (*service.RefreshResult, error)
	RefreshManagerCredentialsFn  func(ctx context.Context, managerName string) (*service.RefreshResult, error)
	EnsureManagerGatewayAuthFn   func(ctx context.Context, managerName, gatewayKey string) error
	ReconcileMCPAuthFn           func(ctx context.Context, consumerName string, mcpServers []string) ([]string, error)
	EnsureManagerServiceAccountFn func(ctx context.Context, managerName string) error
	DeleteManagerServiceAccountFn func(ctx context.Context, managerName string) error
	DeleteCredentialsFn          func(ctx context.Context, name string) error
	RequestManagerSATokenFn      func(ctx context.Context, managerName string) (string, error)
	DeactivateMatrixUserFn       func(ctx context.Context, name string) error

	Calls struct {
		ProvisionManager           []service.ManagerProvisionRequest
		DeprovisionManager         []string
		RefreshCredentials         []string
		RefreshManagerCredentials  []string
		EnsureManagerGatewayAuth   []string
		ReconcileMCPAuth           []string
		EnsureManagerServiceAccount []string
		DeleteManagerServiceAccount []string
		DeleteCredentials          []string
		RequestManagerSAToken      []string
		DeactivateMatrixUser       []string
	}
}

func NewMockManagerProvisioner() *MockManagerProvisioner {
	return &MockManagerProvisioner{}
}

func (m *MockManagerProvisioner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
	m.ProvisionManagerFn = nil
	m.DeprovisionManagerFn = nil
	m.RefreshCredentialsFn = nil
	m.RefreshManagerCredentialsFn = nil
	m.EnsureManagerGatewayAuthFn = nil
	m.ReconcileMCPAuthFn = nil
	m.EnsureManagerServiceAccountFn = nil
	m.DeleteManagerServiceAccountFn = nil
	m.DeleteCredentialsFn = nil
	m.RequestManagerSATokenFn = nil
	m.DeactivateMatrixUserFn = nil
}

func (m *MockManagerProvisioner) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
}

func (m *MockManagerProvisioner) clearCallsLocked() {
	m.Calls = struct {
		ProvisionManager           []service.ManagerProvisionRequest
		DeprovisionManager         []string
		RefreshCredentials         []string
		RefreshManagerCredentials  []string
		EnsureManagerGatewayAuth   []string
		ReconcileMCPAuth           []string
		EnsureManagerServiceAccount []string
		DeleteManagerServiceAccount []string
		DeleteCredentials          []string
		RequestManagerSAToken      []string
		DeactivateMatrixUser       []string
	}{}
}

func (m *MockManagerProvisioner) ProvisionManager(ctx context.Context, req service.ManagerProvisionRequest) (*service.ManagerProvisionResult, error) {
	m.mu.Lock()
	m.Calls.ProvisionManager = append(m.Calls.ProvisionManager, req)
	m.mu.Unlock()
	if m.ProvisionManagerFn != nil {
		return m.ProvisionManagerFn(ctx, req)
	}
	return &service.ManagerProvisionResult{
		MatrixUserID:   "@manager:localhost",
		MatrixToken:    "mock-token-manager",
		RoomID:         "!room-manager:localhost",
		GatewayKey:     "mock-gw-key-manager",
		MinIOPassword:  "mock-minio-pw",
		MatrixPassword: "mock-matrix-pw",
	}, nil
}

func (m *MockManagerProvisioner) DeprovisionManager(ctx context.Context, name string, mcpServers []string) error {
	m.mu.Lock()
	m.Calls.DeprovisionManager = append(m.Calls.DeprovisionManager, name)
	m.mu.Unlock()
	if m.DeprovisionManagerFn != nil {
		return m.DeprovisionManagerFn(ctx, name, mcpServers)
	}
	return nil
}

func (m *MockManagerProvisioner) RefreshCredentials(ctx context.Context, name string) (*service.RefreshResult, error) {
	m.mu.Lock()
	m.Calls.RefreshCredentials = append(m.Calls.RefreshCredentials, name)
	m.mu.Unlock()
	if m.RefreshCredentialsFn != nil {
		return m.RefreshCredentialsFn(ctx, name)
	}
	return &service.RefreshResult{
		MatrixToken:    "mock-token-manager",
		GatewayKey:     "mock-gw-key-manager",
		MinIOPassword:  "mock-minio-pw",
		MatrixPassword: "mock-matrix-pw",
	}, nil
}

func (m *MockManagerProvisioner) RefreshManagerCredentials(ctx context.Context, managerName string) (*service.RefreshResult, error) {
	m.mu.Lock()
	m.Calls.RefreshManagerCredentials = append(m.Calls.RefreshManagerCredentials, managerName)
	m.mu.Unlock()
	if m.RefreshManagerCredentialsFn != nil {
		return m.RefreshManagerCredentialsFn(ctx, managerName)
	}
	return &service.RefreshResult{
		MatrixToken:    "mock-token-manager",
		GatewayKey:     "mock-gw-key-manager",
		MinIOPassword:  "mock-minio-pw",
		MatrixPassword: "mock-matrix-pw",
	}, nil
}

func (m *MockManagerProvisioner) EnsureManagerGatewayAuth(ctx context.Context, managerName, gatewayKey string) error {
	m.mu.Lock()
	m.Calls.EnsureManagerGatewayAuth = append(m.Calls.EnsureManagerGatewayAuth, managerName)
	m.mu.Unlock()
	if m.EnsureManagerGatewayAuthFn != nil {
		return m.EnsureManagerGatewayAuthFn(ctx, managerName, gatewayKey)
	}
	return nil
}

func (m *MockManagerProvisioner) ReconcileMCPAuth(ctx context.Context, consumerName string, mcpServers []string) ([]string, error) {
	m.mu.Lock()
	m.Calls.ReconcileMCPAuth = append(m.Calls.ReconcileMCPAuth, consumerName)
	m.mu.Unlock()
	if m.ReconcileMCPAuthFn != nil {
		return m.ReconcileMCPAuthFn(ctx, consumerName, mcpServers)
	}
	return mcpServers, nil
}

func (m *MockManagerProvisioner) EnsureManagerServiceAccount(ctx context.Context, managerName string) error {
	m.mu.Lock()
	m.Calls.EnsureManagerServiceAccount = append(m.Calls.EnsureManagerServiceAccount, managerName)
	m.mu.Unlock()
	if m.EnsureManagerServiceAccountFn != nil {
		return m.EnsureManagerServiceAccountFn(ctx, managerName)
	}
	return nil
}

func (m *MockManagerProvisioner) DeleteManagerServiceAccount(ctx context.Context, managerName string) error {
	m.mu.Lock()
	m.Calls.DeleteManagerServiceAccount = append(m.Calls.DeleteManagerServiceAccount, managerName)
	m.mu.Unlock()
	if m.DeleteManagerServiceAccountFn != nil {
		return m.DeleteManagerServiceAccountFn(ctx, managerName)
	}
	return nil
}

func (m *MockManagerProvisioner) DeleteCredentials(ctx context.Context, name string) error {
	m.mu.Lock()
	m.Calls.DeleteCredentials = append(m.Calls.DeleteCredentials, name)
	m.mu.Unlock()
	if m.DeleteCredentialsFn != nil {
		return m.DeleteCredentialsFn(ctx, name)
	}
	return nil
}

func (m *MockManagerProvisioner) RequestManagerSAToken(ctx context.Context, managerName string) (string, error) {
	m.mu.Lock()
	m.Calls.RequestManagerSAToken = append(m.Calls.RequestManagerSAToken, managerName)
	m.mu.Unlock()
	if m.RequestManagerSATokenFn != nil {
		return m.RequestManagerSATokenFn(ctx, managerName)
	}
	return "mock-sa-token-manager", nil
}

func (m *MockManagerProvisioner) DeactivateMatrixUser(ctx context.Context, name string) error {
	m.mu.Lock()
	m.Calls.DeactivateMatrixUser = append(m.Calls.DeactivateMatrixUser, name)
	m.mu.Unlock()
	if m.DeactivateMatrixUserFn != nil {
		return m.DeactivateMatrixUserFn(ctx, name)
	}
	return nil
}

// CallCounts returns a snapshot of call counts safe for concurrent use.
func (m *MockManagerProvisioner) CallCounts() (provision, deprovision, refreshManager, deactivate int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls.ProvisionManager),
		len(m.Calls.DeprovisionManager),
		len(m.Calls.RefreshManagerCredentials),
		len(m.Calls.DeactivateMatrixUser)
}

var _ service.ManagerProvisioner = (*MockManagerProvisioner)(nil)
