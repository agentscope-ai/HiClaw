package mocks

import (
	"sync"

	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockEnvBuilder implements service.WorkerEnvBuilderI for testing.
type MockEnvBuilder struct {
	mu sync.Mutex

	BuildFn func(workerName string, prov *service.WorkerProvisionResult) map[string]string

	Calls struct {
		Build []string
	}
}

func NewMockEnvBuilder() *MockEnvBuilder {
	return &MockEnvBuilder{}
}

// Reset clears all Fn overrides and call records.
func (m *MockEnvBuilder) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
	m.BuildFn = nil
}

// ClearCalls resets call records only, preserving Fn overrides.
func (m *MockEnvBuilder) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCallsLocked()
}

func (m *MockEnvBuilder) clearCallsLocked() {
	m.Calls = struct {
		Build []string
	}{}
}

func (m *MockEnvBuilder) Build(workerName string, prov *service.WorkerProvisionResult) map[string]string {
	m.mu.Lock()
	m.Calls.Build = append(m.Calls.Build, workerName)
	m.mu.Unlock()
	if m.BuildFn != nil {
		return m.BuildFn(workerName, prov)
	}
	return map[string]string{
		"HICLAW_WORKER_NAME": workerName,
		"MOCK_ENV":           "true",
	}
}

var _ service.WorkerEnvBuilderI = (*MockEnvBuilder)(nil)
