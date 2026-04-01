package backend

import "context"

// ConsumerRequest holds parameters for creating a gateway consumer.
type ConsumerRequest struct {
	Name       string `json:"name"`
	ConsumerID string `json:"consumer_id,omitempty"`
}

// ConsumerResult holds the result of a consumer operation.
type ConsumerResult struct {
	Name       string `json:"name"`
	ConsumerID string `json:"consumer_id"`
	APIKey     string `json:"api_key"`
	Status     string `json:"status"` // "created" | "exists"
}

// BindRequest holds parameters for binding a consumer to a model API.
type BindRequest struct {
	ConsumerID string `json:"consumer_id"`
	ModelAPIID string `json:"model_api_id"`
	EnvID      string `json:"env_id"`
}

// GatewayBackend defines the interface for AI Gateway consumer management.
// Implementations: HigressBackend (local), APIGBackend (Alibaba Cloud).
type GatewayBackend interface {
	// Name returns the backend identifier (e.g. "higress", "apig").
	Name() string

	// Available reports whether this backend is usable in the current environment.
	Available(ctx context.Context) bool

	// CreateConsumer creates a gateway consumer with key-auth credentials.
	CreateConsumer(ctx context.Context, req ConsumerRequest) (*ConsumerResult, error)

	// BindConsumer binds a consumer to a model API resource.
	BindConsumer(ctx context.Context, req BindRequest) error

	// DeleteConsumer removes a gateway consumer.
	DeleteConsumer(ctx context.Context, consumerID string) error
}
