package agentconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Generator produces worker runtime configuration files in pure Go.
type Generator struct {
	config Config
}

// NewGenerator creates an agent config generator.
func NewGenerator(cfg Config) *Generator {
	if cfg.AdminUser == "" {
		cfg.AdminUser = "admin"
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "qwen3.5-plus"
	}
	return &Generator{config: cfg}
}

// GenerateOpenClawConfig produces the openclaw.json content for a worker.
func (g *Generator) GenerateOpenClawConfig(req WorkerConfigRequest) ([]byte, error) {
	modelName := req.ModelName
	if modelName == "" {
		modelName = g.config.DefaultModel
	}
	modelName = strings.TrimPrefix(modelName, "hiclaw-gateway/")

	matrixServerURL := g.config.MatrixServerURL
	if matrixServerURL == "" {
		// K8s deployments must set HICLAW_MATRIX_URL (Helm injects it automatically).
		// This default only applies to docker/embedded mode.
		matrixServerURL = "http://matrix-local.hiclaw.io:8080"
	}

	aiGatewayURL := g.config.AIGatewayURL
	if aiGatewayURL == "" {
		// K8s deployments must set HICLAW_AI_GATEWAY_URL (Helm injects it automatically).
		aiGatewayURL = "http://aigw-local.hiclaw.io:8080"
	}

	matrixDomain := g.config.MatrixDomain
	if matrixDomain == "" {
		matrixDomain = "matrix-local.hiclaw.io:8080"
	}

	adminUser := g.config.AdminUser
	adminMatrixID := fmt.Sprintf("@%s:%s", adminUser, matrixDomain)

	// Build the base openclaw.json structure (must match OpenClaw schema)
	config := map[string]interface{}{
		"gateway": map[string]interface{}{
			"mode": "local",
			"port": 18800,
			"auth": map[string]interface{}{
				"token": generateRandomHex(32),
			},
			"remote": map[string]interface{}{
				"token": generateRandomHex(32),
			},
		},
		"channels": map[string]interface{}{
			"matrix": g.buildMatrixChannelConfig(req, matrixServerURL, matrixDomain, adminMatrixID),
		},
		"models": map[string]interface{}{
			"mode": "merge",
			"providers": map[string]interface{}{
				"hiclaw-gateway": map[string]interface{}{
					"baseUrl": aiGatewayURL + "/v1",
					"apiKey":  req.GatewayKey,
					"api":     "openai-completions",
					"models":  g.allModelSpecs(modelName),
				},
			},
		},
		"agents": map[string]interface{}{
			"defaults": map[string]interface{}{
				"timeoutSeconds": 1800,
				"workspace":      "~",
				"model": map[string]interface{}{
					"primary": "hiclaw-gateway/" + modelName,
				},
				"models":       g.allModelAliases(modelName),
				"maxConcurrent": 4,
				"subagents": map[string]interface{}{
					"maxConcurrent": 8,
				},
			},
		},
		"session": map[string]interface{}{
			"resetByType": map[string]interface{}{
				"dm":    map[string]interface{}{"mode": "daily", "atHour": 4},
				"group": map[string]interface{}{"mode": "daily", "atHour": 4},
			},
		},
		"plugins": map[string]interface{}{
			"load": map[string]interface{}{
				"paths": []string{"/opt/openclaw/extensions/matrix"},
			},
			"entries": map[string]interface{}{
				"matrix": map[string]interface{}{"enabled": true},
			},
		},
	}

	// Add embedding model for memory search if configured
	if g.config.EmbeddingModel != "" {
		agents := config["agents"].(map[string]interface{})
		defaults := agents["defaults"].(map[string]interface{})
		defaults["memorySearch"] = map[string]interface{}{
			"provider": "openai",
			"model":    g.config.EmbeddingModel,
			"remote": map[string]interface{}{
				"baseUrl": aiGatewayURL + "/v1",
				"apiKey":  req.GatewayKey,
			},
		}
	}

	// Apply channel policy overrides
	if req.ChannelPolicy != nil {
		g.applyChannelPolicy(config, req.ChannelPolicy, matrixDomain)
	}

	return json.MarshalIndent(config, "", "  ")
}

func (g *Generator) buildMatrixChannelConfig(req WorkerConfigRequest, serverURL, domain, adminMatrixID string) map[string]interface{} {
	workerMatrixID := fmt.Sprintf("@%s:%s", req.WorkerName, domain)

	// Default allow list: Manager + Admin
	managerMatrixID := fmt.Sprintf("@manager:%s", domain)
	groupAllowFrom := []string{managerMatrixID, adminMatrixID}
	dmAllowFrom := []string{managerMatrixID, adminMatrixID}

	// Team worker: use Leader + Admin instead
	if req.TeamLeaderName != "" {
		leaderMatrixID := fmt.Sprintf("@%s:%s", req.TeamLeaderName, domain)
		groupAllowFrom = []string{leaderMatrixID, adminMatrixID}
		dmAllowFrom = []string{leaderMatrixID, adminMatrixID}
	}

	cfg := map[string]interface{}{
		"homeserver":  serverURL,
		"enabled":     true,
		"userId":      workerMatrixID,
		"accessToken": req.MatrixToken,
		"encryption":  g.config.E2EEEnabled,
		"dm": map[string]interface{}{
			"policy":    "allowlist",
			"allowFrom": dmAllowFrom,
		},
		"groupPolicy":    "allowlist",
		"groupAllowFrom": groupAllowFrom,
		"groups": map[string]interface{}{
			"*": map[string]interface{}{"allow": true, "requireMention": true},
		},
	}

	return cfg
}

func (g *Generator) applyChannelPolicy(config map[string]interface{}, policy *ChannelPolicy, domain string) {
	channels, _ := config["channels"].(map[string]interface{})
	if channels == nil {
		return
	}
	matrixCfg, _ := channels["matrix"].(map[string]interface{})
	if matrixCfg == nil {
		return
	}

	resolveID := func(s string) string {
		if strings.HasPrefix(s, "@") {
			return s
		}
		return fmt.Sprintf("@%s:%s", s, domain)
	}

	// GroupAllowFrom additions
	if len(policy.GroupAllowExtra) > 0 {
		existing := toStringSlice(matrixCfg["groupAllowFrom"])
		for _, u := range policy.GroupAllowExtra {
			id := resolveID(u)
			if !containsString(existing, id) {
				existing = append(existing, id)
			}
		}
		matrixCfg["groupAllowFrom"] = existing
	}

	// DM AllowFrom additions
	if len(policy.DMAllowExtra) > 0 {
		dm, _ := matrixCfg["dm"].(map[string]interface{})
		if dm != nil {
			existing := toStringSlice(dm["allowFrom"])
			for _, u := range policy.DMAllowExtra {
				id := resolveID(u)
				if !containsString(existing, id) {
					existing = append(existing, id)
				}
			}
			dm["allowFrom"] = existing
		}
	}

	// GroupDenyExtra: remove from groupAllowFrom
	if len(policy.GroupDenyExtra) > 0 {
		existing := toStringSlice(matrixCfg["groupAllowFrom"])
		denySet := make(map[string]bool)
		for _, u := range policy.GroupDenyExtra {
			denySet[resolveID(u)] = true
		}
		var filtered []string
		for _, id := range existing {
			if !denySet[id] {
				filtered = append(filtered, id)
			}
		}
		matrixCfg["groupAllowFrom"] = filtered
	}

	// DMDenyExtra: remove from dm.allowFrom
	if len(policy.DMDenyExtra) > 0 {
		dm, _ := matrixCfg["dm"].(map[string]interface{})
		if dm != nil {
			existing := toStringSlice(dm["allowFrom"])
			denySet := make(map[string]bool)
			for _, u := range policy.DMDenyExtra {
				denySet[resolveID(u)] = true
			}
			var filtered []string
			for _, id := range existing {
				if !denySet[id] {
					filtered = append(filtered, id)
				}
			}
			dm["allowFrom"] = filtered
		}
	}
}

// resolveModelSpec returns model parameters, applying config overrides.
func (g *Generator) resolveModelSpec(modelName string) ModelSpec {
	spec := defaultModelSpec(modelName)

	// Apply user overrides
	if g.config.ModelContextWindow > 0 {
		spec.ContextWindow = g.config.ModelContextWindow
	}
	if g.config.ModelMaxTokens > 0 {
		spec.MaxTokens = g.config.ModelMaxTokens
	}
	if g.config.ModelVision != nil {
		if *g.config.ModelVision {
			spec.Input = []string{"text", "image"}
		} else {
			spec.Input = []string{"text"}
		}
	}
	if g.config.ModelReasoning != nil {
		spec.Reasoning = *g.config.ModelReasoning
	}

	return spec
}

// defaultModelSpec returns built-in parameters for known models.
func defaultModelSpec(modelName string) ModelSpec {
	type preset struct {
		ctx, max int
		vision   bool
		reason   bool
	}

	presets := map[string]preset{
		"gpt-5.3-codex":     {400000, 128000, true, true},
		"gpt-5-mini":        {400000, 128000, true, true},
		"gpt-5-nano":        {400000, 128000, true, true},
		"claude-opus-4-6":   {1000000, 128000, true, true},
		"claude-sonnet-4-6": {1000000, 64000, true, true},
		"claude-haiku-4-5":  {200000, 64000, true, true},
		"qwen3.5-plus":      {200000, 64000, true, true},
		"deepseek-chat":     {256000, 128000, false, true},
		"deepseek-reasoner": {256000, 128000, false, true},
		"kimi-k2.5":         {256000, 128000, true, true},
		"glm-5":             {200000, 128000, false, true},
		"MiniMax-M2.7":          {200000, 128000, false, true},
		"MiniMax-M2.7-highspeed": {200000, 128000, false, true},
		"MiniMax-M2.5":          {200000, 128000, false, true},
	}

	p, found := presets[modelName]
	if !found {
		p = preset{150000, 128000, false, true}
	}

	input := []string{"text"}
	if p.vision {
		input = []string{"text", "image"}
	}

	return ModelSpec{
		ID:            modelName,
		Name:          modelName,
		ContextWindow: p.ctx,
		MaxTokens:     p.max,
		Reasoning:     p.reason,
		Input:         input,
	}
}

// helpers (duplicated from gateway to avoid cross-package dependency)

func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	switch arr := v.(type) {
	case []interface{}:
		var result []string
		for _, item := range arr {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return arr
	}
	return nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// allModelSpecs returns all known model specs for the openclaw.json models list.
func (g *Generator) allModelSpecs(selectedModel string) []ModelSpec {
	allModels := []string{
		"gpt-5.4", "gpt-5.3-codex", "gpt-5-mini", "gpt-5-nano",
		"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5",
		"qwen3.5-plus",
		"deepseek-chat", "deepseek-reasoner",
		"kimi-k2.5", "glm-5",
		"MiniMax-M2.7", "MiniMax-M2.7-highspeed", "MiniMax-M2.5",
	}

	specs := make([]ModelSpec, 0, len(allModels)+1)
	seen := make(map[string]bool)
	for _, name := range allModels {
		specs = append(specs, g.resolveModelSpec(name))
		seen[name] = true
	}
	// Add custom model if not in the built-in list
	if !seen[selectedModel] {
		specs = append(specs, g.resolveModelSpec(selectedModel))
	}
	return specs
}

// allModelAliases returns the agents.defaults.models alias map.
func (g *Generator) allModelAliases(selectedModel string) map[string]interface{} {
	allModels := []string{
		"gpt-5.4", "gpt-5.3-codex", "gpt-5-mini", "gpt-5-nano",
		"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5",
		"qwen3.5-plus",
		"deepseek-chat", "deepseek-reasoner",
		"kimi-k2.5", "glm-5",
		"MiniMax-M2.7", "MiniMax-M2.7-highspeed", "MiniMax-M2.5",
	}

	aliases := make(map[string]interface{})
	for _, name := range allModels {
		aliases["hiclaw-gateway/"+name] = map[string]interface{}{"alias": name}
	}
	// Add custom model if not in the built-in list
	if _, exists := aliases["hiclaw-gateway/"+selectedModel]; !exists {
		aliases["hiclaw-gateway/"+selectedModel] = map[string]interface{}{"alias": selectedModel}
	}
	return aliases
}
