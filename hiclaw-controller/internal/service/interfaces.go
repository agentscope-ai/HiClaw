package service

import (
	"context"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

// WorkerProvisioner defines the provisioning operations used by WorkerReconciler.
// Implemented by *Provisioner; extracted for testability.
type WorkerProvisioner interface {
	ProvisionWorker(ctx context.Context, req WorkerProvisionRequest) (*WorkerProvisionResult, error)
	DeprovisionWorker(ctx context.Context, req WorkerDeprovisionRequest) error
	RefreshCredentials(ctx context.Context, workerName string) (*RefreshResult, error)
	ReconcileMCPAuth(ctx context.Context, consumerName string, mcpServers []string) ([]string, error)
	ReconcileExpose(ctx context.Context, workerName string, desired []v1beta1.ExposePort, current []v1beta1.ExposedPortStatus) ([]v1beta1.ExposedPortStatus, error)
	EnsureServiceAccount(ctx context.Context, workerName string) error
	DeleteServiceAccount(ctx context.Context, workerName string) error
	DeleteCredentials(ctx context.Context, workerName string) error
	RequestSAToken(ctx context.Context, workerName string) (string, error)
	DeactivateMatrixUser(ctx context.Context, workerName string) error
	MatrixUserID(name string) string
}

// WorkerDeployer defines the deployment operations used by WorkerReconciler.
// Implemented by *Deployer; extracted for testability.
type WorkerDeployer interface {
	DeployPackage(ctx context.Context, name, uri string, isUpdate bool) error
	WriteInlineConfigs(name string, spec v1beta1.WorkerSpec) error
	DeployWorkerConfig(ctx context.Context, req WorkerDeployRequest) error
	PushOnDemandSkills(ctx context.Context, workerName string, skills []string) error
	CleanupOSSData(ctx context.Context, workerName string) error
}

// WorkerEnvBuilderI defines env map construction for worker containers.
// Implemented by *WorkerEnvBuilder; extracted for testability.
type WorkerEnvBuilderI interface {
	Build(workerName string, prov *WorkerProvisionResult) map[string]string
}

// Compile-time interface satisfaction checks.
var (
	_ WorkerProvisioner = (*Provisioner)(nil)
	_ WorkerDeployer    = (*Deployer)(nil)
	_ WorkerEnvBuilderI = (*WorkerEnvBuilder)(nil)
)
