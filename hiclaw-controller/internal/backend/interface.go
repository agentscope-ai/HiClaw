package backend

import (
	"context"
	"errors"
)

// Typed errors for backend operations.
var (
	ErrConflict = errors.New("resource already exists")
	ErrNotFound = errors.New("resource not found")
)

// WorkerStatus represents normalized worker status across backends.
type WorkerStatus string

const (
	StatusRunning  WorkerStatus = "running"
	StatusReady    WorkerStatus = "ready"
	StatusStopped  WorkerStatus = "stopped"
	StatusStarting WorkerStatus = "starting"
	StatusNotFound WorkerStatus = "not_found"
	StatusUnknown  WorkerStatus = "unknown"
)

// Supported worker runtimes.
const (
	RuntimeOpenClaw = "openclaw"
	RuntimeCopaw    = "copaw"
)

// ValidRuntime reports whether r is a recognized runtime value.
// An empty string is valid — backends resolve it to the default image.
func ValidRuntime(r string) bool {
	return r == "" || r == RuntimeOpenClaw || r == RuntimeCopaw
}

// ResourceRequirements specifies CPU/memory requests and limits for a container.
// When nil on CreateRequest, backends use their configured defaults.
type ResourceRequirements struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// VolumeMount describes a host-to-container bind mount (Docker backend only;
// K8s backend ignores this — use standard Pod volume specs instead).
type VolumeMount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// PortMapping describes a host-to-container port binding (Docker backend only).
type PortMapping struct {
	HostIP        string // e.g. "127.0.0.1"; empty = all interfaces
	HostPort      string
	ContainerPort string
	Protocol      string // "tcp" (default) or "udp"
}

// CreateRequest holds parameters for creating a worker container/instance.
type CreateRequest struct {
	Name       string            `json:"name"`
	Image      string            `json:"image,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Runtime    string            `json:"runtime,omitempty"` // "openclaw" | "copaw"
	Network    string            `json:"network,omitempty"`
	ExtraHosts []string          `json:"extra_hosts,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`

	// Controller URL advertised to worker for callbacks.
	ControllerURL string `json:"-"`

	// SA-based auth — ServiceAccountName is set on K8s Pods (projected token).
	// AuthToken is the pre-issued SA token for Docker backend.
	// AuthAudience is the projected token audience (K8s backend only; defaults to "hiclaw-controller").
	ServiceAccountName string `json:"-"`
	AuthToken          string `json:"-"`
	AuthAudience       string `json:"-"`

	// Resources overrides default resource limits for this container.
	// nil = use backend defaults (e.g. K8sConfig.WorkerCPU/WorkerMemory).
	Resources *ResourceRequirements `json:"-"`

	// NamePrefix overrides the backend's default container/pod name prefix.
	// When set, pod name = NamePrefix + Name instead of containerPrefix + Name.
	NamePrefix string `json:"-"`

	// ContainerName overrides the computed container/pod name entirely.
	// When set, NamePrefix and containerPrefix are ignored for naming.
	ContainerName string `json:"-"`

	// Labels are additional K8s labels merged into the Pod metadata.
	// If "app" is present, it overrides the default "hiclaw-worker".
	// If "hiclaw.io/worker" should be omitted (e.g. for Manager pods),
	// set an alternative identity label (e.g. "hiclaw.io/manager").
	Labels map[string]string `json:"-"`

	// Volumes are host bind mounts (Docker backend only; ignored by K8s).
	Volumes []VolumeMount `json:"-"`

	// NetworkAliases are DNS names added to the container within the Docker network.
	NetworkAliases []string `json:"-"`

	// Ports are additional host-to-container port mappings (Docker backend only).
	Ports []PortMapping `json:"-"`

	// RestartPolicy for Docker containers (e.g. "unless-stopped", "always").
	// Empty means backend default (no restart).
	RestartPolicy string `json:"-"`
}

// Deployment modes returned by backends.
const (
	DeployLocal = "local"
	DeployCloud = "cloud"
)

// WorkerResult holds the result of a worker operation.
type WorkerResult struct {
	Name            string       `json:"name"`
	Backend         string       `json:"backend"`
	DeploymentMode  string       `json:"deployment_mode"`
	Status          WorkerStatus `json:"status"`
	ContainerID     string       `json:"container_id,omitempty"`
	AppID           string       `json:"app_id,omitempty"`
	RawStatus       string       `json:"raw_status,omitempty"`
	ConsoleHostPort string       `json:"console_host_port,omitempty"`
}

// WorkerBackend defines the interface for worker lifecycle operations.
// Implementations: DockerBackend (local), KubernetesBackend (incluster).
type WorkerBackend interface {
	// Name returns the backend identifier (e.g. "docker", "k8s").
	Name() string

	// DeploymentMode returns the user-facing deployment mode ("local" or "cloud").
	DeploymentMode() string

	// Available reports whether this backend is usable in the current environment.
	Available(ctx context.Context) bool

	// NeedsCredentialInjection reports whether this backend requires
	// controller-mediated credentials (API key + URL) injected into worker env.
	NeedsCredentialInjection() bool

	// Create creates and starts a new worker.
	Create(ctx context.Context, req CreateRequest) (*WorkerResult, error)

	// Delete removes a worker.
	Delete(ctx context.Context, name string) error

	// Start starts a stopped worker.
	Start(ctx context.Context, name string) error

	// Stop stops a running worker.
	Stop(ctx context.Context, name string) error

	// Status returns the current status of a worker.
	Status(ctx context.Context, name string) (*WorkerResult, error)

	// List returns all workers managed by this backend.
	List(ctx context.Context) ([]WorkerResult, error)
}
