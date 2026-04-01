package main

import (
	"os"
	"strconv"

	"github.com/alibaba/hiclaw/orchestrator/backend"
	"github.com/alibaba/hiclaw/orchestrator/credentials"
)

// Config holds all configuration for the orchestrator service.
type Config struct {
	// ListenAddr is the address to listen on (default ":2375").
	ListenAddr string
	// SocketPath is the Docker socket path (default "/var/run/docker.sock").
	SocketPath string
	// ContainerPrefix is the required prefix for worker container names (default "hiclaw-worker-").
	ContainerPrefix string

	// Auth
	ManagerAPIKey string // HICLAW_ORCHESTRATOR_API_KEY

	// SAE Backend
	Region              string
	SAENamespaceID      string
	SAEWorkerImage      string
	SAECopawWorkerImage string
	SAEVPCID            string
	SAEVSwitchID        string
	SAESecurityGroupID  string
	SAEWorkerCPU        int32
	SAEWorkerMemory     int32

	// APIG Gateway
	GWGatewayID  string
	GWModelAPIID string
	GWEnvID      string

	// STS
	OSSBucket       string
	STSRoleArn      string
	OIDCProviderArn string
	OIDCTokenFile   string

	// Orchestrator URL (advertised to workers for STS refresh)
	OrchestratorURL string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() *Config {
	return &Config{
		ListenAddr:      envOrDefault("HICLAW_PROXY_LISTEN", ":2375"),
		SocketPath:      envOrDefault("HICLAW_PROXY_SOCKET", "/var/run/docker.sock"),
		ContainerPrefix: envOrDefault("HICLAW_PROXY_CONTAINER_PREFIX", "hiclaw-worker-"),

		ManagerAPIKey: os.Getenv("HICLAW_ORCHESTRATOR_API_KEY"),

		Region:              envOrDefault("HICLAW_REGION", "cn-hangzhou"),
		SAENamespaceID:      os.Getenv("HICLAW_SAE_NAMESPACE_ID"),
		SAEWorkerImage:      os.Getenv("HICLAW_SAE_WORKER_IMAGE"),
		SAECopawWorkerImage: os.Getenv("HICLAW_SAE_COPAW_WORKER_IMAGE"),
		SAEVPCID:            os.Getenv("HICLAW_SAE_VPC_ID"),
		SAEVSwitchID:        os.Getenv("HICLAW_SAE_VSWITCH_ID"),
		SAESecurityGroupID:  os.Getenv("HICLAW_SAE_SECURITY_GROUP_ID"),
		SAEWorkerCPU:        int32(envOrDefaultInt("HICLAW_SAE_WORKER_CPU", 1000)),
		SAEWorkerMemory:     int32(envOrDefaultInt("HICLAW_SAE_WORKER_MEMORY", 2048)),

		GWGatewayID:  os.Getenv("HICLAW_GW_GATEWAY_ID"),
		GWModelAPIID: os.Getenv("HICLAW_GW_MODEL_API_ID"),
		GWEnvID:      os.Getenv("HICLAW_GW_ENV_ID"),

		OSSBucket:       os.Getenv("HICLAW_OSS_BUCKET"),
		STSRoleArn:      os.Getenv("ALIBABA_CLOUD_ROLE_ARN"),
		OIDCProviderArn: os.Getenv("ALIBABA_CLOUD_OIDC_PROVIDER_ARN"),
		OIDCTokenFile:   os.Getenv("ALIBABA_CLOUD_OIDC_TOKEN_FILE"),

		OrchestratorURL: os.Getenv("HICLAW_ORCHESTRATOR_URL"),
	}
}

func (c *Config) DockerConfig() backend.DockerConfig {
	return backend.DockerConfig{
		SocketPath:       c.SocketPath,
		WorkerImage:      envOrDefault("HICLAW_WORKER_IMAGE", "hiclaw/worker-agent:latest"),
		CopawWorkerImage: envOrDefault("HICLAW_COPAW_WORKER_IMAGE", "hiclaw/copaw-worker:latest"),
		DefaultNetwork:   envOrDefault("HICLAW_DOCKER_NETWORK", "hiclaw-net"),
	}
}

func (c *Config) SAEConfig() backend.SAEConfig {
	return backend.SAEConfig{
		Region:           c.Region,
		NamespaceID:      c.SAENamespaceID,
		WorkerImage:      c.SAEWorkerImage,
		CopawWorkerImage: c.SAECopawWorkerImage,
		VPCID:            c.SAEVPCID,
		VSwitchID:        c.SAEVSwitchID,
		SecurityGroupID:  c.SAESecurityGroupID,
		CPU:              c.SAEWorkerCPU,
		Memory:           c.SAEWorkerMemory,
	}
}

func (c *Config) APIGConfig() backend.APIGConfig {
	return backend.APIGConfig{
		Region:     c.Region,
		GatewayID:  c.GWGatewayID,
		ModelAPIID: c.GWModelAPIID,
		EnvID:      c.GWEnvID,
	}
}

func (c *Config) STSConfig() credentials.STSConfig {
	return credentials.STSConfig{
		Region:          c.Region,
		RoleArn:         c.STSRoleArn,
		OIDCProviderArn: c.OIDCProviderArn,
		OIDCTokenFile:   c.OIDCTokenFile,
		OSSBucket:       c.OSSBucket,
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envOrDefaultInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}
