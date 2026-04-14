package config

import "testing"

func TestLoadConfigAppliesManagerSpec(t *testing.T) {
	t.Setenv("HICLAW_MANAGER_SPEC", `{
		"model":"qwen-max",
		"runtime":"copaw",
		"image":"hiclaw/manager:test",
		"resources":{
			"requests":{"cpu":"750m","memory":"1536Mi"},
			"limits":{"cpu":"3","memory":"5Gi"}
		}
	}`)
	t.Setenv("HICLAW_DEFAULT_MODEL", "qwen-default")

	cfg := LoadConfig()

	if cfg.ManagerModel != "qwen-max" {
		t.Fatalf("ManagerModel = %q, want %q", cfg.ManagerModel, "qwen-max")
	}
	if cfg.ManagerRuntime != "copaw" {
		t.Fatalf("ManagerRuntime = %q, want %q", cfg.ManagerRuntime, "copaw")
	}
	if cfg.ManagerImage != "hiclaw/manager:test" {
		t.Fatalf("ManagerImage = %q, want %q", cfg.ManagerImage, "hiclaw/manager:test")
	}
	if cfg.K8sManagerCPURequest != "750m" {
		t.Fatalf("K8sManagerCPURequest = %q, want %q", cfg.K8sManagerCPURequest, "750m")
	}
	if cfg.K8sManagerMemoryRequest != "1536Mi" {
		t.Fatalf("K8sManagerMemoryRequest = %q, want %q", cfg.K8sManagerMemoryRequest, "1536Mi")
	}
	if cfg.K8sManagerCPU != "3" {
		t.Fatalf("K8sManagerCPU = %q, want %q", cfg.K8sManagerCPU, "3")
	}
	if cfg.K8sManagerMemory != "5Gi" {
		t.Fatalf("K8sManagerMemory = %q, want %q", cfg.K8sManagerMemory, "5Gi")
	}
}

func TestLoadConfigUsesLegacyManagerEnvFallback(t *testing.T) {
	t.Setenv("HICLAW_MANAGER_MODEL", "legacy-model")
	t.Setenv("HICLAW_MANAGER_RUNTIME", "openclaw")
	t.Setenv("HICLAW_MANAGER_IMAGE", "hiclaw/manager:legacy")
	t.Setenv("HICLAW_K8S_MANAGER_CPU", "4")
	t.Setenv("HICLAW_K8S_MANAGER_MEMORY", "6Gi")

	cfg := LoadConfig()

	if cfg.ManagerModel != "legacy-model" {
		t.Fatalf("ManagerModel = %q, want %q", cfg.ManagerModel, "legacy-model")
	}
	if cfg.ManagerRuntime != "openclaw" {
		t.Fatalf("ManagerRuntime = %q, want %q", cfg.ManagerRuntime, "openclaw")
	}
	if cfg.ManagerImage != "hiclaw/manager:legacy" {
		t.Fatalf("ManagerImage = %q, want %q", cfg.ManagerImage, "hiclaw/manager:legacy")
	}
	if cfg.K8sManagerCPU != "4" {
		t.Fatalf("K8sManagerCPU = %q, want %q", cfg.K8sManagerCPU, "4")
	}
	if cfg.K8sManagerMemory != "6Gi" {
		t.Fatalf("K8sManagerMemory = %q, want %q", cfg.K8sManagerMemory, "6Gi")
	}
}

func TestLoadConfigPanicsOnInvalidManagerSpec(t *testing.T) {
	t.Setenv("HICLAW_MANAGER_SPEC", "{")

	defer func() {
		if recover() == nil {
			t.Fatal("LoadConfig() did not panic on invalid HICLAW_MANAGER_SPEC")
		}
	}()

	_ = LoadConfig()
}

func TestLoadConfigPrefersAbstractInfraEnv(t *testing.T) {
	t.Setenv("HICLAW_KUBE_MODE", "incluster")
	t.Setenv("HICLAW_AI_GATEWAY_ADMIN_URL", "http://higress-admin.example.com:8001")
	t.Setenv("HICLAW_FS_BUCKET", "hiclaw-fs")
	t.Setenv("HICLAW_FS_ENDPOINT", "http://fs.example.com:9000")
	t.Setenv("HICLAW_STORAGE_PREFIX", "teams/demo")
	t.Setenv("HICLAW_CONTROLLER_URL", "http://controller.example.com:8090")
	t.Setenv("HICLAW_AI_GATEWAY_URL", "http://aigw.example.com:8080")
	t.Setenv("HICLAW_MATRIX_URL", "http://matrix.example.com:8080")

	cfg := LoadConfig()

	if cfg.HigressBaseURL != "http://higress-admin.example.com:8001" {
		t.Fatalf("HigressBaseURL = %q, want abstract admin URL", cfg.HigressBaseURL)
	}
	if cfg.OSSBucket != "hiclaw-fs" {
		t.Fatalf("OSSBucket = %q, want %q", cfg.OSSBucket, "hiclaw-fs")
	}
	if cfg.WorkerEnv.FSBucket != "hiclaw-fs" {
		t.Fatalf("WorkerEnv.FSBucket = %q, want %q", cfg.WorkerEnv.FSBucket, "hiclaw-fs")
	}
	if cfg.WorkerEnv.FSEndpoint != "http://fs.example.com:9000" {
		t.Fatalf("WorkerEnv.FSEndpoint = %q, want %q", cfg.WorkerEnv.FSEndpoint, "http://fs.example.com:9000")
	}
}

func TestManagerAgentEnvForwardsAbstractInfraEnv(t *testing.T) {
	t.Setenv("HICLAW_KUBE_MODE", "incluster")
	t.Setenv("HICLAW_MINIO_USER", "root")
	t.Setenv("HICLAW_MINIO_PASSWORD", "secret")
	t.Setenv("HICLAW_AI_GATEWAY_ADMIN_URL", "http://higress-admin.example.com:8001")
	t.Setenv("HICLAW_FS_BUCKET", "hiclaw-fs")
	t.Setenv("HICLAW_FS_ENDPOINT", "http://fs.example.com:9000")
	t.Setenv("HICLAW_STORAGE_PREFIX", "teams/demo")
	t.Setenv("HICLAW_AI_GATEWAY_URL", "http://aigw.example.com:8080")
	t.Setenv("HICLAW_MATRIX_URL", "http://matrix.example.com:8080")

	cfg := LoadConfig()
	env := cfg.ManagerAgentEnv()

	for key, want := range map[string]string{
		"HICLAW_AI_GATEWAY_ADMIN_URL": "http://higress-admin.example.com:8001",
		"HICLAW_MATRIX_URL":           "http://matrix.example.com:8080",
		"HICLAW_AI_GATEWAY_URL":       "http://aigw.example.com:8080",
		"HICLAW_FS_ENDPOINT":          "http://fs.example.com:9000",
		"HICLAW_FS_BUCKET":            "hiclaw-fs",
		"HICLAW_STORAGE_PREFIX":       "teams/demo",
		"HICLAW_FS_ACCESS_KEY":        "root",
		"HICLAW_FS_SECRET_KEY":        "secret",
	} {
		if got := env[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	for _, legacyKey := range []string{"HIGRESS_BASE_URL", "HICLAW_MINIO_ENDPOINT", "HICLAW_MINIO_BUCKET", "HICLAW_OSS_BUCKET"} {
		if _, ok := env[legacyKey]; ok {
			t.Fatalf("unexpected legacy env %s in ManagerAgentEnv", legacyKey)
		}
	}
}
