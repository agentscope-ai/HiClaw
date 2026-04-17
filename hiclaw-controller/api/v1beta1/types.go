// +k8s:deepcopy-gen=package

package v1beta1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	GroupName = "hiclaw.io"
	Version   = "v1beta1"
)

// --- Shared constants ---

// Worker role values (used by WorkerSpec.Role).
const (
	WorkerRoleStandalone = "standalone"
	WorkerRoleTeamLeader = "team_leader"
	WorkerRoleTeamWorker = "team_worker"
)

// Lifecycle state values (used by WorkerSpec.State / ManagerSpec.State).
const (
	StateRunning  = "Running"
	StateSleeping = "Sleeping"
	StateStopped  = "Stopped"
)

// TeamAccess role values (used by TeamAccessEntry.Role).
const (
	TeamAccessRoleAdmin  = "admin"
	TeamAccessRoleMember = "member"
)

// Common label keys maintained by controllers as mirrors of spec fields.
// Worker reconciler syncs these from Worker.spec.teamRef / Worker.spec.role
// so that Team/Manager reconcilers can perform O(1) MatchingLabels queries.
const (
	LabelTeam = "hiclaw.io/team"
	LabelRole = "hiclaw.io/role"
)

// Condition type values (used in Status.Conditions across CRs).
const (
	ConditionReady           = "Ready"
	ConditionProvisioned     = "Provisioned"
	ConditionTeamRefResolved = "TeamRefResolved"

	// Team-specific conditions.
	ConditionLeaderResolved  = "LeaderResolved"
	ConditionTeamRoomReady   = "TeamRoomReady"
	ConditionMembersHealthy  = "MembersHealthy"
	ConditionNoLeader        = "NoLeader"
	ConditionMultipleLeaders = "MultipleLeaders"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Worker represents an AI agent worker in HiClaw. A Worker is a first-class,
// independent resource. Team membership is declared via spec.teamRef +
// spec.role; Team reconciler only observes Worker CRs, never writes them.
type Worker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              WorkerSpec   `json:"spec"`
	Status            WorkerStatus `json:"status,omitempty"`
}

type WorkerSpec struct {
	Model   string `json:"model"`
	Runtime string `json:"runtime,omitempty"` // openclaw | copaw (default: openclaw)
	Image   string `json:"image,omitempty"`   // custom Docker image

	// Role declares this Worker's position in the HiClaw topology.
	// Valid values: "standalone" (default) | "team_leader" | "team_worker".
	// team_leader / team_worker require TeamRef to be non-empty;
	// standalone requires TeamRef to be empty (enforced by webhook).
	Role string `json:"role,omitempty"`

	// TeamRef is the name of the Team CR this Worker belongs to.
	// Soft reference: may be unresolved when the referenced Team does not
	// exist yet; Worker reconciler surfaces the status via a
	// "TeamRefResolved" condition.
	TeamRef string `json:"teamRef,omitempty"`

	Identity      string             `json:"identity,omitempty"`
	Soul          string             `json:"soul,omitempty"`
	Agents        string             `json:"agents,omitempty"`
	Skills        []string           `json:"skills,omitempty"`
	McpServers    []string           `json:"mcpServers,omitempty"`
	Package       string             `json:"package,omitempty"` // file://, http(s)://, or nacos:// URI
	Expose        []ExposePort       `json:"expose,omitempty"`  // ports to expose via Higress gateway
	ChannelPolicy *ChannelPolicySpec `json:"channelPolicy,omitempty"`

	// State is the desired lifecycle state of the worker.
	// Valid values: "Running" (default), "Sleeping", "Stopped".
	State *string `json:"state,omitempty"`
}

// DesiredState returns the effective desired state, defaulting to "Running".
func (s WorkerSpec) DesiredState() string {
	if s.State != nil && *s.State != "" {
		return *s.State
	}
	return StateRunning
}

// EffectiveRole returns the effective role, defaulting to "standalone".
func (s WorkerSpec) EffectiveRole() string {
	if s.Role == "" {
		return WorkerRoleStandalone
	}
	return s.Role
}

// ExposePort defines a container port to expose via the Higress gateway.
type ExposePort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"` // http (default) | grpc
}

// ChannelPolicySpec defines additive/subtractive overrides on top of default
// communication policies. Values are Matrix user IDs (@user:domain) or
// short usernames (auto-resolved to full IDs by config generation scripts).
type ChannelPolicySpec struct {
	GroupAllowExtra []string `json:"groupAllowExtra,omitempty"`
	GroupDenyExtra  []string `json:"groupDenyExtra,omitempty"`
	DmAllowExtra    []string `json:"dmAllowExtra,omitempty"`
	DmDenyExtra     []string `json:"dmDenyExtra,omitempty"`
}

type WorkerStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	Phase              string `json:"phase,omitempty"` // Pending/Running/Sleeping/Failed
	MatrixUserID       string `json:"matrixUserID,omitempty"`
	RoomID             string `json:"roomID,omitempty"`
	ContainerState     string `json:"containerState,omitempty"`
	LastHeartbeat      string `json:"lastHeartbeat,omitempty"`
	Message            string `json:"message,omitempty"`
	ExposedPorts       []ExposedPortStatus `json:"exposedPorts,omitempty"`

	// TeamRef is the observed value of spec.teamRef after reconcile. Used by
	// WorkerReconciler to detect cross-team migration (spec.teamRef !=
	// status.teamRef triggers leave-old / join-new actions).
	TeamRef string `json:"teamRef,omitempty"`

	// Conditions exposes the latest available observations of the Worker
	// state. Types: Ready, Provisioned, TeamRefResolved.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ExposedPortStatus records a port that has been exposed via Higress.
type ExposedPortStatus struct {
	Port   int    `json:"port"`
	Domain string `json:"domain"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type WorkerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Worker `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Team represents a group of Workers with a shared Leader, Team Room, and
// coordination settings. Team is a pure coordination CR: it does not embed
// any Worker specs and does not create/modify/delete Worker CRs. Team
// membership is observed via list of Worker CRs with spec.teamRef matching
// this Team's name; Team admins are observed via list of Human CRs with
// spec.teamAccess entries naming this Team with role=admin.
type Team struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TeamSpec   `json:"spec"`
	Status            TeamStatus `json:"status,omitempty"`
}

type TeamSpec struct {
	Description       string              `json:"description,omitempty"`
	PeerMentions      *bool               `json:"peerMentions,omitempty"` // default true
	ChannelPolicy     *ChannelPolicySpec  `json:"channelPolicy,omitempty"`
	Heartbeat         *TeamHeartbeatSpec  `json:"heartbeat,omitempty"`
	WorkerIdleTimeout string              `json:"workerIdleTimeout,omitempty"`
}

// TeamHeartbeatSpec configures the Leader's heartbeat routine.
type TeamHeartbeatSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Every   string `json:"every,omitempty"`
}

type TeamStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	Phase              string `json:"phase,omitempty"` // Pending | Active | Degraded | Failed
	TeamRoomID         string `json:"teamRoomID,omitempty"`
	LeaderDMRoomID     string `json:"leaderDMRoomID,omitempty"`

	// Leader is the observed team_leader Worker. nil when no leader is found
	// or multiple leaders are detected (see Conditions).
	Leader *TeamLeaderObservation `json:"leader,omitempty"`

	// Members is the observed list of team_worker Workers in this Team.
	Members []TeamMemberObservation `json:"members,omitempty"`

	// Admins is the observed list of Humans with teamAccess[].role=admin
	// targeting this Team.
	Admins []TeamAdminObservation `json:"admins,omitempty"`

	TotalMembers int `json:"totalMembers,omitempty"`
	ReadyMembers int `json:"readyMembers,omitempty"`

	Message    string             `json:"message,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TeamLeaderObservation is the observed state of a team's leader Worker.
type TeamLeaderObservation struct {
	Name         string `json:"name"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
	Ready        bool   `json:"ready"`
}

// TeamMemberObservation is the observed state of a team_worker Worker.
type TeamMemberObservation struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
	Ready        bool   `json:"ready"`
}

// TeamAdminObservation is the observed relationship between a Human with
// teamAccess role=admin and this Team.
type TeamAdminObservation struct {
	HumanName    string `json:"humanName"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type TeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Team `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Human represents a real human user with configurable access to Teams
// and Workers. Access is declared via spec.teamAccess (per-Team role) and
// spec.workerAccess (direct Worker access list). SuperAdmin grants global
// access to all Teams and Workers.
type Human struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              HumanSpec   `json:"spec"`
	Status            HumanStatus `json:"status,omitempty"`
}

type HumanSpec struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
	Note        string `json:"note,omitempty"`

	// SuperAdmin grants this Human access to every Team and every Worker
	// (equivalent of the former PermissionLevel=1). When true, TeamAccess
	// and WorkerAccess must be empty (enforced by webhook).
	SuperAdmin bool `json:"superAdmin,omitempty"`

	// TeamAccess declares Team-level access: for each entry, this Human is
	// either the admin (role=admin) or a member participant (role=member)
	// of the named Team. Team reconciler observes these entries to compute
	// Team.status.admins and Team Room membership.
	TeamAccess []TeamAccessEntry `json:"teamAccess,omitempty"`

	// WorkerAccess declares direct Worker-level access (the former L3
	// access model). Each entry is a Worker name.
	WorkerAccess []string `json:"workerAccess,omitempty"`
}

// TeamAccessEntry declares this Human's role within a specific Team.
type TeamAccessEntry struct {
	Team string `json:"team"`
	Role string `json:"role"` // admin | member
}

type HumanStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	Phase              string `json:"phase,omitempty"` // Pending/Active/Failed
	MatrixUserID       string `json:"matrixUserID,omitempty"`
	InitialPassword    string `json:"initialPassword,omitempty"` // Set on creation, shown once
	Rooms              []string           `json:"rooms,omitempty"`
	EmailSent          bool               `json:"emailSent,omitempty"`
	Message            string             `json:"message,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type HumanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Human `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Manager represents the HiClaw Manager Agent — the coordinator that receives
// natural-language instructions from Admin and orchestrates Workers/Teams via
// the hiclaw CLI / Controller REST API.
type Manager struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ManagerSpec   `json:"spec"`
	Status            ManagerStatus `json:"status,omitempty"`
}

type ManagerSpec struct {
	Model      string        `json:"model"`
	Runtime    string        `json:"runtime,omitempty"`    // openclaw | copaw (default: openclaw)
	Image      string        `json:"image,omitempty"`      // custom Docker image
	Soul       string        `json:"soul,omitempty"`       // custom SOUL.md content
	Agents     string        `json:"agents,omitempty"`     // custom AGENTS.md content
	Skills     []string      `json:"skills,omitempty"`     // on-demand skills to enable
	McpServers []string      `json:"mcpServers,omitempty"` // MCP servers to authorize via Gateway
	Package    string        `json:"package,omitempty"`    // file://, http(s)://, or nacos:// URI
	Config     ManagerConfig `json:"config,omitempty"`

	// State is the desired lifecycle state of the manager.
	// Valid values: "Running" (default), "Sleeping", "Stopped".
	State *string `json:"state,omitempty"`
}

// DesiredState returns the effective desired state, defaulting to "Running".
func (s ManagerSpec) DesiredState() string {
	if s.State != nil && *s.State != "" {
		return *s.State
	}
	return StateRunning
}

type ManagerConfig struct {
	HeartbeatInterval string `json:"heartbeatInterval,omitempty"` // default: 15m
	WorkerIdleTimeout string `json:"workerIdleTimeout,omitempty"` // default: 720m
	NotifyChannel     string `json:"notifyChannel,omitempty"`     // default: admin-dm
}

type ManagerStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	Phase              string `json:"phase,omitempty"` // Pending/Running/Updating/Failed
	MatrixUserID       string `json:"matrixUserID,omitempty"`
	RoomID             string `json:"roomID,omitempty"` // Admin DM room
	ContainerState     string `json:"containerState,omitempty"`
	Version            string `json:"version,omitempty"`
	Message            string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ManagerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Manager `json:"items"`
}
