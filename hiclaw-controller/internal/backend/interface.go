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
	// AuthToken is the pre-issued SA token for Docker/SAE backends.
	// AuthAudience is the projected token audience (K8s backend only; defaults to "hiclaw-controller").
	ServiceAccountName string `json:"-"`
	AuthToken          string `json:"-"`
	AuthAudience       string `json:"-"`

	// Resources overrides default resource limits for this container.
	// nil = use backend defaults (e.g. K8sConfig.WorkerCPU/WorkerMemory).
	Resources *ResourceRequirements `json:"-"`

	// Labels are additional K8s labels merged into the Pod metadata.
	// Backend-specific defaults (app, hiclaw.io/worker, hiclaw.io/runtime)
	// are always set; these labels are additive.
	Labels map[string]string `json:"-"`
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
// Implementations: DockerBackend (local), SAEBackend (Alibaba Cloud), future K8s/ACS.
type WorkerBackend interface {
	// Name returns the backend identifier (e.g. "docker", "sae").
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
