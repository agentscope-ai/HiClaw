package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// DockerConfig holds Docker backend configuration.
type DockerConfig struct {
	SocketPath       string
	WorkerImage      string // default worker image (HICLAW_WORKER_IMAGE)
	CopawWorkerImage string // default copaw worker image (HICLAW_COPAW_WORKER_IMAGE)
	DefaultNetwork   string // default Docker network (default "hiclaw-net")
}

// DockerBackend manages worker containers via the Docker Engine API over a Unix socket.
type DockerBackend struct {
	config          DockerConfig
	client          *http.Client
	containerPrefix string
}

// NewDockerBackend creates a DockerBackend that talks to the given Docker socket.
func NewDockerBackend(config DockerConfig, containerPrefix string) *DockerBackend {
	if containerPrefix == "" {
		containerPrefix = DefaultContainerPrefix
	}
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", config.SocketPath)
		},
	}
	return &DockerBackend{
		config:          config,
		client:          &http.Client{Transport: transport},
		containerPrefix: containerPrefix,
	}
}

func (d *DockerBackend) Name() string                        { return "docker" }
func (d *DockerBackend) NeedsCredentialInjection() bool       { return false }

func (d *DockerBackend) Available(ctx context.Context) bool {
	// Check socket file exists
	if _, err := os.Stat(d.config.SocketPath); err != nil {
		return false
	}
	// Ping the Docker daemon
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, "http://localhost/_ping", nil)
	if err != nil {
		return false
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (d *DockerBackend) Create(ctx context.Context, req CreateRequest) (*WorkerResult, error) {
	containerName := d.containerPrefix + req.Name

	// Default image fallback
	image := req.Image
	if image == "" {
		if req.Runtime == RuntimeCopaw && d.config.CopawWorkerImage != "" {
			image = d.config.CopawWorkerImage
		} else {
			image = d.config.WorkerImage
		}
	}
	req.Image = image

	// Default network fallback
	if req.Network == "" && d.config.DefaultNetwork != "" {
		req.Network = d.config.DefaultNetwork
	}

	// Infer WorkingDir from HOME env if not set
	if req.WorkingDir == "" {
		if home, ok := req.Env["HOME"]; ok {
			req.WorkingDir = home
		}
	}

	payload := d.buildCreatePayload(req)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal create payload: %w", err)
	}

	u := fmt.Sprintf("http://localhost/containers/create?name=%s", url.QueryEscape(containerName))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("build create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("docker create: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("%w: container %q", ErrConflict, containerName)
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("docker create failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var createResp struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		return nil, fmt.Errorf("parse create response: %w", err)
	}

	if err := d.startContainer(ctx, createResp.ID); err != nil {
		return nil, fmt.Errorf("start after create: %w", err)
	}

	return &WorkerResult{
		Name:        req.Name,
		Backend:     "docker",
		Status:      StatusRunning,
		ContainerID: createResp.ID,
		RawStatus:   "running",
	}, nil
}

func (d *DockerBackend) Delete(ctx context.Context, name string) error {
	containerName := d.containerPrefix + name
	u := fmt.Sprintf("http://localhost/containers/%s?force=true", url.PathEscape(containerName))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker delete failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (d *DockerBackend) Start(ctx context.Context, name string) error {
	containerName := d.containerPrefix + name
	if err := d.startContainer(ctx, containerName); err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return fmt.Errorf("%w: worker %q", ErrNotFound, name)
		}
		return err
	}
	return nil
}

func (d *DockerBackend) Stop(ctx context.Context, name string) error {
	containerName := d.containerPrefix + name
	u := fmt.Sprintf("http://localhost/containers/%s/stop?t=10", url.PathEscape(containerName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker stop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: worker %q", ErrNotFound, name)
	}
	if resp.StatusCode == http.StatusNotModified {
		return nil // already stopped
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker stop failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (d *DockerBackend) Status(ctx context.Context, name string) (*WorkerResult, error) {
	containerName := d.containerPrefix + name
	u := fmt.Sprintf("http://localhost/containers/%s/json", url.PathEscape(containerName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker inspect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &WorkerResult{
			Name:    name,
			Backend: "docker",
			Status:  StatusNotFound,
		}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker inspect failed (status %d): %s", resp.StatusCode, string(body))
	}

	var inspectResp struct {
		ID    string `json:"Id"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	if err := json.Unmarshal(body, &inspectResp); err != nil {
		return nil, fmt.Errorf("parse inspect response: %w", err)
	}

	return &WorkerResult{
		Name:        name,
		Backend:     "docker",
		Status:      normalizeDockerStatus(inspectResp.State.Status),
		ContainerID: inspectResp.ID,
		RawStatus:   inspectResp.State.Status,
	}, nil
}

func (d *DockerBackend) List(ctx context.Context) ([]WorkerResult, error) {
	filters, _ := json.Marshal(map[string][]string{
		"name": {d.containerPrefix},
	})
	u := fmt.Sprintf("http://localhost/containers/json?all=true&filters=%s", url.QueryEscape(string(filters)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker list: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker list failed (status %d): %s", resp.StatusCode, string(body))
	}

	var containers []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
		State string   `json:"State"`
	}
	if err := json.Unmarshal(body, &containers); err != nil {
		return nil, fmt.Errorf("parse list response: %w", err)
	}

	results := make([]WorkerResult, 0, len(containers))
	for _, c := range containers {
		name := ""
		for _, n := range c.Names {
			n = strings.TrimPrefix(n, "/")
			if strings.HasPrefix(n, d.containerPrefix) {
				name = strings.TrimPrefix(n, d.containerPrefix)
				break
			}
		}
		if name == "" {
			continue
		}
		results = append(results, WorkerResult{
			Name:        name,
			Backend:     "docker",
			Status:      normalizeDockerStatus(c.State),
			ContainerID: c.ID,
			RawStatus:   c.State,
		})
	}
	return results, nil
}

// --- internal helpers ---

func (d *DockerBackend) startContainer(ctx context.Context, nameOrID string) error {
	u := fmt.Sprintf("http://localhost/containers/%s/start", url.PathEscape(nameOrID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker start: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil // already running
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("docker start failed (status 404): container not found")
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker start failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// dockerCreatePayload is the Docker Engine API container create body.
type dockerCreatePayload struct {
	Image      string            `json:"Image"`
	Env        []string          `json:"Env,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	HostConfig *dockerHostConfig `json:"HostConfig,omitempty"`
}

type dockerHostConfig struct {
	NetworkMode string   `json:"NetworkMode,omitempty"`
	ExtraHosts  []string `json:"ExtraHosts,omitempty"`
}

func (d *DockerBackend) buildCreatePayload(req CreateRequest) dockerCreatePayload {
	// Sort env keys for deterministic output
	keys := make([]string, 0, len(req.Env))
	for k := range req.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	envList := make([]string, 0, len(req.Env))
	for _, k := range keys {
		envList = append(envList, k+"="+req.Env[k])
	}

	p := dockerCreatePayload{
		Image:      req.Image,
		Env:        envList,
		WorkingDir: req.WorkingDir,
	}

	if req.Network != "" || len(req.ExtraHosts) > 0 {
		p.HostConfig = &dockerHostConfig{
			NetworkMode: req.Network,
			ExtraHosts:  req.ExtraHosts,
		}
	}

	return p
}

func normalizeDockerStatus(status string) WorkerStatus {
	switch strings.ToLower(status) {
	case "running":
		return StatusRunning
	case "exited", "dead":
		return StatusStopped
	case "created", "restarting":
		return StatusStarting
	default:
		return StatusUnknown
	}
}
