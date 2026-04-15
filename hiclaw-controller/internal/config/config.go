package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hiclaw/hiclaw-controller/internal/agentconfig"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/credentials"
	"github.com/hiclaw/hiclaw-controller/internal/gateway"
	"github.com/hiclaw/hiclaw-controller/internal/matrix"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
)

type Config struct {
	// Controller core
	KubeMode  string // "embedded" or "incluster"
	DataDir   string
	HTTPAddr  string
	ConfigDir string
	CRDDir    string
	SkillsDir string

	// Docker proxy (embedded mode only)
	SocketPath      string
	ContainerPrefix string

	// Auth
	AuthAudience string // SA token audience for TokenReview

	// Higress
	HigressBaseURL       string
	HigressCookieFile    string
	HigressAdminUser     string
	HigressAdminPassword string

	// Worker backend selection
	WorkerBackend string

	// Region (used by APIG, STS, etc.)
	Region string

	// APIG Gateway
	GWGatewayID  string
	GWModelAPIID string
	GWEnvID      string

	// STS
	OSSBucket       string
	STSRoleArn      string
	OIDCProviderArn string
	OIDCTokenFile   string

	// Kubernetes Backend
	K8sNamespace    string
	K8sWorkerCPU    string
	K8sWorkerMemory string

	// Manager deployment (Initializer creates the Manager CR if enabled)
	ManagerEnabled          bool
	ManagerModel            string
	ManagerRuntime          string
	ManagerImage            string
	K8sManagerCPURequest    string
	K8sManagerMemoryRequest string
	K8sManagerCPU           string
	K8sManagerMemory        string

	// Controller URL (advertised to workers for STS refresh etc.)
	ControllerURL string

	// Embedded-mode Manager Agent container mounts (host paths, read from env)
	ManagerWorkspaceDir string // e.g. ~/hiclaw-manager — mounted as /root/manager-workspace
	HostShareDir        string // e.g. ~/ — mounted as /host-share
	ManagerConsolePort  string // host port for manager console (default: 18888)

	// Pre-generated Manager secrets (from install script env)
	ManagerPassword   string // Matrix password for manager user
	ManagerGatewayKey string // Gateway API key for manager consumer

	// Matrix server
	MatrixServerURL         string
	MatrixDomain            string
	MatrixRegistrationToken string
	MatrixAdminUser         string
	MatrixAdminPassword     string
	MatrixE2EE              bool

	// Object storage (embedded MinIO)
	OSSStoragePrefix string

	// AI model
	DefaultModel       string
	EmbeddingModel     string
	Runtime            string
	ModelContextWindow int
	ModelMaxTokens     int

	// LLM provider (for Gateway initialization)
	LLMProvider   string
	LLMAPIKey     string
	OpenAIBaseURL string // HICLAW_OPENAI_BASE_URL — custom base URL for openai-compat providers

	// Element Web URL (for Gateway route initialization)
	ElementWebURL string

	// CMS observability
	CMSTracesEnabled  bool
	CMSMetricsEnabled bool
	CMSEndpoint       string
	CMSLicenseKey     string
	CMSProject        string
	CMSWorkspace      string

	// Pre-resolved worker environment defaults (passed to worker containers)
	WorkerEnv WorkerEnvDefaults
}

// WorkerEnvDefaults holds environment variable defaults injected into worker containers.
// All values are resolved once at config load time from the controller's own environment.
type WorkerEnvDefaults struct {
	MatrixDomain  string
	FSEndpoint    string
	FSBucket      string
	StoragePrefix string
	ControllerURL string
	AIGatewayURL  string
	MatrixURL     string
	AdminUser     string
	Runtime       string // "docker" for embedded, "k8s" for incluster
}

type managerSpecEnv struct {
	Model     string               `json:"model"`
	Runtime   string               `json:"runtime"`
	Image     string               `json:"image"`
	Resources managerSpecResources `json:"resources"`
}

type managerSpecResources struct {
	Requests managerSpecResourceValues `json:"requests"`
	Limits   managerSpecResourceValues `json:"limits"`
}

type managerSpecResourceValues struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

func LoadConfig() *Config {
	dataDir := envOrDefault("HICLAW_DATA_DIR", "/data/hiclaw-controller")
	if !filepath.IsAbs(dataDir) {
		if wd, err := os.Getwd(); err == nil {
			dataDir = filepath.Join(wd, dataDir)
		}
	}

	cfg := &Config{
		KubeMode:  envOrDefault("HICLAW_KUBE_MODE", "embedded"),
		DataDir:   dataDir,
		HTTPAddr:  envOrDefault("HICLAW_HTTP_ADDR", ":8090"),
		ConfigDir: envOrDefault("HICLAW_CONFIG_DIR", "/root/hiclaw-fs/hiclaw-config"),
		CRDDir:    envOrDefault("HICLAW_CRD_DIR", "/opt/hiclaw/config/crd"),
		SkillsDir: envOrDefault("HICLAW_SKILLS_DIR", "/opt/hiclaw/agent/skills"),

		SocketPath:      envOrDefault("HICLAW_PROXY_SOCKET", "/var/run/docker.sock"),
		ContainerPrefix: envOrDefault("HICLAW_PROXY_CONTAINER_PREFIX", "hiclaw-worker-"),

		AuthAudience: envOrDefault("HICLAW_AUTH_AUDIENCE", "hiclaw-controller"),

		HigressBaseURL:       envOrDefault("HICLAW_AI_GATEWAY_ADMIN_URL", "http://127.0.0.1:8001"),
		HigressCookieFile:    os.Getenv("HIGRESS_COOKIE_FILE"),
		HigressAdminUser:     firstNonEmpty(os.Getenv("HICLAW_HIGRESS_ADMIN_USER"), envOrDefault("HICLAW_ADMIN_USER", "admin")),
		HigressAdminPassword: firstNonEmpty(os.Getenv("HICLAW_HIGRESS_ADMIN_PASSWORD"), envOrDefault("HICLAW_ADMIN_PASSWORD", "admin")),

		WorkerBackend: firstNonEmpty(
			os.Getenv("HICLAW_WORKER_BACKEND"),
			os.Getenv("HICLAW_ALIYUN_WORKER_BACKEND"),
		),

		Region: envOrDefault("HICLAW_REGION", "cn-hangzhou"),

		GWGatewayID:  os.Getenv("HICLAW_GW_GATEWAY_ID"),
		GWModelAPIID: os.Getenv("HICLAW_GW_MODEL_API_ID"),
		GWEnvID:      os.Getenv("HICLAW_GW_ENV_ID"),

		OSSBucket:       envOrDefault("HICLAW_FS_BUCKET", "hiclaw-storage"),
		STSRoleArn:      os.Getenv("ALIBABA_CLOUD_ROLE_ARN"),
		OIDCProviderArn: os.Getenv("ALIBABA_CLOUD_OIDC_PROVIDER_ARN"),
		OIDCTokenFile:   os.Getenv("ALIBABA_CLOUD_OIDC_TOKEN_FILE"),

		K8sNamespace:    os.Getenv("HICLAW_K8S_NAMESPACE"),
		K8sWorkerCPU:    envOrDefault("HICLAW_K8S_WORKER_CPU", "1000m"),
		K8sWorkerMemory: envOrDefault("HICLAW_K8S_WORKER_MEMORY", "2Gi"),

		ManagerEnabled:          envOrDefault("HICLAW_MANAGER_ENABLED", "true") == "true",
		ManagerModel:            firstNonEmpty(os.Getenv("HICLAW_MANAGER_MODEL"), envOrDefault("HICLAW_DEFAULT_MODEL", "qwen3.5-plus")),
		ManagerRuntime:          envOrDefault("HICLAW_MANAGER_RUNTIME", "openclaw"),
		ManagerImage:            os.Getenv("HICLAW_MANAGER_IMAGE"),
		K8sManagerCPURequest:    envOrDefault("HICLAW_K8S_MANAGER_CPU_REQUEST", "500m"),
		K8sManagerMemoryRequest: envOrDefault("HICLAW_K8S_MANAGER_MEMORY_REQUEST", "1Gi"),
		K8sManagerCPU:           envOrDefault("HICLAW_K8S_MANAGER_CPU", "2"),
		K8sManagerMemory:        envOrDefault("HICLAW_K8S_MANAGER_MEMORY", "4Gi"),

		ControllerURL: os.Getenv("HICLAW_CONTROLLER_URL"),

		ManagerWorkspaceDir: os.Getenv("HICLAW_WORKSPACE_DIR"),
		HostShareDir:        os.Getenv("HICLAW_HOST_SHARE_DIR"),
		ManagerConsolePort:  envOrDefault("HICLAW_PORT_MANAGER_CONSOLE", "18888"),
		ManagerPassword:     os.Getenv("HICLAW_MANAGER_PASSWORD"),
		ManagerGatewayKey:   os.Getenv("HICLAW_MANAGER_GATEWAY_KEY"),

		MatrixServerURL:         envOrDefault("HICLAW_MATRIX_URL", "http://matrix-local.hiclaw.io:8080"),
		MatrixDomain:            envOrDefault("HICLAW_MATRIX_DOMAIN", "matrix-local.hiclaw.io:8080"),
		MatrixRegistrationToken: envOrDefault("HICLAW_MATRIX_REGISTRATION_TOKEN", os.Getenv("HICLAW_REGISTRATION_TOKEN")),
		MatrixAdminUser:         envOrDefault("HICLAW_ADMIN_USER", "admin"),
		MatrixAdminPassword:     envOrDefault("HICLAW_ADMIN_PASSWORD", "admin"),
		MatrixE2EE:              os.Getenv("HICLAW_MATRIX_E2EE") == "1" || os.Getenv("HICLAW_MATRIX_E2EE") == "true",

		OSSStoragePrefix: envOrDefault("HICLAW_STORAGE_PREFIX", "hiclaw/hiclaw-storage"),

		DefaultModel:       envOrDefault("HICLAW_DEFAULT_MODEL", "qwen3.5-plus"),
		EmbeddingModel:     os.Getenv("HICLAW_EMBEDDING_MODEL"),
		Runtime:            envOrDefault("HICLAW_RUNTIME", "docker"),
		ModelContextWindow: envOrDefaultInt("HICLAW_MODEL_CONTEXT_WINDOW", 0),
		ModelMaxTokens:     envOrDefaultInt("HICLAW_MODEL_MAX_TOKENS", 0),

		LLMProvider:   envOrDefault("HICLAW_LLM_PROVIDER", "qwen"),
		LLMAPIKey:     os.Getenv("HICLAW_LLM_API_KEY"),
		OpenAIBaseURL: os.Getenv("HICLAW_OPENAI_BASE_URL"),
		ElementWebURL: os.Getenv("HICLAW_ELEMENT_WEB_URL"),

		CMSTracesEnabled:  envBool("HICLAW_CMS_TRACES_ENABLED"),
		CMSMetricsEnabled: envBool("HICLAW_CMS_METRICS_ENABLED"),
		CMSEndpoint:       os.Getenv("HICLAW_CMS_ENDPOINT"),
		CMSLicenseKey:     os.Getenv("HICLAW_CMS_LICENSE_KEY"),
		CMSProject:        os.Getenv("HICLAW_CMS_PROJECT"),
		CMSWorkspace:      os.Getenv("HICLAW_CMS_WORKSPACE"),

		WorkerEnv: WorkerEnvDefaults{
			MatrixDomain:  envOrDefault("HICLAW_MATRIX_DOMAIN", "matrix-local.hiclaw.io:8080"),
			FSEndpoint:    os.Getenv("HICLAW_FS_ENDPOINT"),
			FSBucket:      envOrDefault("HICLAW_FS_BUCKET", "hiclaw-storage"),
			StoragePrefix: envOrDefault("HICLAW_STORAGE_PREFIX", "hiclaw/hiclaw-storage"),
			ControllerURL: os.Getenv("HICLAW_CONTROLLER_URL"),
			AIGatewayURL:  envOrDefault("HICLAW_AI_GATEWAY_URL", "http://aigw-local.hiclaw.io:8080"),
			MatrixURL:     envOrDefault("HICLAW_MATRIX_URL", "http://matrix-local.hiclaw.io:8080"),
			AdminUser:     envOrDefault("HICLAW_ADMIN_USER", "admin"),
		},
	}

	// In embedded mode, services (Tuwunel, MinIO) run inside the controller container.
	// The controller itself uses 127.0.0.1, but child containers (Manager, Workers) must
	// reach them via the controller's Docker network hostname.
	if cfg.KubeMode == "embedded" {
		if ctrlHost := extractHost(cfg.WorkerEnv.ControllerURL); ctrlHost != "" {
			cfg.WorkerEnv.MatrixURL = replaceHost(cfg.WorkerEnv.MatrixURL, ctrlHost)
			cfg.WorkerEnv.FSEndpoint = replaceHost(cfg.WorkerEnv.FSEndpoint, ctrlHost)
		}
	}

	if specJSON := os.Getenv("HICLAW_MANAGER_SPEC"); specJSON != "" {
		if err := applyManagerSpec(cfg, specJSON); err != nil {
			panic(fmt.Sprintf("invalid HICLAW_MANAGER_SPEC: %v", err))
		}
	}

	return cfg
}

// Namespace returns the effective K8s namespace, defaulting to "default".
func (c *Config) Namespace() string {
	if c.K8sNamespace != "" {
		return c.K8sNamespace
	}
	return "default"
}

// HasMinIOAdmin reports whether the local MinIO admin API is available.
func (c *Config) HasMinIOAdmin() bool {
	return c.WorkerEnv.FSEndpoint != ""
}

// CredsDir returns the directory for persisted worker credentials (embedded mode).
func (c *Config) CredsDir() string {
	return envOrDefault("HICLAW_CREDS_DIR", "/data/worker-creds")
}

// AgentFSDir returns the local filesystem root for agent workspaces.
func (c *Config) AgentFSDir() string {
	return envOrDefault("HICLAW_AGENT_FS_DIR", "/root/hiclaw-fs/agents")
}

// WorkerAgentDir returns the source directory for builtin worker agent files.
func (c *Config) WorkerAgentDir() string {
	return envOrDefault("HICLAW_WORKER_AGENT_DIR", "/opt/hiclaw/agent/worker-agent")
}

// ManagerConfigPath returns the path to the Manager Agent's openclaw.json (embedded mode).
func (c *Config) ManagerConfigPath() string {
	return envOrDefault("HICLAW_MANAGER_CONFIG_PATH", "/root/openclaw.json")
}

// RegistryPath returns the path to the workers-registry.json (embedded mode).
func (c *Config) RegistryPath() string {
	return envOrDefault("HICLAW_REGISTRY_PATH", "/root/workers-registry.json")
}

// ManagerResources returns the resource requirements for the Manager Pod.
func (c *Config) ManagerResources() *backend.ResourceRequirements {
	return &backend.ResourceRequirements{
		CPURequest:    c.K8sManagerCPURequest,
		CPULimit:      c.K8sManagerCPU,
		MemoryRequest: c.K8sManagerMemoryRequest,
		MemoryLimit:   c.K8sManagerMemory,
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

func (c *Config) K8sConfig() backend.K8sConfig {
	return backend.K8sConfig{
		Namespace:        c.K8sNamespace,
		WorkerImage:      envOrDefault("HICLAW_WORKER_IMAGE", "hiclaw/worker-agent:latest"),
		CopawWorkerImage: envOrDefault("HICLAW_COPAW_WORKER_IMAGE", "hiclaw/copaw-worker:latest"),
		WorkerCPU:        c.K8sWorkerCPU,
		WorkerMemory:     c.K8sWorkerMemory,
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

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "True" || v == "TRUE"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func applyManagerSpec(cfg *Config, specJSON string) error {
	var spec managerSpecEnv
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return err
	}

	if spec.Model != "" {
		cfg.ManagerModel = spec.Model
	}
	if spec.Runtime != "" {
		cfg.ManagerRuntime = spec.Runtime
	}
	if spec.Image != "" {
		cfg.ManagerImage = spec.Image
	}
	if spec.Resources.Requests.CPU != "" {
		cfg.K8sManagerCPURequest = spec.Resources.Requests.CPU
	}
	if spec.Resources.Requests.Memory != "" {
		cfg.K8sManagerMemoryRequest = spec.Resources.Requests.Memory
	}
	if spec.Resources.Limits.CPU != "" {
		cfg.K8sManagerCPU = spec.Resources.Limits.CPU
	}
	if spec.Resources.Limits.Memory != "" {
		cfg.K8sManagerMemory = spec.Resources.Limits.Memory
	}

	return nil
}

// extractHost returns the hostname from a URL (e.g. "http://hiclaw-controller:8090" → "hiclaw-controller").
func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// replaceHost replaces the hostname in a URL while preserving scheme, port, and path.
func replaceHost(rawURL, newHost string) string {
	if rawURL == "" || newHost == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if u.Port() != "" {
		u.Host = newHost + ":" + u.Port()
	} else {
		u.Host = newHost
	}
	return u.String()
}

func (c *Config) MatrixConfig() matrix.Config {
	return matrix.Config{
		ServerURL:         c.MatrixServerURL,
		Domain:            c.MatrixDomain,
		RegistrationToken: c.MatrixRegistrationToken,
		AdminUser:         c.MatrixAdminUser,
		AdminPassword:     c.MatrixAdminPassword,
		E2EEEnabled:       c.MatrixE2EE,
	}
}

func (c *Config) GatewayConfig() gateway.Config {
	cfg := gateway.Config{
		ConsoleURL:    c.HigressBaseURL,
		AdminUser:     c.HigressAdminUser,
		AdminPassword: c.HigressAdminPassword,
	}
	if c.KubeMode == "embedded" {
		cfg.PilotURL = "http://127.0.0.1:15014"
	}
	return cfg
}

func (c *Config) OSSConfig() oss.Config {
	accessKey := firstNonEmpty(os.Getenv("HICLAW_FS_ACCESS_KEY"), os.Getenv("HICLAW_MINIO_USER"))
	secretKey := firstNonEmpty(os.Getenv("HICLAW_FS_SECRET_KEY"), os.Getenv("HICLAW_MINIO_PASSWORD"))
	return oss.Config{
		StoragePrefix: c.OSSStoragePrefix,
		Bucket:        c.OSSBucket,
		Endpoint:      firstNonEmpty(os.Getenv("HICLAW_FS_ENDPOINT"), c.WorkerEnv.FSEndpoint),
		AccessKey:     accessKey,
		SecretKey:     secretKey,
	}
}

// ManagerAgentEnv returns environment variables that a standalone Manager Agent
// container needs to connect to the infrastructure services in the embedded
// controller container. These are passed via DockerBackend.Create.
func (c *Config) ManagerAgentEnv() map[string]string {
	env := map[string]string{}
	setIfNonEmpty := func(k, v string) {
		if v != "" {
			env[k] = v
		}
	}
	setIfNonEmpty("HICLAW_MINIO_USER", os.Getenv("HICLAW_MINIO_USER"))
	setIfNonEmpty("HICLAW_MINIO_PASSWORD", os.Getenv("HICLAW_MINIO_PASSWORD"))
	setIfNonEmpty("HICLAW_ADMIN_USER", c.MatrixAdminUser)
	setIfNonEmpty("HICLAW_ADMIN_PASSWORD", c.MatrixAdminPassword)
	setIfNonEmpty("HICLAW_REGISTRATION_TOKEN", c.MatrixRegistrationToken)
	setIfNonEmpty("HICLAW_HIGRESS_ADMIN_USER", c.HigressAdminUser)
	setIfNonEmpty("HICLAW_HIGRESS_ADMIN_PASSWORD", c.HigressAdminPassword)
	setIfNonEmpty("HICLAW_AI_GATEWAY_ADMIN_URL", c.HigressBaseURL)
	setIfNonEmpty("HICLAW_MATRIX_URL", c.WorkerEnv.MatrixURL)
	setIfNonEmpty("HICLAW_AI_GATEWAY_URL", c.WorkerEnv.AIGatewayURL)
	setIfNonEmpty("HICLAW_FS_ENDPOINT", c.WorkerEnv.FSEndpoint)
	setIfNonEmpty("HICLAW_FS_BUCKET", c.WorkerEnv.FSBucket)
	setIfNonEmpty("HICLAW_FS_ACCESS_KEY", firstNonEmpty(os.Getenv("HICLAW_FS_ACCESS_KEY"), os.Getenv("HICLAW_MINIO_USER")))
	setIfNonEmpty("HICLAW_FS_SECRET_KEY", firstNonEmpty(os.Getenv("HICLAW_FS_SECRET_KEY"), os.Getenv("HICLAW_MINIO_PASSWORD")))
	setIfNonEmpty("HICLAW_STORAGE_PREFIX", c.OSSStoragePrefix)
	setIfNonEmpty("HICLAW_MATRIX_DOMAIN", c.MatrixDomain)
	setIfNonEmpty("HICLAW_DEFAULT_MODEL", c.DefaultModel)
	setIfNonEmpty("HICLAW_EMBEDDING_MODEL", c.EmbeddingModel)
	setIfNonEmpty("HICLAW_LLM_PROVIDER", c.LLMProvider)
	setIfNonEmpty("HICLAW_LLM_API_KEY", c.LLMAPIKey)
	setIfNonEmpty("HICLAW_ELEMENT_WEB_URL", c.ElementWebURL)
	if c.MatrixE2EE {
		env["HICLAW_MATRIX_E2EE"] = "1"
	}
	if c.CMSTracesEnabled {
		env["HICLAW_CMS_TRACES_ENABLED"] = "1"
	}
	if c.CMSMetricsEnabled {
		env["HICLAW_CMS_METRICS_ENABLED"] = "1"
	}
	setIfNonEmpty("HICLAW_CMS_ENDPOINT", c.CMSEndpoint)
	setIfNonEmpty("HICLAW_CMS_LICENSE_KEY", c.CMSLicenseKey)
	setIfNonEmpty("HICLAW_CMS_PROJECT", c.CMSProject)
	setIfNonEmpty("HICLAW_CMS_WORKSPACE", c.CMSWorkspace)
	return env
}

func (c *Config) AgentConfig() agentconfig.Config {
	// Use WorkerEnv URLs (host-replaced in embedded mode) since openclaw.json
	// is consumed by worker containers, not the controller itself.
	matrixURL := c.MatrixServerURL
	aiGatewayURL := envOrDefault("HICLAW_AI_GATEWAY_URL", "http://aigw-local.hiclaw.io:8080")
	if c.KubeMode == "embedded" {
		if c.WorkerEnv.MatrixURL != "" {
			matrixURL = c.WorkerEnv.MatrixURL
		}
		if c.WorkerEnv.AIGatewayURL != "" {
			aiGatewayURL = c.WorkerEnv.AIGatewayURL
		}
	}
	return agentconfig.Config{
		MatrixDomain:       c.MatrixDomain,
		MatrixServerURL:    matrixURL,
		AIGatewayURL:       aiGatewayURL,
		AdminUser:          c.MatrixAdminUser,
		DefaultModel:       c.DefaultModel,
		EmbeddingModel:     c.EmbeddingModel,
		Runtime:            c.Runtime,
		E2EEEnabled:        c.MatrixE2EE,
		ModelContextWindow: c.ModelContextWindow,
		ModelMaxTokens:     c.ModelMaxTokens,
		CMSTracesEnabled:   c.CMSTracesEnabled,
		CMSMetricsEnabled:  c.CMSMetricsEnabled,
		CMSEndpoint:        c.CMSEndpoint,
		CMSLicenseKey:      c.CMSLicenseKey,
		CMSProject:         c.CMSProject,
		CMSWorkspace:       c.CMSWorkspace,
	}
}
