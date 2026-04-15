package mocks

import (
	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockEnvBuilder implements service.WorkerEnvBuilderI for testing.
type MockEnvBuilder struct {
	BuildFn func(workerName string, prov *service.WorkerProvisionResult) map[string]string
}

func NewMockEnvBuilder() *MockEnvBuilder {
	return &MockEnvBuilder{}
}

func (m *MockEnvBuilder) Build(workerName string, prov *service.WorkerProvisionResult) map[string]string {
	if m.BuildFn != nil {
		return m.BuildFn(workerName, prov)
	}
	return map[string]string{
		"HICLAW_WORKER_NAME": workerName,
		"MOCK_ENV":           "true",
	}
}

var _ service.WorkerEnvBuilderI = (*MockEnvBuilder)(nil)
