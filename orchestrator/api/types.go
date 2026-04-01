package api

import "github.com/alibaba/hiclaw/orchestrator/backend"

// --- Worker API types ---

// CreateWorkerRequest is the JSON body for POST /workers.
type CreateWorkerRequest struct {
	Name       string            `json:"name"`
	Image      string            `json:"image,omitempty"`
	Runtime    string            `json:"runtime,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Network    string            `json:"network,omitempty"`
	ExtraHosts []string          `json:"extra_hosts,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Backend    string            `json:"backend,omitempty"` // override auto-detection
}

// WorkerResponse is the JSON response for worker operations.
type WorkerResponse struct {
	Name            string                `json:"name"`
	Backend         string                `json:"backend"`
	DeploymentMode  string                `json:"deployment_mode"`
	Status          backend.WorkerStatus  `json:"status"`
	ContainerID     string                `json:"container_id,omitempty"`
	AppID           string                `json:"app_id,omitempty"`
	RawStatus       string                `json:"raw_status,omitempty"`
	APIKey          string                `json:"api_key,omitempty"`
	ConsoleHostPort string                `json:"console_host_port,omitempty"`
}

// WorkerListResponse is the JSON response for GET /workers.
type WorkerListResponse struct {
	Workers []WorkerResponse `json:"workers"`
}

// --- Gateway API types ---

// CreateConsumerRequest is the JSON body for POST /gateway/consumers.
type CreateConsumerRequest struct {
	Name string `json:"name"`
}

// ConsumerResponse is the JSON response for consumer operations.
type ConsumerResponse struct {
	Name       string `json:"name"`
	ConsumerID string `json:"consumer_id"`
	APIKey     string `json:"api_key,omitempty"`
	Status     string `json:"status"`
}

// BindConsumerRequest is the JSON body for POST /gateway/consumers/{id}/bind.
type BindConsumerRequest struct {
	ModelAPIID string `json:"model_api_id"`
	EnvID      string `json:"env_id"`
}
