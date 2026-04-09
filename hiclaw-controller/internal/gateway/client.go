package gateway

import "context"

// Client abstracts AI gateway operations (consumer management, route authorization).
// Implementations: HigressClient (self-hosted), future APigClient (Alibaba Cloud).
type Client interface {
	// EnsureConsumer creates a consumer or returns existing.
	// Idempotent: repeated calls with the same name are safe.
	EnsureConsumer(ctx context.Context, req ConsumerRequest) (*ConsumerResult, error)

	// DeleteConsumer removes a consumer by name. No-op if not found.
	DeleteConsumer(ctx context.Context, name string) error

	// AuthorizeAIRoutes adds the consumer to all AI routes' allowedConsumers.
	// Handles 409 conflict with retry logic.
	AuthorizeAIRoutes(ctx context.Context, consumerName string) error

	// DeauthorizeAIRoutes removes the consumer from all AI routes' allowedConsumers.
	DeauthorizeAIRoutes(ctx context.Context, consumerName string) error

	// AuthorizeMCPServers adds the consumer to the specified MCP servers' allowedConsumers.
	// If mcpServers is empty, authorizes all existing MCP servers.
	// Returns the list of MCP server names that were successfully authorized.
	AuthorizeMCPServers(ctx context.Context, consumerName string, mcpServers []string) ([]string, error)

	// DeauthorizeMCPServers removes the consumer from MCP servers' allowedConsumers.
	DeauthorizeMCPServers(ctx context.Context, consumerName string, mcpServers []string) error

	// ExposePort creates gateway resources to expose a worker port.
	ExposePort(ctx context.Context, req PortExposeRequest) error

	// UnexposePort removes gateway resources for a worker port.
	UnexposePort(ctx context.Context, req PortExposeRequest) error

	// --- Infrastructure initialization (used by Initializer) ---

	// EnsureServiceSource registers a DNS-type service source.
	EnsureServiceSource(ctx context.Context, name, domain string, port int) error

	// EnsureRoute creates a route mapping domains to a backend service.
	EnsureRoute(ctx context.Context, name string, domains []string, serviceName string, port int) error

	// DeleteRoute removes a route by name. No-op if not found.
	DeleteRoute(ctx context.Context, name string) error

	// EnsureAIProvider creates an LLM provider configuration.
	EnsureAIProvider(ctx context.Context, req AIProviderRequest) error

	// EnsureAIRoute creates an AI route with consumer auth.
	EnsureAIRoute(ctx context.Context, req AIRouteRequest) error

	// Healthy returns nil if the gateway console is reachable and authenticated.
	Healthy(ctx context.Context) error
}
