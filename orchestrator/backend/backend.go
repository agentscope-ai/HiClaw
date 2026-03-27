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

// CreateRequest holds parameters for creating a worker container/instance.
type CreateRequest struct {
	Name       string            `json:"name"`
	Image      string            `json:"image,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Runtime    string            `json:"runtime,omitempty"` // "openclaw" | "copaw"
	Network    string            `json:"network,omitempty"`
	ExtraHosts []string          `json:"extra_hosts,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`

	// Credential injection — set by handler, backends that need it will inject into env.
	OrchestratorURL string `json:"-"`
	WorkerAPIKey    string `json:"-"`
}

// WorkerResult holds the result of a worker operation.
type WorkerResult struct {
	Name        string       `json:"name"`
	Backend     string       `json:"backend"`
	Status      WorkerStatus `json:"status"`
	ContainerID string       `json:"container_id,omitempty"`
	AppID       string       `json:"app_id,omitempty"`
	RawStatus   string       `json:"raw_status,omitempty"`
}

// WorkerBackend defines the interface for worker lifecycle operations.
// Implementations: DockerBackend (local), SAEBackend (Alibaba Cloud), future K8s/ACS.
type WorkerBackend interface {
	// Name returns the backend identifier (e.g. "docker", "sae").
	Name() string

	// Available reports whether this backend is usable in the current environment.
	Available(ctx context.Context) bool

	// NeedsCredentialInjection reports whether this backend requires
	// orchestrator-mediated credentials (API key + URL) injected into worker env.
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
