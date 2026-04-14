package backend

import (
	"context"
	"fmt"
)

// DefaultContainerPrefix is the default prefix for worker container/app names.
const DefaultContainerPrefix = "hiclaw-worker-"

// Registry holds all available backends and provides auto-detection.
type Registry struct {
	workerBackends  []WorkerBackend
	gatewayBackends []GatewayBackend
}

// NewRegistry creates a Registry with the given backends.
func NewRegistry(workers []WorkerBackend, gateways []GatewayBackend) *Registry {
	return &Registry{
		workerBackends:  workers,
		gatewayBackends: gateways,
	}
}

// DetectWorkerBackend returns the first available worker backend.
// Priority is determined by registration order (set in buildBackends):
//  1. Docker backend (socket available)
//  2. K8s backend (incluster mode)
//  3. nil
func (r *Registry) DetectWorkerBackend(ctx context.Context) WorkerBackend {
	for _, b := range r.workerBackends {
		if b.Available(ctx) {
			return b
		}
	}
	return nil
}

// GetWorkerBackend returns a specific worker backend by name, or auto-detects if name is empty.
func (r *Registry) GetWorkerBackend(ctx context.Context, name string) (WorkerBackend, error) {
	if name == "" {
		b := r.DetectWorkerBackend(ctx)
		if b == nil {
			return nil, fmt.Errorf("no worker backend available")
		}
		return b, nil
	}
	for _, b := range r.workerBackends {
		if b.Name() == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("unknown worker backend: %q", name)
}

// DetectGatewayBackend returns the first available gateway backend.
func (r *Registry) DetectGatewayBackend(ctx context.Context) GatewayBackend {
	for _, b := range r.gatewayBackends {
		if b.Available(ctx) {
			return b
		}
	}
	return nil
}

// GetGatewayBackend returns a specific gateway backend by name, or auto-detects if name is empty.
func (r *Registry) GetGatewayBackend(ctx context.Context, name string) (GatewayBackend, error) {
	if name == "" {
		b := r.DetectGatewayBackend(ctx)
		if b == nil {
			return nil, fmt.Errorf("no gateway backend available")
		}
		return b, nil
	}
	for _, b := range r.gatewayBackends {
		if b.Name() == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("unknown gateway backend: %q", name)
}
