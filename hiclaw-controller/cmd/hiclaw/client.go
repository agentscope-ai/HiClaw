package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// APIClient is a thin HTTP wrapper for the hiclaw-controller REST API.
type APIClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// APIError represents a non-2xx response from the controller.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// NewAPIClient constructs a client from environment variables.
func NewAPIClient() *APIClient {
	baseURL := os.Getenv("HICLAW_CONTROLLER_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8090"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &APIClient{
		BaseURL: baseURL,
		Token:   discoverToken(),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// discoverToken returns a bearer token using a multi-level fallback:
//  1. HICLAW_AUTH_TOKEN env var (Agent containers, injected by Reconciler)
//  2. /var/run/hiclaw/token file (embedded controller)
//  3. K8s SA projected token (incluster pods)
//  4. empty string (unauthenticated, for controllers with auth disabled)
func discoverToken() string {
	if token := os.Getenv("HICLAW_AUTH_TOKEN"); token != "" {
		return token
	}
	for _, path := range []string{
		"/var/run/hiclaw/token",
		"/var/run/secrets/kubernetes.io/serviceaccount/token",
	} {
		if data, err := os.ReadFile(path); err == nil {
			if t := strings.TrimSpace(string(data)); t != "" {
				return t
			}
		}
	}
	return ""
}

// Do sends an HTTP request and returns the raw response.
// body may be nil for methods that have no request body.
func (c *APIClient) Do(method, path string, body interface{}) (*http.Response, error) {
	url := c.BaseURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return c.HTTPClient.Do(req)
}

// DoJSON sends a request, checks for 2xx, and decodes the response body into result.
// result may be nil if the caller does not need the response body (e.g. DELETE → 204).
func (c *APIClient) DoJSON(method, path string, body, result interface{}) error {
	resp, err := c.Do(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		// Try to extract "error" field from JSON error response
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
