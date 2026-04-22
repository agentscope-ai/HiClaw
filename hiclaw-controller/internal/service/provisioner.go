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
	TeamName    string
	LeaderName  string
	WorkerNames []string
	AdminSpec   *v1beta1.TeamAdminSpec
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

// roomAliasLocalpart is the single source of truth for how controller-managed
// rooms are named on the Matrix homeserver. The chosen shape
// "hiclaw-<kind>-<name>" is deliberately verbose to avoid colliding with rooms
// created manually or by unrelated tooling. Changing this format in place
// would orphan every existing room — callers must instead introduce a new
// kind and handle migration explicitly.
func roomAliasLocalpart(kind, name string) string {
	return "hiclaw-" + kind + "-" + name
}

// roomAliasFull builds the full "#localpart:domain" form used by
// ResolveRoomAlias / DeleteRoomAlias.
func (p *Provisioner) roomAliasFull(localpart string) string {
	return "#" + localpart + ":" + p.matrixDomain
}

// leaveAllRooms logs in (or refreshes credentials via orphan recovery) as
// the given Matrix localpart and asks the homeserver to make the user
// leave every room they are currently joined to. Errors leaving individual
// rooms are logged but not returned, so the overall delete flow remains
// best-effort.
//
// credsKey is the storage key passed to the credential loader, which may
// differ from matrixUsername (e.g. manager credentials are stored under
// the Manager CR name, but the Matrix localpart is always "manager").
func (p *Provisioner) leaveAllRooms(ctx context.Context, credsKey, matrixUsername string) error {
	logger := log.FromContext(ctx)

	creds, err := p.creds.Load(ctx, credsKey)
	if err != nil {
		return fmt.Errorf("load credentials for %s: %w", credsKey, err)
	}
	if creds == nil {
		logger.Info("no credentials found; skipping leave-all-rooms", "credsKey", credsKey)
		return nil
	}

	token, err := p.ensureMatrixToken(ctx, matrixUsername, creds)
	if err != nil {
		return fmt.Errorf("login %s: %w", matrixUsername, err)
	}

	rooms, err := p.matrix.ListJoinedRooms(ctx, token)
	if err != nil {
		return fmt.Errorf("list joined rooms for %s: %w", matrixUsername, err)
	}

	for _, roomID := range rooms {
		if err := p.matrix.LeaveRoom(ctx, roomID, token); err != nil {
			logger.Error(err, "leave room (best-effort)",
				"user", matrixUsername, "roomID", roomID)
		}
	}
	return nil
}

// deleteRoom issues a fire-and-forget `!admin rooms delete-room` command
// to the Tuwunel admin bot. Tuwunel processes it asynchronously, and the
// `delete_rooms_after_leave`/`forget_forced_upon_leave` homeserver
// settings act as a fallback if this never lands.
func (p *Provisioner) deleteRoom(ctx context.Context, roomID string) error {
	if roomID == "" {
		return nil
	}
	cmd := fmt.Sprintf("!admin rooms delete-room %s", roomID)
	return p.matrix.AdminCommand(ctx, cmd)
}

// LeaveAllWorkerRooms makes the worker leave every Matrix room it is
// joined to. Used during worker deletion so that rooms where the worker
// was the last local member get pruned via the tuwunel
// delete_rooms_after_leave setting.
func (p *Provisioner) LeaveAllWorkerRooms(ctx context.Context, workerName string) error {
	return p.leaveAllRooms(ctx, workerName, workerName)
}

// DeleteWorkerRoom asks tuwunel to delete the worker's exclusive DM room.
// Fire-and-forget; callers should treat errors as non-fatal.
func (p *Provisioner) DeleteWorkerRoom(ctx context.Context, roomID string) error {
	return p.deleteRoom(ctx, roomID)
}

// LeaveAllManagerRooms makes the manager leave every Matrix room it is
// joined to. Used during manager deletion.
func (p *Provisioner) LeaveAllManagerRooms(ctx context.Context, managerName string) error {
	return p.leaveAllRooms(ctx, managerName, "manager")
}

// DeleteManagerRoom asks tuwunel to delete the manager's exclusive DM
// room. Fire-and-forget.
func (p *Provisioner) DeleteManagerRoom(ctx context.Context, roomID string) error {
	return p.deleteRoom(ctx, roomID)
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
	// Cache the freshly issued access token so subsequent reconciles can reuse
	// it via RefreshCredentials instead of issuing a new login (which would
	// rotate channels.matrix.accessToken in openclaw.json and trigger a
	// gateway restart).
	if userCreds.AccessToken != "" {
		creds.MatrixToken = userCreds.AccessToken
	}

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
		Name:          fmt.Sprintf("Worker: %s", workerName),
		Topic:         fmt.Sprintf("Communication channel for %s", workerName),
		Invite:        []string{adminMatrixID, authorityID, workerMatrixID},
		PowerLevels:   powerLevels,
		RoomAliasName: roomAliasLocalpart("worker", workerName),
	})
	if err != nil {
		return nil, fmt.Errorf("Matrix room creation failed: %w", err)
	}
	roomID := roomInfo.RoomID
	logger.Info("Matrix room ready", "roomID", roomID, "created", roomInfo.Created)

	// Persist the freshly-registered Matrix token. Room identity is no
	// longer stored here — the Matrix alias is the sole source of truth
	// and is resolved via CreateRoom on every reconcile.
	if err := p.creds.Save(ctx, workerName, creds); err != nil {
		logger.Error(err, "failed to persist credentials (non-fatal)")
	}

	// When an existing alias was resolved, CreateRoom returned without
	// sending fresh invites. Reconcile membership so late-added
	// authorities (e.g. a team admin joining after initial provisioning)
	// or recovered power levels are applied.
	if !roomInfo.Created {
		if err := p.ReconcileRoomMembership(ctx, roomID, []string{adminMatrixID, authorityID, workerMatrixID}); err != nil {
			logger.Error(err, "failed to reconcile worker room membership (non-fatal)", "roomID", roomID)
		}
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

	return &WorkerProvisionResult{
		MatrixUserID:   workerMatrixID,
		MatrixToken:    userCreds.AccessToken,
		RoomID:         roomID,
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

// ensureMatrixToken returns creds.MatrixToken if it is non-empty; otherwise it
// performs a fresh matrix.Login under matrixUsername, persists the new token
// back to creds, and returns it. Reusing the cached token across reconciles is
// critical: the controller pushes the manager's openclaw.json into the shared
// filesystem mount on every DeployManagerConfig call, and any change to
// channels.matrix.accessToken triggers an openclaw matrix-client reload (and
// in practice often a full gateway restart due to the related token churn),
// which tears down in-flight agent dispatches. Callers should Save the
// updated creds back to the credential store after this returns so the
// freshly-issued token survives controller restarts.
func (p *Provisioner) ensureMatrixToken(ctx context.Context, matrixUsername string, creds *WorkerCredentials) (string, error) {
	if creds.MatrixToken != "" {
		return creds.MatrixToken, nil
	}
	tok, err := p.matrix.Login(ctx, matrixUsername, creds.MatrixPassword)
	if err != nil {
		return "", err
	}
	creds.MatrixToken = tok
	return tok, nil
}

// RefreshCredentials loads persisted credentials and obtains a Matrix token,
// reusing the cached token when present. Used during update operations.
func (p *Provisioner) RefreshCredentials(ctx context.Context, workerName string) (*RefreshResult, error) {
	creds, err := p.creds.Load(ctx, workerName)
	if err != nil || creds == nil {
		return nil, fmt.Errorf("credentials not found for %s", workerName)
	}

	hadToken := creds.MatrixToken != ""
	matrixToken, err := p.ensureMatrixToken(ctx, workerName, creds)
	if err != nil {
		return nil, fmt.Errorf("Matrix login failed: %w", err)
	}
	if !hadToken {
		if err := p.creds.Save(ctx, workerName, creds); err != nil {
			return nil, fmt.Errorf("persist matrix token: %w", err)
		}
	}

	return &RefreshResult{
		MatrixToken:    matrixToken,
		GatewayKey:     creds.GatewayKey,
		MinIOPassword:  creds.MinIOPassword,
		MatrixPassword: creds.MatrixPassword,
	}, nil
}

// RefreshManagerCredentials loads persisted credentials for the Manager and
// returns a Matrix access token, reusing the cached token when present. The
// Manager CR name (e.g. "default") differs from the Matrix username (always
// "manager"), so this uses a dedicated method.
func (p *Provisioner) RefreshManagerCredentials(ctx context.Context, managerName string) (*RefreshResult, error) {
	creds, err := p.creds.Load(ctx, managerName)
	if err != nil || creds == nil {
		return nil, fmt.Errorf("credentials not found for manager %s", managerName)
	}

	hadToken := creds.MatrixToken != ""
	matrixToken, err := p.ensureMatrixToken(ctx, "manager", creds)
	if err != nil {
		return nil, fmt.Errorf("Matrix login failed: %w", err)
	}
	if !hadToken {
		if err := p.creds.Save(ctx, managerName, creds); err != nil {
			return nil, fmt.Errorf("persist matrix token: %w", err)
		}
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

// ProvisionTeamRooms creates (or resolves) the team room and leader DM room
// and reconciles their Matrix memberships against the desired member set.
// Idempotency is guaranteed by the Matrix alias: repeated calls always land
// on the same RoomID regardless of K8s informer cache state, so no
// "existing room ID" inputs are threaded through. Membership is reconciled
// unconditionally on every call so newly-added workers are invited and
// removed workers are kicked.
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
		Name:          fmt.Sprintf("Team: %s", req.TeamName),
		Topic:         fmt.Sprintf("Team room for %s", req.TeamName),
		Invite:        teamInvites,
		PowerLevels:   teamPowerLevels,
		RoomAliasName: roomAliasLocalpart("team", req.TeamName),
	})
	if err != nil {
		return nil, fmt.Errorf("team room creation failed: %w", err)
	}
	logger.Info("team room ready", "roomID", teamRoom.RoomID, "created", teamRoom.Created)

	// Reconcile unconditionally: on fresh creation the invite list already
	// took effect and Reconcile is a no-op; on alias resolution it catches
	// up members added/removed since the previous run.
	if err := p.ReconcileRoomMembership(ctx, teamRoom.RoomID, teamInvites); err != nil {
		return nil, fmt.Errorf("reconcile team room membership: %w", err)
	}

	// Leader DM Room: Leader + Admin (+ optional Team Admin)
	leaderDMInvites := []string{adminMatrixID, leaderMatrixID}
	if req.AdminSpec != nil && req.AdminSpec.MatrixUserID != "" {
		leaderDMInvites = append(leaderDMInvites, req.AdminSpec.MatrixUserID)
	}
	leaderDMRoom, err := p.matrix.CreateRoom(ctx, matrix.CreateRoomRequest{
		Name:          fmt.Sprintf("Leader DM: %s", req.LeaderName),
		Topic:         fmt.Sprintf("DM channel for team leader %s", req.LeaderName),
		Invite:        leaderDMInvites,
		PowerLevels:   teamPowerLevels,
		RoomAliasName: roomAliasLocalpart("leader-dm", req.LeaderName),
	})
	if err != nil {
		return nil, fmt.Errorf("leader DM room creation failed: %w", err)
	}
	logger.Info("leader DM room ready", "roomID", leaderDMRoom.RoomID, "created", leaderDMRoom.Created)

	if err := p.ReconcileRoomMembership(ctx, leaderDMRoom.RoomID, leaderDMInvites); err != nil {
		return nil, fmt.Errorf("reconcile leader DM membership: %w", err)
	}

	return &TeamRoomResult{
		TeamRoomID:     teamRoom.RoomID,
		LeaderDMRoomID: leaderDMRoom.RoomID,
	}, nil
}

// EnsureRoomMember invites userID into roomID. Idempotent (treats
// already-joined/invited as success). Returns nil on success.
func (p *Provisioner) EnsureRoomMember(ctx context.Context, roomID, userID string) error {
	return p.matrix.InviteToRoom(ctx, roomID, userID)
}

// EnsureRoomNonMember kicks userID out of roomID. Idempotent (treats
// not-in-room as success). Returns nil on success.
func (p *Provisioner) EnsureRoomNonMember(ctx context.Context, roomID, userID, reason string) error {
	return p.matrix.KickFromRoom(ctx, roomID, userID, reason)
}

// ReconcileRoomMembership drives the membership of roomID to match `desired`
// (a list of full Matrix user IDs). Users present in `desired` but not in
// the room are invited; users in the room but not in `desired` are kicked.
// Per-user errors are logged and collected; the first error encountered is
// returned after processing every user (best-effort semantics, consistent
// with DeprovisionWorker).
func (p *Provisioner) ReconcileRoomMembership(ctx context.Context, roomID string, desired []string) error {
	logger := log.FromContext(ctx)

	current, err := p.matrix.ListRoomMembers(ctx, roomID)
	if err != nil {
		return fmt.Errorf("list members of %s: %w", roomID, err)
	}

	desiredSet := make(map[string]struct{}, len(desired))
	for _, u := range desired {
		if u == "" {
			continue
		}
		desiredSet[u] = struct{}{}
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, m := range current {
		currentSet[m.UserID] = struct{}{}
	}

	var firstErr error

	for _, u := range desired {
		if _, ok := currentSet[u]; ok {
			continue
		}
		if err := p.matrix.InviteToRoom(ctx, roomID, u); err != nil {
			logger.Error(err, "failed to invite user to room", "room", roomID, "user", u)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	for _, m := range current {
		if _, ok := desiredSet[m.UserID]; ok {
			continue
		}
		// Leave admin bot alone even if it isn't in `desired`: admin owns
		// power level 100 and some rooms (e.g. Manager Admin DM) expect it
		// implicitly. Callers must include the admin in `desired` when they
		// want it to stay.
		if err := p.matrix.KickFromRoom(ctx, roomID, m.UserID, "removed from desired member set"); err != nil {
			logger.Error(err, "failed to kick user from room", "room", roomID, "user", m.UserID)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// DeleteCredentials removes persisted credentials for a worker.
func (p *Provisioner) DeleteCredentials(ctx context.Context, workerName string) error {
	return p.creds.Delete(ctx, workerName)
}

// DeleteTeamRoomAliases removes the room aliases that identify a team's group
// room and the leader DM room so a future Team CR with the same name can
// reclaim the aliases cleanly. Best-effort: alias removal does not affect
// the underlying room, which is intentionally left intact to preserve chat
// history; it only detaches the controller's stable identifier from it.
func (p *Provisioner) DeleteTeamRoomAliases(ctx context.Context, teamName, leaderName string) error {
	logger := log.FromContext(ctx)
	teamAlias := p.roomAliasFull(roomAliasLocalpart("team", teamName))
	if err := p.matrix.DeleteRoomAlias(ctx, teamAlias); err != nil {
		logger.Error(err, "failed to delete team room alias (non-fatal)", "alias", teamAlias)
	}
	if leaderName != "" {
		leaderAlias := p.roomAliasFull(roomAliasLocalpart("leader-dm", leaderName))
		if err := p.matrix.DeleteRoomAlias(ctx, leaderAlias); err != nil {
			logger.Error(err, "failed to delete leader DM alias (non-fatal)", "alias", leaderAlias)
		}
	}
	return nil
}

// DeleteWorkerRoomAlias removes the alias that identifies a worker's comm
// channel. Same semantics as DeleteTeamRoomAliases — the underlying room is
// preserved, only the controller's handle to it is released.
func (p *Provisioner) DeleteWorkerRoomAlias(ctx context.Context, workerName string) error {
	logger := log.FromContext(ctx)
	alias := p.roomAliasFull(roomAliasLocalpart("worker", workerName))
	if err := p.matrix.DeleteRoomAlias(ctx, alias); err != nil {
		logger.Error(err, "failed to delete worker room alias (non-fatal)", "alias", alias)
	}
	return nil
}

// DeleteManagerRoomAlias removes the alias for the Manager's Admin DM room.
// Same preservation semantics as the worker/team variants.
func (p *Provisioner) DeleteManagerRoomAlias(ctx context.Context, managerName string) error {
	logger := log.FromContext(ctx)
	alias := p.roomAliasFull(roomAliasLocalpart("manager", managerName))
	if err := p.matrix.DeleteRoomAlias(ctx, alias); err != nil {
		logger.Error(err, "failed to delete manager room alias (non-fatal)", "alias", alias)
	}
	return nil
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
	// Cache the freshly issued access token so subsequent reconciles can
	// reuse it via RefreshManagerCredentials instead of issuing a new login
	// (which would rotate channels.matrix.accessToken in openclaw.json and
	// trigger a gateway restart).
	if userCreds.AccessToken != "" {
		creds.MatrixToken = userCreds.AccessToken
	}

	// Step 3: Create MinIO user (embedded mode only)
	if p.ossAdmin != nil {
		logger.Info("creating MinIO user for Manager", "name", managerName)
		if err := p.ossAdmin.EnsureUser(ctx, managerName, creds.MinIOPassword); err != nil {
			return nil, fmt.Errorf("MinIO user creation failed: %w", err)
		}
		if err := p.ossAdmin.EnsurePolicy(ctx, oss.PolicyRequest{
			WorkerName: managerName,
			IsManager:  true,
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
		Name:          fmt.Sprintf("Manager: %s", managerName),
		Topic:         fmt.Sprintf("Admin DM channel for Manager %s", managerName),
		Invite:        []string{adminMatrixID, managerMatrixID},
		PowerLevels:   powerLevels,
		IsDirect:      true,
		RoomAliasName: roomAliasLocalpart("manager", managerName),
	})
	if err != nil {
		return nil, fmt.Errorf("Admin DM room creation failed: %w", err)
	}
	roomID := roomInfo.RoomID
	logger.Info("Manager Admin DM room ready", "roomID", roomID, "created", roomInfo.Created)

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

	return &ManagerProvisionResult{
		MatrixUserID:   managerMatrixID,
		MatrixToken:    userCreds.AccessToken,
		RoomID:         roomID,
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
