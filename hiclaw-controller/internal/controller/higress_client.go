package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// HigressClient wraps the Higress Console REST API.
type HigressClient struct {
	BaseURL    string // e.g. http://127.0.0.1:8001
	CookieFile string // path to session cookie file

	mu      sync.Mutex
	cookies []*http.Cookie
}

// EnsureSession logs in to Higress Console and caches the session cookie.
func (c *HigressClient) EnsureSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.cookies) > 0 {
		return nil
	}

	adminUser := os.Getenv("HICLAW_ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPassword := os.Getenv("HICLAW_ADMIN_PASSWORD")
	if adminPassword == "" {
		adminPassword = "admin"
	}

	body := fmt.Sprintf(`{"username":%q,"password":%q}`, adminUser, adminPassword)
	resp, err := http.Post(c.BaseURL+"/session/login", "application/json", strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("higress login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("higress login: HTTP %d", resp.StatusCode)
	}

	c.cookies = resp.Cookies()
	return nil
}

// EnsureDomain creates a domain if it doesn't exist (409 = already exists, OK).
func (c *HigressClient) EnsureDomain(name string) error {
	body := fmt.Sprintf(`{"name":%q,"enableHttps":"off"}`, name)
	_, statusCode, err := c.doRequest("POST", "/v1/domains", body)
	if err != nil {
		return fmt.Errorf("ensure domain %s: %w", name, err)
	}
	if statusCode != 200 && statusCode != 201 && statusCode != 409 {
		return fmt.Errorf("ensure domain %s: HTTP %d", name, statusCode)
	}
	return nil
}

// EnsureServiceSource creates a DNS service source if it doesn't exist.
func (c *HigressClient) EnsureServiceSource(name, dnsDomain string, port int) error {
	body := fmt.Sprintf(`{"type":"dns","name":%q,"domain":%q,"port":%d,"protocol":"http","properties":{},"authN":{"enabled":false}}`,
		name, dnsDomain, port)
	respBody, statusCode, err := c.doRequest("POST", "/v1/service-sources", body)
	if err != nil {
		return fmt.Errorf("ensure service source %s: %w", name, err)
	}
	if statusCode != 200 && statusCode != 201 && statusCode != 409 {
		return fmt.Errorf("ensure service source %s: HTTP %d: %s", name, statusCode, string(respBody))
	}
	return nil
}

// EnsureRoute creates or updates a route.
func (c *HigressClient) EnsureRoute(name string, domains []string, serviceName string, port int) error {
	domainsJSON, _ := json.Marshal(domains)
	routeBody := fmt.Sprintf(`{
		"name":%q,
		"domains":%s,
		"path":{"matchType":"PRE","matchValue":"/","caseSensitive":false},
		"services":[{"name":%q,"port":%d,"weight":100}]
	}`, name, string(domainsJSON), serviceName, port)

	// Try create first
	_, statusCode, err := c.doRequest("POST", "/v1/routes", routeBody)
	if err != nil {
		return fmt.Errorf("ensure route %s: %w", name, err)
	}
	if statusCode == 200 || statusCode == 201 || statusCode == 409 {
		return nil
	}
	return fmt.Errorf("ensure route %s: HTTP %d", name, statusCode)
}

// DeleteRoute deletes a route by name. Returns nil if not found.
func (c *HigressClient) DeleteRoute(name string) error {
	_, statusCode, err := c.doRequest("DELETE", "/v1/routes/"+name, "")
	if err != nil {
		return fmt.Errorf("delete route %s: %w", name, err)
	}
	if statusCode != 204 && statusCode != 200 && statusCode != 404 {
		return fmt.Errorf("delete route %s: HTTP %d", name, statusCode)
	}
	return nil
}

// DeleteServiceSource deletes a service source by name. Returns nil if not found.
func (c *HigressClient) DeleteServiceSource(name string) error {
	_, statusCode, err := c.doRequest("DELETE", "/v1/service-sources/"+name, "")
	if err != nil {
		return fmt.Errorf("delete service source %s: %w", name, err)
	}
	if statusCode != 204 && statusCode != 200 && statusCode != 404 {
		return fmt.Errorf("delete service source %s: HTTP %d", name, statusCode)
	}
	return nil
}

// DeleteDomain deletes a domain by name. Returns nil if not found.
func (c *HigressClient) DeleteDomain(name string) error {
	_, statusCode, err := c.doRequest("DELETE", "/v1/domains/"+name, "")
	if err != nil {
		return fmt.Errorf("delete domain %s: %w", name, err)
	}
	if statusCode != 204 && statusCode != 200 && statusCode != 404 {
		return fmt.Errorf("delete domain %s: HTTP %d", name, statusCode)
	}
	return nil
}

// doRequest performs an HTTP request with session cookies.
func (c *HigressClient) doRequest(method, path, body string) ([]byte, int, error) {
	if err := c.EnsureSession(); err != nil {
		return nil, 0, err
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	c.mu.Lock()
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	c.mu.Unlock()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	// If session expired, clear cookies so next call re-authenticates
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		c.mu.Lock()
		c.cookies = nil
		c.mu.Unlock()
	}

	respBody, _ := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, nil
}
