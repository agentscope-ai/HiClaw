package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"log"
	"sync"
	"time"
)

// HigressClient implements Client for self-hosted Higress gateway.
type HigressClient struct {
	config Config
	http   *http.Client

	mu      sync.Mutex
	cookies []*http.Cookie
}

// NewHigressClient creates a gateway Client for Higress Console API.
func NewHigressClient(cfg Config, httpClient *http.Client) *HigressClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.AdminUser == "" {
		cfg.AdminUser = "admin"
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = "admin"
	}
	return &HigressClient{config: cfg, http: httpClient}
}

// ensureSession logs in to Higress Console and caches the session cookie.
func (c *HigressClient) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.cookies) > 0 {
		return nil
	}

	// Initialize admin account on first boot (idempotent — succeeds if already initialized).
	// Higress Console requires /system/init before login works.
	// Race note: the Higress all-in-one base image may auto-initialize with admin/admin
	// before we get here. We retry init to win the race when possible.
	initBody := fmt.Sprintf(`{"adminUser":{"name":%q,"password":%q,"displayName":%q}}`,
		c.config.AdminUser, c.config.AdminPassword, c.config.AdminUser)
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.config.ConsoleURL+"/system/init",
		strings.NewReader(initBody))
	if err != nil {
		return err
	}
	initReq.Header.Set("Content-Type", "application/json")
	initResp, err := c.http.Do(initReq)
	if err != nil {
		return fmt.Errorf("higress init: %w", err)
	}
	initResp.Body.Close()

	// Try login with configured password first, then fallback to default admin/admin
	// (Higress all-in-one may have auto-initialized with default credentials).
	passwords := []string{c.config.AdminPassword}
	if c.config.AdminPassword != "admin" {
		passwords = append(passwords, "admin")
	}

	for _, pw := range passwords {
		body := fmt.Sprintf(`{"username":%q,"password":%q}`, c.config.AdminUser, pw)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.config.ConsoleURL+"/session/login",
			strings.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("higress login: %w", err)
		}

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			c.cookies = resp.Cookies()
			resp.Body.Close()
			return nil
		}
		resp.Body.Close()
	}

	return fmt.Errorf("higress login: all credential attempts failed")
}

func (c *HigressClient) EnsureConsumer(ctx context.Context, req ConsumerRequest) (*ConsumerResult, error) {
	body := map[string]interface{}{
		"name": req.Name,
		"credentials": []map[string]interface{}{
			{
				"type":   "key-auth",
				"source": "BEARER",
				"values": []string{req.CredentialKey},
			},
		},
	}

	_, statusCode, err := c.doJSON(ctx, http.MethodPost, "/v1/consumers", body)
	if err != nil {
		return nil, fmt.Errorf("ensure consumer %s: %w", req.Name, err)
	}

	status := "created"
	if statusCode == http.StatusConflict {
		status = "exists"
	} else if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("ensure consumer %s: HTTP %d", req.Name, statusCode)
	}

	return &ConsumerResult{
		Status: status,
		APIKey: req.CredentialKey,
	}, nil
}

func (c *HigressClient) DeleteConsumer(ctx context.Context, name string) error {
	_, statusCode, err := c.doJSON(ctx, http.MethodDelete, "/v1/consumers/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete consumer %s: %w", name, err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusNoContent && statusCode != http.StatusNotFound {
		return fmt.Errorf("delete consumer %s: HTTP %d", name, statusCode)
	}
	return nil
}

func (c *HigressClient) AuthorizeAIRoutes(ctx context.Context, consumerName string) error {
	return c.modifyAIRoutes(ctx, consumerName, true)
}

func (c *HigressClient) DeauthorizeAIRoutes(ctx context.Context, consumerName string) error {
	return c.modifyAIRoutes(ctx, consumerName, false)
}

func (c *HigressClient) modifyAIRoutes(ctx context.Context, consumerName string, add bool) error {
	respBody, statusCode, err := c.doJSON(ctx, http.MethodGet, "/v1/ai/routes", nil)
	if err != nil {
		return fmt.Errorf("list AI routes: %w", err)
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("list AI routes: HTTP %d", statusCode)
	}

	var listResp struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &listResp); err != nil {
		return fmt.Errorf("decode AI routes list: %w", err)
	}

	const maxRetries = 5

	for _, raw := range listResp.Data {
		var routeInfo struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &routeInfo); err != nil || routeInfo.Name == "" {
			continue
		}

		for attempt := 0; attempt < maxRetries; attempt++ {
			if err := ctx.Err(); err != nil {
				return err
			}

			routeBody, sc, err := c.doJSON(ctx, http.MethodGet,
				"/v1/ai/routes/"+routeInfo.Name, nil)
			if err != nil || sc != http.StatusOK {
				break
			}

			var routeResp struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(routeBody, &routeResp); err != nil {
				break
			}
			routeData := routeResp.Data
			if routeData == nil {
				routeData = routeBody
			}

			var route map[string]interface{}
			if err := json.Unmarshal(routeData, &route); err != nil {
				break
			}

			authConfig, _ := route["authConfig"].(map[string]interface{})
			if authConfig == nil {
				authConfig = make(map[string]interface{})
			}

			consumers := toStringSlice(authConfig["allowedConsumers"])

			if add {
				if !containsString(consumers, consumerName) {
					consumers = append(consumers, consumerName)
					authConfig["allowedConsumers"] = consumers
					route["authConfig"] = authConfig
				}
				// Always PUT to trigger WASM key-auth resync — the consumer
				// may have been created after the route was last written,
				// so WASM needs to reload credentials even if the name was
				// already in allowedConsumers.
			} else {
				consumers = removeString(consumers, consumerName)
			}

			authConfig["allowedConsumers"] = consumers
			route["authConfig"] = authConfig

			_, sc, err = c.doJSON(ctx, http.MethodPut,
				"/v1/ai/routes/"+routeInfo.Name, route)
			if err != nil {
				break
			}
			if sc == http.StatusOK {
				break
			}
			if sc == http.StatusConflict {
				time.Sleep(time.Duration(rand.Intn(3)+1) * time.Second)
				continue
			}
			break
		}
	}

	return nil
}

func (c *HigressClient) AuthorizeMCPServers(ctx context.Context, consumerName string, mcpServers []string) ([]string, error) {
	return c.modifyMCPServers(ctx, consumerName, mcpServers, true)
}

func (c *HigressClient) DeauthorizeMCPServers(ctx context.Context, consumerName string, mcpServers []string) error {
	_, err := c.modifyMCPServers(ctx, consumerName, mcpServers, false)
	return err
}

func (c *HigressClient) modifyMCPServers(ctx context.Context, consumerName string, mcpServers []string, add bool) ([]string, error) {
	allMCP, err := c.listMCPServers(ctx)
	if err != nil {
		return nil, err
	}

	existingNames := make(map[string]bool)
	for _, m := range allMCP {
		existingNames[m.Name] = true
	}

	// Determine target list
	targets := mcpServers
	if len(targets) == 0 && add {
		for name := range existingNames {
			targets = append(targets, name)
		}
	}

	var resolved []string

	for _, mcpName := range targets {
		mcpName = strings.TrimSpace(mcpName)
		if mcpName == "" {
			continue
		}
		if !existingNames[mcpName] {
			continue
		}

		// Re-fetch latest state to minimize race window
		freshMCP, err := c.listMCPServers(ctx)
		if err != nil {
			continue
		}

		var currentConsumers []string
		for _, m := range freshMCP {
			if m.Name == mcpName {
				currentConsumers = m.AllowedConsumers
				break
			}
		}

		var newConsumers []string
		if add {
			// Always include "manager", then existing (minus target), then target
			newConsumers = []string{"manager"}
			for _, ec := range currentConsumers {
				if ec == "manager" || ec == consumerName {
					continue
				}
				newConsumers = append(newConsumers, ec)
			}
			newConsumers = append(newConsumers, consumerName)
		} else {
			for _, ec := range currentConsumers {
				if ec != consumerName {
					newConsumers = append(newConsumers, ec)
				}
			}
		}

		body := map[string]interface{}{
			"mcpServerName": mcpName,
			"consumers":     newConsumers,
		}
		_, sc, err := c.doJSON(ctx, http.MethodPut, "/v1/mcpServer/consumers", body)
		if err != nil || (sc != http.StatusOK && sc != http.StatusNoContent) {
			continue
		}
		resolved = append(resolved, mcpName)
	}

	return resolved, nil
}

func (c *HigressClient) ExposePort(ctx context.Context, req PortExposeRequest) error {
	svcSrc := fmt.Sprintf("worker-%s-%d", req.WorkerName, req.Port)
	routeN := svcSrc
	domain := req.Domain
	if domain == "" {
		domain = fmt.Sprintf("worker-%s-%d-local.hiclaw.io", req.WorkerName, req.Port)
	}
	dnsHost := req.ServiceHost
	if dnsHost == "" {
		dnsHost = fmt.Sprintf("%s.local", req.WorkerName)
	}

	if err := c.ensureDomain(ctx, domain); err != nil {
		return fmt.Errorf("expose port %d: %w", req.Port, err)
	}
	if err := c.ensureServiceSource(ctx, svcSrc, dnsHost, req.Port, "http"); err != nil {
		return fmt.Errorf("expose port %d: %w", req.Port, err)
	}
	if err := c.ensureRoute(ctx, routeN, []string{domain}, svcSrc+".dns", req.Port, "/"); err != nil {
		return fmt.Errorf("expose port %d: %w", req.Port, err)
	}
	return nil
}

func (c *HigressClient) UnexposePort(ctx context.Context, req PortExposeRequest) error {
	svcSrc := fmt.Sprintf("worker-%s-%d", req.WorkerName, req.Port)
	routeN := svcSrc
	domain := req.Domain
	if domain == "" {
		domain = fmt.Sprintf("worker-%s-%d-local.hiclaw.io", req.WorkerName, req.Port)
	}

	c.deleteRoute(ctx, routeN)
	c.deleteServiceSource(ctx, svcSrc)
	c.deleteDomain(ctx, domain)
	return nil
}

// --- Public infrastructure init methods (used by Initializer) ---

func (c *HigressClient) EnsureServiceSource(ctx context.Context, name, domain string, port int, protocol string) error {
	return c.ensureServiceSource(ctx, name, domain, port, protocol)
}

func (c *HigressClient) EnsureStaticServiceSource(ctx context.Context, name, address string, port int) error {
	return c.ensureStaticServiceSource(ctx, name, address, port)
}

func (c *HigressClient) EnsureRoute(ctx context.Context, name string, domains []string, serviceName string, port int, pathPrefix string) error {
	return c.ensureRoute(ctx, name, domains, serviceName, port, pathPrefix)
}

func (c *HigressClient) DeleteRoute(ctx context.Context, name string) error {
	c.deleteRoute(ctx, name)
	return nil
}

func (c *HigressClient) EnsureAIProvider(ctx context.Context, req AIProviderRequest) error {
	body := map[string]interface{}{
		"name":     req.Name,
		"type":     req.Type,
		"tokens":   req.Tokens,
		"protocol": req.Protocol,
	}
	if req.Raw != nil {
		body["rawConfigs"] = req.Raw
	}
	_, sc, err := c.doJSON(ctx, http.MethodPost, "/v1/ai/providers", body)
	if err != nil {
		return fmt.Errorf("ensure AI provider %s: %w", req.Name, err)
	}
	if sc == 200 || sc == 201 || sc == 409 {
		return nil
	}
	return fmt.Errorf("ensure AI provider %s: HTTP %d", req.Name, sc)
}

func (c *HigressClient) EnsureAIRoute(ctx context.Context, req AIRouteRequest) error {
	body := map[string]interface{}{
		"name":    req.Name,
		"domains": []string{},
		"pathPredicate": map[string]interface{}{
			"matchType":     "PRE",
			"matchValue":    req.PathPrefix,
			"caseSensitive": false,
		},
		"upstreams": []map[string]interface{}{
			{"provider": req.Provider, "weight": 100, "modelMapping": map[string]interface{}{}},
		},
	}
	// Always enable key-auth on AI routes so the auth framework is in place.
	// Consumers are bound later via AuthorizeAIRoutes after they are created.
	body["authConfig"] = map[string]interface{}{
		"enabled":                true,
		"allowedCredentialTypes": []string{"key-auth"},
		"allowedConsumers":       req.AllowedConsumers,
	}
	_, sc, err := c.doJSON(ctx, http.MethodPost, "/v1/ai/routes", body)
	if err != nil {
		return fmt.Errorf("ensure AI route %s: %w", req.Name, err)
	}
	if sc == 200 || sc == 201 || sc == 409 {
		return nil
	}
	return fmt.Errorf("ensure AI route %s: HTTP %d", req.Name, sc)
}

func (c *HigressClient) Healthy(ctx context.Context) error {
	_, sc, err := c.doJSON(ctx, http.MethodGet, "/v1/consumers", nil)
	if err != nil {
		return err
	}
	if sc != http.StatusOK {
		return fmt.Errorf("higress health check: HTTP %d", sc)
	}
	return nil
}

// ── Higress Console primitives (migrated from controller/higress_client.go) ──

func (c *HigressClient) ensureDomain(ctx context.Context, name string) error {
	body := map[string]interface{}{"name": name, "enableHttps": "off"}
	_, sc, err := c.doJSON(ctx, http.MethodPost, "/v1/domains", body)
	if err != nil {
		return fmt.Errorf("ensure domain %s: %w", name, err)
	}
	if sc != 200 && sc != 201 && sc != 409 {
		return fmt.Errorf("ensure domain %s: HTTP %d", name, sc)
	}
	return nil
}

func (c *HigressClient) ensureServiceSource(ctx context.Context, name, dnsDomain string, port int, protocol string) error {
	if protocol == "" {
		protocol = "http"
	}
	body := map[string]interface{}{
		"type": "dns", "name": name, "domain": dnsDomain,
		"port": port, "protocol": protocol,
		"properties": map[string]interface{}{},
		"authN":      map[string]interface{}{"enabled": false},
	}
	_, sc, err := c.doJSON(ctx, http.MethodPost, "/v1/service-sources", body)
	if err != nil {
		return fmt.Errorf("ensure service source %s: %w", name, err)
	}
	if sc != 200 && sc != 201 && sc != 409 {
		return fmt.Errorf("ensure service source %s: HTTP %d", name, sc)
	}
	return nil
}

func (c *HigressClient) ensureStaticServiceSource(ctx context.Context, name, address string, port int) error {
	body := map[string]interface{}{
		"type": "static", "name": name, "domain": fmt.Sprintf("%s:%d", address, port),
		"port": port, "protocol": "http",
		"properties": map[string]interface{}{},
		"authN":      map[string]interface{}{"enabled": false},
	}
	_, sc, err := c.doJSON(ctx, http.MethodPost, "/v1/service-sources", body)
	if err != nil {
		return fmt.Errorf("ensure static service source %s: %w", name, err)
	}
	if sc != 200 && sc != 201 && sc != 409 {
		return fmt.Errorf("ensure static service source %s: HTTP %d", name, sc)
	}
	return nil
}

func (c *HigressClient) ensureRoute(ctx context.Context, name string, domains []string, serviceName string, port int, pathPrefix string) error {
	if pathPrefix == "" {
		pathPrefix = "/"
	}
	body := map[string]interface{}{
		"name":    name,
		"domains": domains,
		"path":    map[string]interface{}{"matchType": "PRE", "matchValue": pathPrefix, "caseSensitive": false},
		"services": []map[string]interface{}{
			{"name": serviceName, "port": port, "weight": 100},
		},
	}
	_, sc, err := c.doJSON(ctx, http.MethodPost, "/v1/routes", body)
	if err != nil {
		return fmt.Errorf("ensure route %s: %w", name, err)
	}
	if sc == 200 || sc == 201 || sc == 409 {
		return nil
	}
	return fmt.Errorf("ensure route %s: HTTP %d", name, sc)
}

func (c *HigressClient) deleteRoute(ctx context.Context, name string) {
	c.doJSON(ctx, http.MethodDelete, "/v1/routes/"+name, nil)
}

func (c *HigressClient) deleteServiceSource(ctx context.Context, name string) {
	c.doJSON(ctx, http.MethodDelete, "/v1/service-sources/"+name, nil)
}

func (c *HigressClient) deleteDomain(ctx context.Context, name string) {
	c.doJSON(ctx, http.MethodDelete, "/v1/domains/"+name, nil)
}

func (c *HigressClient) listMCPServers(ctx context.Context) ([]MCPServerAuth, error) {
	respBody, sc, err := c.doJSON(ctx, http.MethodGet, "/v1/mcpServer", nil)
	if err != nil {
		return nil, fmt.Errorf("list MCP servers: %w", err)
	}
	if sc != http.StatusOK {
		return nil, fmt.Errorf("list MCP servers: HTTP %d", sc)
	}

	var listResp struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &listResp); err != nil {
		// Might be a plain array
		var arr []json.RawMessage
		if err2 := json.Unmarshal(respBody, &arr); err2 != nil {
			return nil, fmt.Errorf("decode MCP servers: %w", err)
		}
		listResp.Data = arr
	}

	var result []MCPServerAuth
	for _, raw := range listResp.Data {
		var m struct {
			Name             string `json:"name"`
			ConsumerAuthInfo struct {
				AllowedConsumers []string `json:"allowedConsumers"`
			} `json:"consumerAuthInfo"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		result = append(result, MCPServerAuth{
			Name:             m.Name,
			AllowedConsumers: m.ConsumerAuthInfo.AllowedConsumers,
		})
	}
	return result, nil
}

// doJSON performs an HTTP request with session cookies.
func (c *HigressClient) doJSON(ctx context.Context, method, path string, reqBody interface{}) ([]byte, int, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, 0, err
	}

	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := strings.TrimRight(c.config.ConsoleURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.mu.Lock()
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	c.mu.Unlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		c.mu.Lock()
		c.cookies = nil
		c.mu.Unlock()
	}

	respBody, _ := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, nil
}

// ── helpers ──

// TriggerPush asks istiod to push config to Envoy immediately.
// No-op if PilotURL is not configured. Errors are logged but do not propagate.
func (c *HigressClient) TriggerPush() {
	if c.config.PilotURL == "" {
		return
	}
	log.Printf("[gateway] TriggerPush: pushing config to Envoy via %s", c.config.PilotURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.PilotURL+"/debug/adsz?push=true", nil)
	if err != nil {
		log.Printf("[gateway] TriggerPush: build request: %v", err)
		return
	}
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[gateway] TriggerPush: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.Printf("[gateway] TriggerPush: HTTP %d, response: %s", resp.StatusCode, string(body))
}

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

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
