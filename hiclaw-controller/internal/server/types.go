package server

import v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"

// --- Worker API types ---

type CreateWorkerRequest struct {
	Name          string                     `json:"name"`
	Model         string                     `json:"model,omitempty"`
	Runtime       string                     `json:"runtime,omitempty"`
	Image         string                     `json:"image,omitempty"`
	Identity      string                     `json:"identity,omitempty"`
	Soul          string                     `json:"soul,omitempty"`
	Agents        string                     `json:"agents,omitempty"`
	Skills        []string                   `json:"skills,omitempty"`
	McpServers    []string                   `json:"mcpServers,omitempty"`
	Package       string                     `json:"package,omitempty"`
	Expose        []v1beta1.ExposePort       `json:"expose,omitempty"`
	ChannelPolicy *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	State         *string                    `json:"state,omitempty"`

	// Team citizenship (Stage 10): role and teamRef are first-class Worker
	// spec fields. Callers with a RoleTeamLeader auth context have these
	// forced to (team_worker, caller.Team) by the handler.
	Role    string `json:"role,omitempty"`    // standalone | team_leader | team_worker
	TeamRef string `json:"teamRef,omitempty"` // Team CR name
}

type UpdateWorkerRequest struct {
	Model         string                     `json:"model,omitempty"`
	Runtime       string                     `json:"runtime,omitempty"`
	Image         string                     `json:"image,omitempty"`
	Identity      string                     `json:"identity,omitempty"`
	Soul          string                     `json:"soul,omitempty"`
	Agents        string                     `json:"agents,omitempty"`
	Skills        []string                   `json:"skills,omitempty"`
	McpServers    []string                   `json:"mcpServers,omitempty"`
	Package       string                     `json:"package,omitempty"`
	Expose        []v1beta1.ExposePort       `json:"expose,omitempty"`
	ChannelPolicy *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	State         *string                    `json:"state,omitempty"`

	// Role override: empty means "leave unchanged"; non-empty replaces spec.Role.
	Role string `json:"role,omitempty"`
	// TeamRef pointer: nil = leave unchanged; non-nil (including "") replaces
	// spec.TeamRef so callers can promote/demote between standalone and team.
	TeamRef *string `json:"teamRef,omitempty"`
}

type WorkerResponse struct {
	Name           string            `json:"name"`
	Phase          string            `json:"phase"`
	State          string            `json:"state,omitempty"`
	Model          string            `json:"model,omitempty"`
	Runtime        string            `json:"runtime,omitempty"`
	Image          string            `json:"image,omitempty"`
	ContainerState string            `json:"containerState,omitempty"`
	MatrixUserID   string            `json:"matrixUserID,omitempty"`
	RoomID         string            `json:"roomID,omitempty"`
	Message        string            `json:"message,omitempty"`
	ExposedPorts   []ExposedPortInfo `json:"exposedPorts,omitempty"`

	Role    string `json:"role,omitempty"`
	TeamRef string `json:"teamRef,omitempty"`
}

type ExposedPortInfo struct {
	Port   int    `json:"port"`
	Domain string `json:"domain"`
}

type WorkerListResponse struct {
	Workers []WorkerResponse `json:"workers"`
	Total   int              `json:"total"`
}

// --- Team API types ---

// CreateTeamRequest is the slim Team CR create payload. Per the refactor,
// Leader / Workers / Admin are no longer part of Team spec; use the bundle
// endpoint (POST /api/v1/bundles/team) to create a Team together with its
// Leader and Member Workers in one call.
type CreateTeamRequest struct {
	Name              string                     `json:"name"`
	Description       string                     `json:"description,omitempty"`
	PeerMentions      *bool                      `json:"peerMentions,omitempty"`
	ChannelPolicy     *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	Heartbeat         *v1beta1.TeamHeartbeatSpec `json:"heartbeat,omitempty"`
	WorkerIdleTimeout string                     `json:"workerIdleTimeout,omitempty"`
}

type UpdateTeamRequest struct {
	Description       string                     `json:"description,omitempty"`
	PeerMentions      *bool                      `json:"peerMentions,omitempty"`
	ChannelPolicy     *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	Heartbeat         *v1beta1.TeamHeartbeatSpec `json:"heartbeat,omitempty"`
	WorkerIdleTimeout string                     `json:"workerIdleTimeout,omitempty"`
}

// TeamMemberInfo is the response-shape projection of a Team member (flattened
// from v1beta1.TeamMemberObservation for the REST API).
type TeamMemberInfo struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
	Ready        bool   `json:"ready"`
}

// TeamAdminInfo is the response-shape projection of a Team admin Human.
type TeamAdminInfo struct {
	HumanName    string `json:"humanName"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
}

type TeamResponse struct {
	Name              string                     `json:"name"`
	Phase             string                     `json:"phase"`
	Description       string                     `json:"description,omitempty"`
	Heartbeat         *v1beta1.TeamHeartbeatSpec `json:"heartbeat,omitempty"`
	WorkerIdleTimeout string                     `json:"workerIdleTimeout,omitempty"`

	TeamRoomID         string `json:"teamRoomID,omitempty"`
	LeaderDMRoomID     string `json:"leaderDMRoomID,omitempty"`
	LeaderName         string `json:"leaderName,omitempty"`
	LeaderMatrixUserID string `json:"leaderMatrixUserID,omitempty"`
	LeaderReady        bool   `json:"leaderReady"`

	Members      []TeamMemberInfo `json:"members,omitempty"`
	Admins       []TeamAdminInfo  `json:"admins,omitempty"`
	TotalMembers int              `json:"totalMembers,omitempty"`
	ReadyMembers int              `json:"readyMembers,omitempty"`
	Message      string           `json:"message,omitempty"`
}

type TeamListResponse struct {
	Teams []TeamResponse `json:"teams"`
	Total int            `json:"total"`
}

// --- Human API types ---

type CreateHumanRequest struct {
	Name         string                    `json:"name"`
	DisplayName  string                    `json:"displayName"`
	Email        string                    `json:"email,omitempty"`
	Note         string                    `json:"note,omitempty"`
	SuperAdmin   bool                      `json:"superAdmin,omitempty"`
	TeamAccess   []v1beta1.TeamAccessEntry `json:"teamAccess,omitempty"`
	WorkerAccess []string                  `json:"workerAccess,omitempty"`
}

// UpdateHumanRequest differentiates "leave unchanged" from "clear to empty":
// nil slice / nil pointer keeps the field; a non-nil value replaces it.
type UpdateHumanRequest struct {
	DisplayName  string                    `json:"displayName,omitempty"`
	Email        string                    `json:"email,omitempty"`
	Note         string                    `json:"note,omitempty"`
	SuperAdmin   *bool                     `json:"superAdmin,omitempty"`
	TeamAccess   []v1beta1.TeamAccessEntry `json:"teamAccess,omitempty"`
	WorkerAccess []string                  `json:"workerAccess,omitempty"`
}

type HumanResponse struct {
	Name            string                    `json:"name"`
	Phase           string                    `json:"phase"`
	DisplayName     string                    `json:"displayName"`
	Email           string                    `json:"email,omitempty"`
	MatrixUserID    string                    `json:"matrixUserID,omitempty"`
	InitialPassword string                    `json:"initialPassword,omitempty"`
	Rooms           []string                  `json:"rooms,omitempty"`
	SuperAdmin      bool                      `json:"superAdmin,omitempty"`
	TeamAccess      []v1beta1.TeamAccessEntry `json:"teamAccess,omitempty"`
	WorkerAccess    []string                  `json:"workerAccess,omitempty"`
	Message         string                    `json:"message,omitempty"`
}

type HumanListResponse struct {
	Humans []HumanResponse `json:"humans"`
	Total  int             `json:"total"`
}

// --- Manager API types ---

type CreateManagerRequest struct {
	Name       string                 `json:"name"`
	Model      string                 `json:"model"`
	Runtime    string                 `json:"runtime,omitempty"`
	Image      string                 `json:"image,omitempty"`
	Soul       string                 `json:"soul,omitempty"`
	Agents     string                 `json:"agents,omitempty"`
	Skills     []string               `json:"skills,omitempty"`
	McpServers []string               `json:"mcpServers,omitempty"`
	Package    string                 `json:"package,omitempty"`
	Config     *v1beta1.ManagerConfig `json:"config,omitempty"`
	State      *string                `json:"state,omitempty"`
}

type UpdateManagerRequest struct {
	Model      string                 `json:"model,omitempty"`
	Runtime    string                 `json:"runtime,omitempty"`
	Image      string                 `json:"image,omitempty"`
	Soul       string                 `json:"soul,omitempty"`
	Agents     string                 `json:"agents,omitempty"`
	Skills     []string               `json:"skills,omitempty"`
	McpServers []string               `json:"mcpServers,omitempty"`
	Package    string                 `json:"package,omitempty"`
	Config     *v1beta1.ManagerConfig `json:"config,omitempty"`
	State      *string                `json:"state,omitempty"`
}

type ManagerResponse struct {
	Name         string `json:"name"`
	Phase        string `json:"phase"`
	State        string `json:"state,omitempty"`
	Model        string `json:"model,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Image        string `json:"image,omitempty"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
	RoomID       string `json:"roomID,omitempty"`
	Version      string `json:"version,omitempty"`
	Message      string `json:"message,omitempty"`
}

type ManagerListResponse struct {
	Managers []ManagerResponse `json:"managers"`
	Total    int               `json:"total"`
}

// --- Gateway API types ---

type CreateConsumerRequest struct {
	Name          string `json:"name"`
	CredentialKey string `json:"credential_key,omitempty"`
}

type ConsumerResponse struct {
	Name       string `json:"name"`
	ConsumerID string `json:"consumer_id"`
	APIKey     string `json:"api_key,omitempty"`
	Status     string `json:"status"`
}

// --- Lifecycle API types ---

type WorkerLifecycleResponse struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

// --- Bundle API types (Stage 10) ---

// TeamBundleLeader is the inline Worker spec view for the team leader in a
// bundle request. Name is required; all other fields map 1:1 to WorkerSpec.
type TeamBundleLeader struct {
	Name          string                     `json:"name"`
	Model         string                     `json:"model,omitempty"`
	Runtime       string                     `json:"runtime,omitempty"`
	Image         string                     `json:"image,omitempty"`
	Identity      string                     `json:"identity,omitempty"`
	Soul          string                     `json:"soul,omitempty"`
	Agents        string                     `json:"agents,omitempty"`
	Skills        []string                   `json:"skills,omitempty"`
	McpServers    []string                   `json:"mcpServers,omitempty"`
	Package       string                     `json:"package,omitempty"`
	Expose        []v1beta1.ExposePort       `json:"expose,omitempty"`
	ChannelPolicy *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	State         *string                    `json:"state,omitempty"`
}

// TeamBundleWorker mirrors TeamBundleLeader for member Workers.
type TeamBundleWorker struct {
	Name          string                     `json:"name"`
	Model         string                     `json:"model,omitempty"`
	Runtime       string                     `json:"runtime,omitempty"`
	Image         string                     `json:"image,omitempty"`
	Identity      string                     `json:"identity,omitempty"`
	Soul          string                     `json:"soul,omitempty"`
	Agents        string                     `json:"agents,omitempty"`
	Skills        []string                   `json:"skills,omitempty"`
	McpServers    []string                   `json:"mcpServers,omitempty"`
	Package       string                     `json:"package,omitempty"`
	Expose        []v1beta1.ExposePort       `json:"expose,omitempty"`
	ChannelPolicy *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	State         *string                    `json:"state,omitempty"`
}

// TeamBundleRequest is the POST /api/v1/bundles/team payload. Server-side the
// handler validates Team + Leader Worker + each Member Worker via the same
// webhook validator pipeline, then creates resources in dependency order:
// Team -> Leader Worker -> Member Workers -> Admin Human patches. Missing
// Admin Humans are reported as warnings (non-fatal).
type TeamBundleRequest struct {
	Name              string                     `json:"name"`
	Description       string                     `json:"description,omitempty"`
	PeerMentions      *bool                      `json:"peerMentions,omitempty"`
	ChannelPolicy     *v1beta1.ChannelPolicySpec `json:"channelPolicy,omitempty"`
	Heartbeat         *v1beta1.TeamHeartbeatSpec `json:"heartbeat,omitempty"`
	WorkerIdleTimeout string                     `json:"workerIdleTimeout,omitempty"`
	Admins            []string                   `json:"admins,omitempty"`
	Leader            TeamBundleLeader           `json:"leader"`
	Workers           []TeamBundleWorker         `json:"workers,omitempty"`
}

// BundleResultItem records the outcome of a single resource operation inside
// a bundle request. The Bundle endpoints always return 207 Multi-Status so
// partial failures can be surfaced to the caller without losing detail.
type BundleResultItem struct {
	Kind    string `json:"kind"` // team | worker | human | validation
	Name    string `json:"name"`
	Status  string `json:"status"` // created | patched | skipped | deleted | not_found | invalid | error
	Message string `json:"message,omitempty"`
	Warning bool   `json:"warning,omitempty"`
}

// BundleResponse is the body of 207 Multi-Status responses from the bundle
// endpoints.
type BundleResponse struct {
	Items []BundleResultItem `json:"items"`
}
