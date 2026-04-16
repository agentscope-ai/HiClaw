package service

import (
	"context"
	"fmt"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/gateway"
	"github.com/hiclaw/hiclaw-controller/internal/matrix"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// --- Request / Result types ---

// WorkerProvisionRequest describes the infrastructure to provision for a worker.
type WorkerProvisionRequest struct {
	Name           string
	Role           string // "standalone" | "team_leader" | "worker"
	TeamName       string
	TeamLeaderName string
	McpServers     []string
	ExistingRoomID string // for retry idempotency (from persisted creds)
}

// WorkerProvisionResult contains all outputs from a successful provision.
type WorkerProvisionResult struct {
	MatrixUserID   string
	MatrixToken    string
	RoomID         string
	GatewayKey     string
	MinIOPassword  string
	MatrixPassword string
	AuthorizedMCPs []string
}

// WorkerDeprovisionRequest describes which infrastructure to clean up.
type WorkerDeprovisionRequest struct {
	Name         string
	IsTeamWorker bool
	McpServers   []string
	ExposedPorts []v1beta1.ExposedPortStatus
	ExposeSpec   []v1beta1.ExposePort
}

// TeamRoomRequest describes rooms to create for a team.
type TeamRoomRequest struct {
	TeamName       string
	LeaderName     string
	WorkerNames    []string
	AdminSpec      *v1beta1.TeamAdminSpec
	ExistingRoomID string
}

// TeamRoomResult contains the created room IDs.
type TeamRoomResult struct {
	TeamRoomID     string
	LeaderDMRoomID string
}

// RefreshResult contains refreshed credentials for update operations.
type RefreshResult struct {
	MatrixToken    string
	GatewayKey     string
	MinIOPassword  string
	MatrixPassword string
}

// --- Provisioner ---

// ProvisionerConfig holds configuration for constructing a Provisioner.
type ProvisionerConfig struct {
	Matrix       matrix.Client
	Gateway      gateway.Client
	OSSAdmin     oss.StorageAdminClient // nil in incluster/cloud mode
	Creds        CredentialStore
	K8sClient    kubernetes.Interface
	KubeMode     string
	Namespace    string
	AuthAudience string
	MatrixDomain string
	AdminUser    string

	// Pre-generated Manager secrets (from install script env).
	// When set, used instead of generating random credentials.
	ManagerPassword   string
	ManagerGatewayKey string
}

// Provisioner orchestrates infrastructure provisioning and deprovisioning
// for workers and teams: Matrix accounts/rooms, Gateway consumers, MinIO
// users, K8s ServiceAccounts, and port exposure.
type Provisioner struct {
	matrix       matrix.Client
	gateway      gateway.Client
	ossAdmin     oss.StorageAdminClient
	creds        CredentialStore
	k8sClient    kubernetes.Interface
	kubeMode     string
	namespace    string
	authAudience string
	matrixDomain string
	adminUser    string

	managerPassword   string
	managerGatewayKey string
}

func NewProvisioner(cfg ProvisionerConfig) *Provisioner {
	return &Provisioner{
		matrix:            cfg.Matrix,
		gateway:           cfg.Gateway,
		ossAdmin:          cfg.OSSAdmin,
		creds:             cfg.Creds,
		k8sClient:         cfg.K8sClient,
		kubeMode:          cfg.KubeMode,
		namespace:         cfg.Namespace,
		authAudience:      cfg.AuthAudience,
		matrixDomain:      cfg.MatrixDomain,
		adminUser:         cfg.AdminUser,
		managerPassword:   cfg.ManagerPassword,
		managerGatewayKey: cfg.ManagerGatewayKey,
	}
}

// MatrixUserID builds a full Matrix user ID from a localpart.
func (p *Provisioner) MatrixUserID(name string) string {
	return p.matrix.UserID(name)
}

func (p *Provisioner) DeactivateMatrixUser(ctx context.Context, workerName string) error {
	return p.matrix.DeactivateUser(ctx, workerName)
}

// ProvisionWorker executes the full infrastructure setup for a new worker:
// credentials, Matrix account, MinIO user, Matrix room, Gateway consumer.
func (p *Provisioner) ProvisionWorker(ctx context.Context, req WorkerProvisionRequest) (*WorkerProvisionResult, error) {
	logger := log.FromContext(ctx)
	workerName := req.Name
	consumerName := "worker-" + workerName
	workerMatrixID := p.matrix.UserID(workerName)
	managerMatrixID := p.matrix.UserID("manager")
	adminMatrixID := p.matrix.UserID(p.adminUser)

	isTeamWorker := req.TeamLeaderName != ""

	// Step 1: Load or generate credentials
	creds, err := p.creds.Load(ctx, workerName)
	if err != nil {
		return nil, fmt.Errorf("load credentials: %w", err)
	}
	if creds == nil {
		creds, err = GenerateCredentials()
		if err != nil {
			return nil, fmt.Errorf("generate credentials: %w", err)
		}
		if err := p.creds.Save(ctx, workerName, creds); err != nil {
			return nil, fmt.Errorf("save credentials: %w", err)
		}
	}

	// Use persisted room ID for idempotent retry
	if req.ExistingRoomID == "" {
		req.ExistingRoomID = creds.RoomID
	}

	// Step 2: Register Matrix account
	logger.Info("registering Matrix account", "name", workerName)
	userCreds, err := p.matrix.EnsureUser(ctx, matrix.EnsureUserRequest{
		Username: workerName,
		Password: creds.MatrixPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("Matrix registration failed: %w", err)
	}
	creds.MatrixPassword = userCreds.Password

	// Step 3: Create MinIO user (embedded mode only)
	if p.ossAdmin != nil {
		logger.Info("creating MinIO user", "name", workerName)
		if err := p.ossAdmin.EnsureUser(ctx, workerName, creds.MinIOPassword); err != nil {
			return nil, fmt.Errorf("MinIO user creation failed: %w", err)
		}
		if err := p.ossAdmin.EnsurePolicy(ctx, oss.PolicyRequest{
			WorkerName: workerName,
			TeamName:   req.TeamName,
		}); err != nil {
			return nil, fmt.Errorf("MinIO policy creation failed: %w", err)
		}
	}

	// Step 4: Create Matrix room
	logger.Info("creating Matrix room", "name", workerName)

	var authorityID string
	if isTeamWorker {
		authorityID = p.matrix.UserID(req.TeamLeaderName)
	} else {
		authorityID = managerMatrixID
	}

	powerLevels := map[string]int{
		managerMatrixID: 100,
		adminMatrixID:   100,
		authorityID:     100,
		workerMatrixID:  0,
	}

	roomInfo, err := p.matrix.CreateRoom(ctx, matrix.CreateRoomRequest{
		Name:           fmt.Sprintf("Worker: %s", workerName),
		Topic:          fmt.Sprintf("Communication channel for %s", workerName),
		Invite:         []string{adminMatrixID, authorityID, workerMatrixID},
		PowerLevels:    powerLevels,
		ExistingRoomID: req.ExistingRoomID,
	})
	if err != nil {
		return nil, fmt.Errorf("Matrix room creation failed: %w", err)
	}
	creds.RoomID = roomInfo.RoomID
	logger.Info("Matrix room ready", "roomID", creds.RoomID, "created", roomInfo.Created)

	// Persist credentials (including room ID) for retry idempotency
	if err := p.creds.Save(ctx, workerName, creds); err != nil {
		logger.Error(err, "failed to persist credentials (non-fatal)")
	}

	// Step 5: Gateway consumer and authorization
	logger.Info("creating gateway consumer", "consumer", consumerName)
	consumerResult, err := p.gateway.EnsureConsumer(ctx, gateway.ConsumerRequest{
		Name:          consumerName,
		CredentialKey: creds.GatewayKey,
	})
	if err != nil {
		return nil, fmt.Errorf("gateway consumer creation failed: %w", err)
	}
	if consumerResult.APIKey != "" && consumerResult.APIKey != creds.GatewayKey {
		creds.GatewayKey = consumerResult.APIKey
		_ = p.creds.Save(ctx, workerName, creds)
	}

	if err := p.gateway.AuthorizeAIRoutes(ctx, consumerName); err != nil {
		return nil, fmt.Errorf("AI route authorization failed: %w", err)
	}
	// Higress WASM key-auth plugin needs ~1-2s to sync after route update.
	// Without this, the worker's first LLM call may get 401.
	time.Sleep(2 * time.Second)

	var authorizedMCPs []string
	if len(req.McpServers) > 0 {
		authorizedMCPs, err = p.gateway.AuthorizeMCPServers(ctx, consumerName, req.McpServers)
		if err != nil {
			logger.Error(err, "MCP authorization partial failure (non-fatal)")
		}
	}

	p.gateway.TriggerPush()

	return &WorkerProvisionResult{
		MatrixUserID:   workerMatrixID,
		MatrixToken:    userCreds.AccessToken,
		RoomID:         creds.RoomID,
		GatewayKey:     creds.GatewayKey,
		MinIOPassword:  creds.MinIOPassword,
		MatrixPassword: creds.MatrixPassword,
		AuthorizedMCPs: authorizedMCPs,
	}, nil
}

// DeprovisionWorker cleans up infrastructure for a deleted worker:
// exposed ports, container, gateway auth, MinIO user.
// Best-effort: individual step errors are logged but don't fail the operation.
func (p *Provisioner) DeprovisionWorker(ctx context.Context, req WorkerDeprovisionRequest) error {
	logger := log.FromContext(ctx)
	consumerName := "worker-" + req.Name

	// Clean up exposed ports
	currentExposed := req.ExposedPorts
	if len(currentExposed) == 0 && len(req.ExposeSpec) > 0 {
		for _, ep := range req.ExposeSpec {
			currentExposed = append(currentExposed, v1beta1.ExposedPortStatus{
				Port:   ep.Port,
				Domain: domainForExpose(req.Name, ep.Port),
			})
		}
	}
	if len(currentExposed) > 0 {
		if _, err := p.ReconcileExpose(ctx, req.Name, nil, currentExposed); err != nil {
			logger.Error(err, "failed to clean up exposed ports (non-fatal)")
		}
	}

	// Deauthorize gateway
	if err := p.gateway.DeauthorizeAIRoutes(ctx, consumerName); err != nil {
		logger.Error(err, "failed to deauthorize AI routes (non-fatal)")
	}
	if len(req.McpServers) > 0 {
		if err := p.gateway.DeauthorizeMCPServers(ctx, consumerName, req.McpServers); err != nil {
			logger.Error(err, "failed to deauthorize MCP servers (non-fatal)")
		}
	}
	if err := p.gateway.DeleteConsumer(ctx, consumerName); err != nil {
		logger.Error(err, "failed to delete gateway consumer (non-fatal)")
	}

	// Delete MinIO user (embedded mode)
	if p.ossAdmin != nil {
		if err := p.ossAdmin.DeleteUser(ctx, req.Name); err != nil {
			logger.Error(err, "failed to delete MinIO user (non-fatal)")
		}
	}

	return nil
}

// RefreshCredentials loads persisted credentials and obtains a fresh Matrix token.
// Used during update operations.
func (p *Provisioner) RefreshCredentials(ctx context.Context, workerName string) (*RefreshResult, error) {
	creds, err := p.creds.Load(ctx, workerName)
	if err != nil || creds == nil {
		return nil, fmt.Errorf("credentials not found for %s", workerName)
	}

	matrixToken, err := p.matrix.Login(ctx, workerName, creds.MatrixPassword)
	if err != nil {
		return nil, fmt.Errorf("Matrix login failed: %w", err)
	}

	return &RefreshResult{
		MatrixToken:    matrixToken,
		GatewayKey:     creds.GatewayKey,
		MinIOPassword:  creds.MinIOPassword,
		MatrixPassword: creds.MatrixPassword,
	}, nil
}

// RefreshManagerCredentials loads persisted credentials for the Manager and
// obtains a fresh Matrix token. The Manager CR name (e.g. "default") differs
// from the Matrix username (always "manager"), so this uses a dedicated method.
func (p *Provisioner) RefreshManagerCredentials(ctx context.Context, managerName string) (*RefreshResult, error) {
	creds, err := p.creds.Load(ctx, managerName)
	if err != nil || creds == nil {
		return nil, fmt.Errorf("credentials not found for manager %s", managerName)
	}

	matrixToken, err := p.matrix.Login(ctx, "manager", creds.MatrixPassword)
	if err != nil {
		return nil, fmt.Errorf("Matrix login failed: %w", err)
	}

	return &RefreshResult{
		MatrixToken:    matrixToken,
		GatewayKey:     creds.GatewayKey,
		MinIOPassword:  creds.MinIOPassword,
		MatrixPassword: creds.MatrixPassword,
	}, nil
}

// EnsureManagerGatewayAuth ensures the Manager's gateway consumer exists and is
// authorized on AI routes. Called during container recreation to restore auth
// that may have been lost (e.g. after upgrade with fresh Higress state).
func (p *Provisioner) EnsureManagerGatewayAuth(ctx context.Context, managerName, gatewayKey string) error {
	consumerName := "manager"
	_, err := p.gateway.EnsureConsumer(ctx, gateway.ConsumerRequest{
		Name:          consumerName,
		CredentialKey: gatewayKey,
	})
	if err != nil {
		return fmt.Errorf("ensure consumer: %w", err)
	}
	if err := p.gateway.AuthorizeAIRoutes(ctx, consumerName); err != nil {
		return fmt.Errorf("authorize AI routes: %w", err)
	}
	return nil
}

// ReconcileMCPAuth reauthorizes MCP servers for a consumer. Returns the list of
// successfully authorized server names.
func (p *Provisioner) ReconcileMCPAuth(ctx context.Context, consumerName string, mcpServers []string) ([]string, error) {
	if len(mcpServers) == 0 {
		return nil, nil
	}
	return p.gateway.AuthorizeMCPServers(ctx, consumerName, mcpServers)
}

// ProvisionTeamRooms creates the team room and leader DM room.
func (p *Provisioner) ProvisionTeamRooms(ctx context.Context, req TeamRoomRequest) (*TeamRoomResult, error) {
	logger := log.FromContext(ctx)
	managerMatrixID := p.matrix.UserID("manager")
	adminMatrixID := p.matrix.UserID(p.adminUser)
	leaderMatrixID := p.matrix.UserID(req.LeaderName)

	// Team Room: Leader + Admin + all Workers
	teamInvites := []string{adminMatrixID, leaderMatrixID}
	for _, wn := range req.WorkerNames {
		teamInvites = append(teamInvites, p.matrix.UserID(wn))
	}
	teamPowerLevels := map[string]int{
		managerMatrixID: 100,
		adminMatrixID:   100,
		leaderMatrixID:  100,
	}

	teamRoom, err := p.matrix.CreateRoom(ctx, matrix.CreateRoomRequest{
		Name:           fmt.Sprintf("Team: %s", req.TeamName),
		Topic:          fmt.Sprintf("Team room for %s", req.TeamName),
		Invite:         teamInvites,
		PowerLevels:    teamPowerLevels,
		ExistingRoomID: req.ExistingRoomID,
	})
	if err != nil {
		return nil, fmt.Errorf("team room creation failed: %w", err)
	}
	logger.Info("team room ready", "roomID", teamRoom.RoomID)

	// Leader DM Room: Leader + Admin (+ optional Team Admin)
	leaderDMInvites := []string{adminMatrixID, leaderMatrixID}
	if req.AdminSpec != nil && req.AdminSpec.MatrixUserID != "" {
		leaderDMInvites = append(leaderDMInvites, req.AdminSpec.MatrixUserID)
	}
	leaderDMRoom, err := p.matrix.CreateRoom(ctx, matrix.CreateRoomRequest{
		Name:        fmt.Sprintf("Leader DM: %s", req.LeaderName),
		Topic:       fmt.Sprintf("DM channel for team leader %s", req.LeaderName),
		Invite:      leaderDMInvites,
		PowerLevels: teamPowerLevels,
	})
	if err != nil {
		return nil, fmt.Errorf("leader DM room creation failed: %w", err)
	}
	logger.Info("leader DM room ready", "roomID", leaderDMRoom.RoomID)

	return &TeamRoomResult{
		TeamRoomID:     teamRoom.RoomID,
		LeaderDMRoomID: leaderDMRoom.RoomID,
	}, nil
}

// DeleteCredentials removes persisted credentials for a worker.
func (p *Provisioner) DeleteCredentials(ctx context.Context, workerName string) error {
	return p.creds.Delete(ctx, workerName)
}

// --- Manager Provisioning ---

// ManagerProvisionRequest describes the infrastructure to provision for a Manager.
type ManagerProvisionRequest struct {
	Name       string
	McpServers []string
}

// ManagerProvisionResult contains all outputs from a successful Manager provision.
type ManagerProvisionResult struct {
	MatrixUserID   string
	MatrixToken    string
	RoomID         string
	GatewayKey     string
	MinIOPassword  string
	MatrixPassword string
	AuthorizedMCPs []string
}

// ProvisionManager executes the full infrastructure setup for a Manager Agent:
// credentials, Matrix account, MinIO user, Admin DM room, Gateway consumer.
func (p *Provisioner) ProvisionManager(ctx context.Context, req ManagerProvisionRequest) (*ManagerProvisionResult, error) {
	logger := log.FromContext(ctx)
	managerName := req.Name
	matrixUsername := "manager"
	consumerName := "manager"
	managerMatrixID := p.matrix.UserID(matrixUsername)
	adminMatrixID := p.matrix.UserID(p.adminUser)

	// Step 1: Load or generate credentials
	creds, err := p.creds.Load(ctx, managerName)
	if err != nil {
		return nil, fmt.Errorf("load credentials: %w", err)
	}
	if creds == nil {
		creds, err = GenerateCredentials()
		if err != nil {
			return nil, fmt.Errorf("generate credentials: %w", err)
		}
		// Use pre-generated secrets from install script if available
		if p.managerPassword != "" {
			creds.MatrixPassword = p.managerPassword
		}
		if p.managerGatewayKey != "" {
			creds.GatewayKey = p.managerGatewayKey
		}
		if err := p.creds.Save(ctx, managerName, creds); err != nil {
			return nil, fmt.Errorf("save credentials: %w", err)
		}
	}

	// Step 2: Register Matrix account (always "manager", matching container script)
	logger.Info("registering Manager Matrix account", "matrixUser", matrixUsername)
	userCreds, err := p.matrix.EnsureUser(ctx, matrix.EnsureUserRequest{
		Username: matrixUsername,
		Password: creds.MatrixPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("Matrix registration failed: %w", err)
	}
	creds.MatrixPassword = userCreds.Password

	// Step 3: Create MinIO user (embedded mode only)
	if p.ossAdmin != nil {
		logger.Info("creating MinIO user for Manager", "name", managerName)
		if err := p.ossAdmin.EnsureUser(ctx, managerName, creds.MinIOPassword); err != nil {
			return nil, fmt.Errorf("MinIO user creation failed: %w", err)
		}
		if err := p.ossAdmin.EnsurePolicy(ctx, oss.PolicyRequest{
			WorkerName: managerName,
		}); err != nil {
			return nil, fmt.Errorf("MinIO policy creation failed: %w", err)
		}
	}

	// Step 4: Create Admin DM Room (Admin + Manager only)
	logger.Info("creating Manager Admin DM room", "name", managerName)
	powerLevels := map[string]int{
		adminMatrixID:   100,
		managerMatrixID: 100,
	}
	roomInfo, err := p.matrix.CreateRoom(ctx, matrix.CreateRoomRequest{
		Name:           fmt.Sprintf("Manager: %s", managerName),
		Topic:          fmt.Sprintf("Admin DM channel for Manager %s", managerName),
		Invite:         []string{adminMatrixID, managerMatrixID},
		PowerLevels:    powerLevels,
		IsDirect:       true,
		ExistingRoomID: creds.RoomID,
	})
	if err != nil {
		return nil, fmt.Errorf("Admin DM room creation failed: %w", err)
	}
	creds.RoomID = roomInfo.RoomID
	logger.Info("Manager Admin DM room ready", "roomID", creds.RoomID, "created", roomInfo.Created)

	if err := p.creds.Save(ctx, managerName, creds); err != nil {
		logger.Error(err, "failed to persist credentials (non-fatal)")
	}

	// Step 5: Gateway consumer and authorization
	logger.Info("creating gateway consumer for Manager", "consumer", consumerName)
	consumerResult, err := p.gateway.EnsureConsumer(ctx, gateway.ConsumerRequest{
		Name:          consumerName,
		CredentialKey: creds.GatewayKey,
	})
	if err != nil {
		return nil, fmt.Errorf("gateway consumer creation failed: %w", err)
	}
	if consumerResult.APIKey != "" && consumerResult.APIKey != creds.GatewayKey {
		creds.GatewayKey = consumerResult.APIKey
		_ = p.creds.Save(ctx, managerName, creds)
	}

	if err := p.gateway.AuthorizeAIRoutes(ctx, consumerName); err != nil {
		return nil, fmt.Errorf("AI route authorization failed: %w", err)
	}
	// Higress WASM key-auth plugin needs ~1-2s to sync after route update.
	// Without this, the worker's first LLM call may get 401.
	time.Sleep(2 * time.Second)

	var authorizedMCPs []string
	if len(req.McpServers) > 0 {
		authorizedMCPs, err = p.gateway.AuthorizeMCPServers(ctx, consumerName, req.McpServers)
		if err != nil {
			logger.Error(err, "MCP authorization partial failure (non-fatal)")
		}
	}

	p.gateway.TriggerPush()

	return &ManagerProvisionResult{
		MatrixUserID:   managerMatrixID,
		MatrixToken:    userCreds.AccessToken,
		RoomID:         creds.RoomID,
		GatewayKey:     creds.GatewayKey,
		MinIOPassword:  creds.MinIOPassword,
		MatrixPassword: creds.MatrixPassword,
		AuthorizedMCPs: authorizedMCPs,
	}, nil
}

// DeprovisionManager cleans up infrastructure for a deleted Manager.
func (p *Provisioner) DeprovisionManager(ctx context.Context, name string, mcpServers []string) error {
	logger := log.FromContext(ctx)
	consumerName := "manager"

	if err := p.gateway.DeauthorizeAIRoutes(ctx, consumerName); err != nil {
		logger.Error(err, "failed to deauthorize AI routes (non-fatal)")
	}
	if len(mcpServers) > 0 {
		if err := p.gateway.DeauthorizeMCPServers(ctx, consumerName, mcpServers); err != nil {
			logger.Error(err, "failed to deauthorize MCP servers (non-fatal)")
		}
	}
	if err := p.gateway.DeleteConsumer(ctx, consumerName); err != nil {
		logger.Error(err, "failed to delete gateway consumer (non-fatal)")
	}

	if p.ossAdmin != nil {
		if err := p.ossAdmin.DeleteUser(ctx, name); err != nil {
			logger.Error(err, "failed to delete MinIO user (non-fatal)")
		}
	}

	return nil
}
