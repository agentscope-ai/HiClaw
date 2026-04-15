package mocks

import (
	"context"
	"sync"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
)

// MockDeployer implements service.WorkerDeployer for testing.
type MockDeployer struct {
	mu sync.Mutex

	DeployPackageFn      func(ctx context.Context, name, uri string, isUpdate bool) error
	WriteInlineConfigsFn func(name string, spec v1beta1.WorkerSpec) error
	DeployWorkerConfigFn func(ctx context.Context, req service.WorkerDeployRequest) error
	PushOnDemandSkillsFn func(ctx context.Context, workerName string, skills []string) error
	CleanupOSSDataFn     func(ctx context.Context, workerName string) error

	Calls struct {
		DeployPackage     []string
		DeployWorkerConfig []string
		CleanupOSSData    []string
	}
}

func NewMockDeployer() *MockDeployer {
	return &MockDeployer{}
}

func (m *MockDeployer) DeployPackage(ctx context.Context, name, uri string, isUpdate bool) error {
	m.mu.Lock()
	m.Calls.DeployPackage = append(m.Calls.DeployPackage, name)
	m.mu.Unlock()
	if m.DeployPackageFn != nil {
		return m.DeployPackageFn(ctx, name, uri, isUpdate)
	}
	return nil
}

func (m *MockDeployer) WriteInlineConfigs(name string, spec v1beta1.WorkerSpec) error {
	if m.WriteInlineConfigsFn != nil {
		return m.WriteInlineConfigsFn(name, spec)
	}
	return nil
}

func (m *MockDeployer) DeployWorkerConfig(ctx context.Context, req service.WorkerDeployRequest) error {
	m.mu.Lock()
	m.Calls.DeployWorkerConfig = append(m.Calls.DeployWorkerConfig, req.Name)
	m.mu.Unlock()
	if m.DeployWorkerConfigFn != nil {
		return m.DeployWorkerConfigFn(ctx, req)
	}
	return nil
}

func (m *MockDeployer) PushOnDemandSkills(ctx context.Context, workerName string, skills []string) error {
	if m.PushOnDemandSkillsFn != nil {
		return m.PushOnDemandSkillsFn(ctx, workerName, skills)
	}
	return nil
}

func (m *MockDeployer) CleanupOSSData(ctx context.Context, workerName string) error {
	m.mu.Lock()
	m.Calls.CleanupOSSData = append(m.Calls.CleanupOSSData, workerName)
	m.mu.Unlock()
	if m.CleanupOSSDataFn != nil {
		return m.CleanupOSSDataFn(ctx, workerName)
	}
	return nil
}

var _ service.WorkerDeployer = (*MockDeployer)(nil)
