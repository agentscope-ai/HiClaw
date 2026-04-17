package mocks

import (
	"context"
	"sync"

	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockTeamObserver implements service.TeamObserver for testing. Default
// behaviour returns empty result slices; tests can populate the Members /
// Admins keyed maps below or override with custom Fns to simulate
// specific topology scenarios.
type MockTeamObserver struct {
	mu sync.Mutex

	// Members maps a team name to the list of WorkerObservation returned
	// by ListTeamMembers. Populated via AddMember / SetMembers / Reset.
	Members map[string][]service.WorkerObservation

	// Admins maps a team name to the list of HumanObservation returned
	// by ListTeamAdmins. Populated via AddAdmin / SetAdmins / Reset.
	Admins map[string][]service.HumanObservation

	ListTeamMembersFn func(ctx context.Context, teamName string) ([]service.WorkerObservation, error)
	ListTeamAdminsFn  func(ctx context.Context, teamName string) ([]service.HumanObservation, error)

	Calls struct {
		ListTeamMembers []string
		ListTeamAdmins  []string
	}
}

// NewMockTeamObserver constructs a MockTeamObserver with empty maps.
func NewMockTeamObserver() *MockTeamObserver {
	return &MockTeamObserver{
		Members: make(map[string][]service.WorkerObservation),
		Admins:  make(map[string][]service.HumanObservation),
	}
}

// Reset clears seeded data, Fn overrides, and call records.
func (m *MockTeamObserver) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Members = make(map[string][]service.WorkerObservation)
	m.Admins = make(map[string][]service.HumanObservation)
	m.ListTeamMembersFn = nil
	m.ListTeamAdminsFn = nil
	m.Calls = struct {
		ListTeamMembers []string
		ListTeamAdmins  []string
	}{}
}

// SetMembers replaces the seeded members list for a team.
func (m *MockTeamObserver) SetMembers(teamName string, members []service.WorkerObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Members == nil {
		m.Members = make(map[string][]service.WorkerObservation)
	}
	m.Members[teamName] = members
}

// AddMember appends a single WorkerObservation to the team's members.
func (m *MockTeamObserver) AddMember(teamName string, member service.WorkerObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Members == nil {
		m.Members = make(map[string][]service.WorkerObservation)
	}
	m.Members[teamName] = append(m.Members[teamName], member)
}

// SetAdmins replaces the seeded admin list for a team.
func (m *MockTeamObserver) SetAdmins(teamName string, admins []service.HumanObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Admins == nil {
		m.Admins = make(map[string][]service.HumanObservation)
	}
	m.Admins[teamName] = admins
}

// AddAdmin appends a single HumanObservation to the team's admin list.
func (m *MockTeamObserver) AddAdmin(teamName string, admin service.HumanObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Admins == nil {
		m.Admins = make(map[string][]service.HumanObservation)
	}
	m.Admins[teamName] = append(m.Admins[teamName], admin)
}

// ListTeamMembers returns the seeded member list or invokes the Fn
// override. Records the call so tests can assert on invocation count.
func (m *MockTeamObserver) ListTeamMembers(ctx context.Context, teamName string) ([]service.WorkerObservation, error) {
	m.mu.Lock()
	m.Calls.ListTeamMembers = append(m.Calls.ListTeamMembers, teamName)
	fn := m.ListTeamMembersFn
	members := m.Members[teamName]
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, teamName)
	}
	// Return a copy so callers cannot mutate seeded data.
	out := make([]service.WorkerObservation, len(members))
	copy(out, members)
	return out, nil
}

// ListTeamAdmins returns the seeded admin list or invokes the Fn override.
func (m *MockTeamObserver) ListTeamAdmins(ctx context.Context, teamName string) ([]service.HumanObservation, error) {
	m.mu.Lock()
	m.Calls.ListTeamAdmins = append(m.Calls.ListTeamAdmins, teamName)
	fn := m.ListTeamAdminsFn
	admins := m.Admins[teamName]
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, teamName)
	}
	out := make([]service.HumanObservation, len(admins))
	copy(out, admins)
	return out, nil
}

var _ service.TeamObserver = (*MockTeamObserver)(nil)
