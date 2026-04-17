package mocks

import (
	"context"
	"sync"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockProvisioner implements service.WorkerProvisioner AND
// service.TeamProvisioner for testing. The real *service.Provisioner
// satisfies both interfaces so the mock mirrors that shape; tests can
// wire the same MockProvisioner into both WorkerReconciler and
// TeamReconciler without additional plumbing.
type MockProvisioner struct {
	mu sync.Mutex

	// WorkerProvisioner overrides.
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

	// TeamProvisioner overrides.
	EnsureTeamRoomsFn              func(ctx context.Context, req service.TeamRoomsRequest) (*service.TeamRoomsResult, error)
	ReconcileTeamRoomMembershipFn  func(ctx context.Context, req service.TeamRoomMembershipRequest) error
	EnsureTeamStorageFn            func(ctx context.Context, teamName string) error
	CleanupTeamInfraFn             func(ctx context.Context, req service.TeamCleanupRequest) error

	Calls struct {
		ProvisionWorker             []service.WorkerProvisionRequest
		DeprovisionWorker           []service.WorkerDeprovisionRequest
		RefreshCredentials          []string
		ReconcileMCPAuth            []string
		ReconcileExpose             []string
		EnsureServiceAccount        []string
		DeleteServiceAccount        []string
		DeleteCredentials           []string
		RequestSAToken              []string
		DeactivateMatrixUser        []string
		EnsureTeamRooms             []service.TeamRoomsRequest
		ReconcileTeamRoomMembership []service.TeamRoomMembershipRequest
		EnsureTeamStorage           []string
		CleanupTeamInfra            []service.TeamCleanupRequest
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
	m.EnsureTeamRoomsFn = nil
	m.ReconcileTeamRoomMembershipFn = nil
	m.EnsureTeamStorageFn = nil
	m.CleanupTeamInfraFn = nil
}

// ClearCalls resets call records only, preserving Fn overrides.
func (m *MockProvisioner) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
}

func (m *MockProvisioner) clearCallsLocked() {
	m.Calls = struct {
		ProvisionWorker             []service.WorkerProvisionRequest
		DeprovisionWorker           []service.WorkerDeprovisionRequest
		RefreshCredentials          []string
		ReconcileMCPAuth            []string
		ReconcileExpose             []string
		EnsureServiceAccount        []string
		DeleteServiceAccount        []string
		DeleteCredentials           []string
		RequestSAToken              []string
		DeactivateMatrixUser        []string
		EnsureTeamRooms             []service.TeamRoomsRequest
		ReconcileTeamRoomMembership []service.TeamRoomMembershipRequest
		EnsureTeamStorage           []string
		CleanupTeamInfra            []service.TeamCleanupRequest
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

// --- TeamProvisioner interface ---

// EnsureTeamRooms is the mock for the EnsureTeamRooms method. Default
// returns a deterministic pair of room IDs derived from the team name
// so tests can assert on them without wiring a custom Fn.
func (m *MockProvisioner) EnsureTeamRooms(ctx context.Context, req service.TeamRoomsRequest) (*service.TeamRoomsResult, error) {
	m.mu.Lock()
	m.Calls.EnsureTeamRooms = append(m.Calls.EnsureTeamRooms, req)
	fn := m.EnsureTeamRoomsFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	teamRoomID := req.ExistingTeamRoomID
	if teamRoomID == "" {
		teamRoomID = "!team-" + req.TeamName + ":localhost"
	}
	leaderDMRoomID := req.ExistingLeaderDMRoomID
	if leaderDMRoomID == "" {
		leaderDMRoomID = "!leader-dm-" + req.TeamName + ":localhost"
	}
	return &service.TeamRoomsResult{
		TeamRoomID:     teamRoomID,
		LeaderDMRoomID: leaderDMRoomID,
	}, nil
}

// ReconcileTeamRoomMembership records the desired membership diff for the
// team and leader DM rooms. Default implementation is a no-op.
func (m *MockProvisioner) ReconcileTeamRoomMembership(ctx context.Context, req service.TeamRoomMembershipRequest) error {
	m.mu.Lock()
	m.Calls.ReconcileTeamRoomMembership = append(m.Calls.ReconcileTeamRoomMembership, req)
	fn := m.ReconcileTeamRoomMembershipFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil
}

// EnsureTeamStorage records the team shared-storage ensure call.
func (m *MockProvisioner) EnsureTeamStorage(ctx context.Context, teamName string) error {
	m.mu.Lock()
	m.Calls.EnsureTeamStorage = append(m.Calls.EnsureTeamStorage, teamName)
	fn := m.EnsureTeamStorageFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, teamName)
	}
	return nil
}

// CleanupTeamInfra records team finalizer cleanup. Workers are intentionally
// not touched by this call — the refactor moved cascading Worker deletion
// out of the TeamReconciler and into the REST API bundle layer.
func (m *MockProvisioner) CleanupTeamInfra(ctx context.Context, req service.TeamCleanupRequest) error {
	m.mu.Lock()
	m.Calls.CleanupTeamInfra = append(m.Calls.CleanupTeamInfra, req)
	fn := m.CleanupTeamInfraFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil
}

// TeamCallCounts returns the number of team-scope infra calls. Useful
// when asserting that TeamReconciler ran its phases the expected
// number of times.
func (m *MockProvisioner) TeamCallCounts() (ensureRooms, membership, ensureStorage, cleanup int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls.EnsureTeamRooms),
		len(m.Calls.ReconcileTeamRoomMembership),
		len(m.Calls.EnsureTeamStorage),
		len(m.Calls.CleanupTeamInfra)
}

var (
	_ service.WorkerProvisioner = (*MockProvisioner)(nil)
	_ service.TeamProvisioner   = (*MockProvisioner)(nil)
)
