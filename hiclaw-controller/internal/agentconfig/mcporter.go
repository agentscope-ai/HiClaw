package agentconfig

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GenerateMcporterConfig produces mcporter-servers.json content for a worker's
// authorized MCP servers. Each server entry points at the AI gateway's MCP endpoint.
func (g *Generator) GenerateMcporterConfig(gatewayKey, gatewayServerURL string, mcpServers []string) ([]byte, error) {
	if len(mcpServers) == 0 {
		return nil, nil
	}

	serverURL := gatewayServerURL
	if serverURL == "" {
		serverURL = g.config.AIGatewayURL
	}
	if serverURL == "" {
		serverURL = "http://aigw-local.hiclaw.io:8080"
	}
	serverURL = strings.TrimRight(serverURL, "/")

	servers := make(map[string]interface{})
	for _, name := range mcpServers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		servers[name] = map[string]interface{}{
			"url":       fmt.Sprintf("%s/mcp-servers/%s/mcp", serverURL, name),
			"transport": "http",
			"headers": map[string]string{
				"Authorization": "Bearer " + gatewayKey,
			},
		}
	}

	config := map[string]interface{}{
		"mcpServers": servers,
	}
	return json.MarshalIndent(config, "", "  ")
}
