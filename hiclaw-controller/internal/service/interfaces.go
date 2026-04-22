package service

import (
	"context"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

// WorkerProvisioner defines the provisioning operations used by WorkerReconciler
// and TeamReconciler. Implemented by *Provisioner; extracted for testability.
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
	// LeaveAllWorkerRooms logs in as the worker (using stored credentials,
	// or resetting the password via admin if they are stale) and makes
	// the worker leave every room it is currently joined to.
	LeaveAllWorkerRooms(ctx context.Context, workerName string) error
	// DeleteWorkerRoom fires an admin "!admin rooms delete-room" command
	// for the given room. Best-effort; the actual deletion runs
	// asynchronously inside tuwunel.
	DeleteWorkerRoom(ctx context.Context, roomID string) error
	MatrixUserID(name string) string
	ProvisionTeamRooms(ctx context.Context, req TeamRoomRequest) (*TeamRoomResult, error)
	DeleteTeamRoomAliases(ctx context.Context, teamName, leaderName string) error
	DeleteWorkerRoomAlias(ctx context.Context, workerName string) error
}

// WorkerDeployer defines the deployment operations used by WorkerReconciler
// and TeamReconciler. Implemented by *Deployer; extracted for testability.
type WorkerDeployer interface {
	DeployPackage(ctx context.Context, name, uri string, isUpdate bool) error
	WriteInlineConfigs(name string, spec v1beta1.WorkerSpec) error
	DeployWorkerConfig(ctx context.Context, req WorkerDeployRequest) error
	PushOnDemandSkills(ctx context.Context, workerName string, skills []string) error
	CleanupOSSData(ctx context.Context, workerName string) error
	InjectCoordinationContext(ctx context.Context, req CoordinationDeployRequest) error
	EnsureTeamStorage(ctx context.Context, teamName string) error
}

// WorkerEnvBuilderI defines env map construction for worker containers.
// Implemented by *WorkerEnvBuilder; extracted for testability.
type WorkerEnvBuilderI interface {
	Build(workerName string, prov *WorkerProvisionResult) map[string]string
}

// ManagerProvisioner defines the provisioning operations used by ManagerReconciler.
// Implemented by *Provisioner; extracted for testability.
//
// NOTE: RefreshCredentials is included because the current handleUpdate calls it
// (likely a bug — should be RefreshManagerCredentials). Phase 2 reconciler rewrite
// will unify to RefreshManagerCredentials only.
type ManagerProvisioner interface {
	ProvisionManager(ctx context.Context, req ManagerProvisionRequest) (*ManagerProvisionResult, error)
	DeprovisionManager(ctx context.Context, name string, mcpServers []string) error
	RefreshCredentials(ctx context.Context, name string) (*RefreshResult, error)
	RefreshManagerCredentials(ctx context.Context, managerName string) (*RefreshResult, error)
	EnsureManagerGatewayAuth(ctx context.Context, managerName, gatewayKey string) error
	ReconcileMCPAuth(ctx context.Context, consumerName string, mcpServers []string) ([]string, error)
	EnsureManagerServiceAccount(ctx context.Context, managerName string) error
	DeleteManagerServiceAccount(ctx context.Context, managerName string) error
	DeleteCredentials(ctx context.Context, name string) error
	RequestManagerSAToken(ctx context.Context, managerName string) (string, error)
	// LeaveAllManagerRooms logs in as the manager and makes it leave every
	// room it is currently joined to. See LeaveAllWorkerRooms.
	LeaveAllManagerRooms(ctx context.Context, managerName string) error
	// DeleteManagerRoom fires an admin "!admin rooms delete-room" command
	// for the given room. See DeleteWorkerRoom.
	DeleteManagerRoom(ctx context.Context, roomID string) error
	DeleteManagerRoomAlias(ctx context.Context, managerName string) error
}

// ManagerDeployer defines the deployment operations used by ManagerReconciler.
// Implemented by *Deployer; extracted for testability.
type ManagerDeployer interface {
	DeployPackage(ctx context.Context, name, uri string, isUpdate bool) error
	DeployManagerConfig(ctx context.Context, req ManagerDeployRequest) error
	PushOnDemandSkills(ctx context.Context, name string, skills []string) error
	CleanupOSSData(ctx context.Context, name string) error
}

// ManagerEnvBuilderI defines env map construction for manager containers.
// Implemented by *WorkerEnvBuilder; extracted for testability.
type ManagerEnvBuilderI interface {
	BuildManager(managerName string, prov *ManagerProvisionResult, spec v1beta1.ManagerSpec) map[string]string
}

// Compile-time interface satisfaction checks.
var (
	_ WorkerProvisioner = (*Provisioner)(nil)
	_ WorkerDeployer    = (*Deployer)(nil)
	_ WorkerEnvBuilderI = (*WorkerEnvBuilder)(nil)

	_ ManagerProvisioner = (*Provisioner)(nil)
	_ ManagerDeployer    = (*Deployer)(nil)
	_ ManagerEnvBuilderI = (*WorkerEnvBuilder)(nil)
)
