package agentconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateOpenClawConfig_Basic(t *testing.T) {
	g := NewGenerator(Config{
		MatrixDomain:    "matrix.test:8080",
		MatrixServerURL: "http://matrix.test:8080",
		AIGatewayURL:    "http://aigw.test:8080",
		AdminUser:       "admin",
		DefaultModel:    "qwen3.5-plus",
	})

	data, err := g.GenerateOpenClawConfig(WorkerConfigRequest{
		WorkerName:  "worker-alice",
		MatrixToken: "tok-matrix-alice",
		GatewayKey:  "key-gateway-alice",
	})
	if err != nil {
		t.Fatalf("GenerateOpenClawConfig: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify Matrix channel config
	channels := config["channels"].(map[string]interface{})
	matrixCfg := channels["matrix"].(map[string]interface{})
	if matrixCfg["userId"] != "@worker-alice:matrix.test:8080" {
		t.Errorf("userId = %v", matrixCfg["userId"])
	}
	if matrixCfg["accessToken"] != "tok-matrix-alice" {
		t.Errorf("accessToken = %v", matrixCfg["accessToken"])
	}

	// Verify default allowFrom includes manager and admin
	groupAllow := toStringSlice(matrixCfg["groupAllowFrom"])
	if !containsString(groupAllow, "@manager:matrix.test:8080") {
		t.Errorf("groupAllowFrom missing manager: %v", groupAllow)
	}
	if !containsString(groupAllow, "@admin:matrix.test:8080") {
		t.Errorf("groupAllowFrom missing admin: %v", groupAllow)
	}

	// Verify model config — default model is set via agents.defaults.model.primary
	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	model := defaults["model"].(map[string]interface{})
	if model["primary"] != "hiclaw-gateway/qwen3.5-plus" {
		t.Errorf("agents.defaults.model.primary = %v", model["primary"])
	}
	// Verify models section does NOT contain "default" (unrecognized key in openclaw)
	models := config["models"].(map[string]interface{})
	if _, exists := models["default"]; exists {
		t.Error("models.default should not be set (unrecognized key in openclaw)")
	}
}

func TestGenerateOpenClawConfig_TeamWorker(t *testing.T) {
	g := NewGenerator(Config{
		MatrixDomain:    "matrix.test:8080",
		MatrixServerURL: "http://matrix.test:8080",
		AIGatewayURL:    "http://aigw.test:8080",
	})

	data, err := g.GenerateOpenClawConfig(WorkerConfigRequest{
		WorkerName:     "worker-dev-1",
		MatrixToken:    "tok",
		GatewayKey:     "key",
		TeamLeaderName: "team-lead-dev",
	})
	if err != nil {
		t.Fatalf("GenerateOpenClawConfig: %v", err)
	}

	var config map[string]interface{}
	json.Unmarshal(data, &config)

	matrixCfg := config["channels"].(map[string]interface{})["matrix"].(map[string]interface{})
	groupAllow := toStringSlice(matrixCfg["groupAllowFrom"])

	if containsString(groupAllow, "@manager:matrix.test:8080") {
		t.Error("team worker should not have manager in groupAllowFrom")
	}
	if !containsString(groupAllow, "@team-lead-dev:matrix.test:8080") {
		t.Errorf("team worker groupAllowFrom should include leader: %v", groupAllow)
	}
}

func TestGenerateOpenClawConfig_ChannelPolicy(t *testing.T) {
	g := NewGenerator(Config{
		MatrixDomain:    "d",
		MatrixServerURL: "http://m:8080",
		AIGatewayURL:    "http://g:8080",
	})

	data, err := g.GenerateOpenClawConfig(WorkerConfigRequest{
		WorkerName:  "w1",
		MatrixToken: "tok",
		GatewayKey:  "key",
		ChannelPolicy: &ChannelPolicy{
			GroupAllowExtra: []string{"extra-user"},
			GroupDenyExtra:  []string{"manager"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateOpenClawConfig: %v", err)
	}

	var config map[string]interface{}
	json.Unmarshal(data, &config)

	matrixCfg := config["channels"].(map[string]interface{})["matrix"].(map[string]interface{})
	groupAllow := toStringSlice(matrixCfg["groupAllowFrom"])

	if containsString(groupAllow, "@manager:d") {
		t.Error("manager should be denied by policy")
	}
	if !containsString(groupAllow, "@extra-user:d") {
		t.Errorf("extra-user should be allowed: %v", groupAllow)
	}
}

func TestGenerateOpenClawConfig_CustomModel(t *testing.T) {
	g := NewGenerator(Config{
		MatrixDomain:    "d",
		MatrixServerURL: "http://m:8080",
		AIGatewayURL:    "http://g:8080",
		DefaultModel:    "custom-model-x",
	})

	data, err := g.GenerateOpenClawConfig(WorkerConfigRequest{
		WorkerName:  "w1",
		MatrixToken: "tok",
		GatewayKey:  "key",
	})
	if err != nil {
		t.Fatalf("GenerateOpenClawConfig: %v", err)
	}

	var config map[string]interface{}
	json.Unmarshal(data, &config)

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	model := defaults["model"].(map[string]interface{})
	if model["primary"] != "hiclaw-gateway/custom-model-x" {
		t.Errorf("agents.defaults.model.primary = %v, want hiclaw-gateway/custom-model-x", model["primary"])
	}
}

func TestGenerateOpenClawConfig_WithEmbedding(t *testing.T) {
	g := NewGenerator(Config{
		MatrixDomain:    "d",
		MatrixServerURL: "http://m:8080",
		AIGatewayURL:    "http://g:8080",
		EmbeddingModel:  "text-embedding-v3",
	})

	data, err := g.GenerateOpenClawConfig(WorkerConfigRequest{
		WorkerName:  "w1",
		MatrixToken: "tok",
		GatewayKey:  "key-embed",
	})
	if err != nil {
		t.Fatalf("GenerateOpenClawConfig: %v", err)
	}

	var config map[string]interface{}
	json.Unmarshal(data, &config)

	agents := config["agents"].(map[string]interface{})
	defaults := agents["defaults"].(map[string]interface{})
	memSearch, ok := defaults["memorySearch"].(map[string]interface{})
	if !ok {
		t.Fatal("memorySearch not found in agents.defaults")
	}
	if memSearch["model"] != "text-embedding-v3" {
		t.Errorf("memorySearch.model = %v", memSearch["model"])
	}
}

func TestMergeBuiltinSection_NewFile(t *testing.T) {
	source := "# Worker Agent\n\nYou are a worker.\n"
	result := MergeBuiltinSection("", source)

	if !strings.Contains(result, BuiltinStart) {
		t.Error("result should contain builtin start marker")
	}
	if !strings.Contains(result, BuiltinEnd) {
		t.Error("result should contain builtin end marker")
	}
	if !strings.Contains(result, "You are a worker.") {
		t.Error("result should contain source content")
	}
}

func TestMergeBuiltinSection_UpdatePreservesUserContent(t *testing.T) {
	existing := BuiltinHeader + "\nOld content\n\n" + BuiltinEnd + "\n\nMy custom rules\n"
	newSource := "# Updated\n\nNew builtin content\n"

	result := MergeBuiltinSection(existing, newSource)

	if !strings.Contains(result, "New builtin content") {
		t.Error("result should contain updated builtin content")
	}
	if strings.Contains(result, "Old content") {
		t.Error("result should not contain old builtin content")
	}
	if !strings.Contains(result, "My custom rules") {
		t.Error("result should preserve user content")
	}
}

func TestMergeBuiltinSection_LegacyFile(t *testing.T) {
	legacy := "# Old content without markers\nSome instructions\n"
	source := "# New builtin\nNew content\n"

	result := MergeBuiltinSection(legacy, source)

	if !strings.Contains(result, BuiltinStart) {
		t.Error("result should add markers to legacy file")
	}
	if !strings.Contains(result, "New content") {
		t.Error("result should contain new builtin content")
	}
}

func TestExtractFrontmatter(t *testing.T) {
	content := "---\ntitle: Test\n---\n# Content\nBody text\n"
	fm, body := ExtractFrontmatter(content)

	if !strings.Contains(fm, "title: Test") {
		t.Errorf("frontmatter = %q", fm)
	}
	if !strings.Contains(body, "# Content") {
		t.Errorf("body = %q", body)
	}
}

func TestExtractFrontmatter_NoFrontmatter(t *testing.T) {
	content := "# Just a heading\nBody text\n"
	fm, body := ExtractFrontmatter(content)

	if fm != "" {
		t.Errorf("expected empty frontmatter, got %q", fm)
	}
	if body != content {
		t.Errorf("body should equal original content")
	}
}

func TestDefaultModelSpec(t *testing.T) {
	spec := defaultModelSpec("claude-opus-4-6")
	if spec.ContextWindow != 1000000 {
		t.Errorf("claude-opus-4-6 ctx = %d, want 1000000", spec.ContextWindow)
	}
	if spec.MaxTokens != 128000 {
		t.Errorf("claude-opus-4-6 max = %d, want 128000", spec.MaxTokens)
	}
	if len(spec.Input) != 2 || spec.Input[1] != "image" {
		t.Errorf("claude-opus-4-6 should have vision: %v", spec.Input)
	}

	unknown := defaultModelSpec("unknown-model-xyz")
	if unknown.ContextWindow != 150000 {
		t.Errorf("unknown model ctx = %d, want 150000", unknown.ContextWindow)
	}
}
