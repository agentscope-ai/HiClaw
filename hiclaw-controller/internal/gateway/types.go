package gateway

// Config holds connection parameters for an AI gateway.
type Config struct {
	ConsoleURL    string // gateway console API, e.g. http://127.0.0.1:8001
	AdminUser     string // console login username
	AdminPassword string // console login password
	PilotURL      string // istiod debug API, e.g. http://127.0.0.1:15014 (embedded mode only)
}

// ConsumerRequest describes a gateway consumer to create.
type ConsumerRequest struct {
	Name          string // consumer name, e.g. "worker-alice"
	CredentialKey string // API key for key-auth (self-hosted Higress)
	ConsumerID    string // platform-specific consumer ID (cloud APIG, optional)
}

// ConsumerResult holds the result of an EnsureConsumer call.
type ConsumerResult struct {
	Status     string // "created" or "exists"
	APIKey     string // the active API key
	ConsumerID string // platform-specific consumer ID (cloud only)
}

// AIRoute represents an AI route in the gateway.
type AIRoute struct {
	Name             string   `json:"name"`
	AllowedConsumers []string `json:"allowedConsumers,omitempty"`
}

// MCPServerAuth describes MCP server authorization state.
type MCPServerAuth struct {
	Name             string   `json:"name"`
	AllowedConsumers []string `json:"allowedConsumers,omitempty"`
}

// PortExposeRequest describes a port to expose through the gateway.
type PortExposeRequest struct {
	WorkerName  string // worker identifier
	ServiceHost string // DNS hostname of the service
	Port        int    // port number to expose
	Domain      string // domain name to bind
}

// AIProviderRequest describes an LLM provider to register in the gateway.
type AIProviderRequest struct {
	Name     string // provider name, e.g. "qwen"
	Type     string // provider type, e.g. "qwen", "openai"
	Tokens   []string
	Protocol string            // e.g. "openai/v1"
	Raw      map[string]interface{} // provider-specific raw config
}

// AIRouteRequest describes an AI route to create.
type AIRouteRequest struct {
	Name             string
	PathPrefix       string   // e.g. "/v1"
	Provider         string   // upstream provider name
	AllowedConsumers []string // consumers authorized for this route
}
