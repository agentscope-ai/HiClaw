package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PackageResolver handles file://, http(s)://, and nacos:// package URIs.
type PackageResolver struct {
	ImportDir  string // e.g. /tmp/import
	ExtractDir string // e.g. /tmp/import/extracted
}

func NewPackageResolver(importDir string) *PackageResolver {
	extractDir := filepath.Join(importDir, "extracted")
	os.MkdirAll(extractDir, 0755)
	return &PackageResolver{ImportDir: importDir, ExtractDir: extractDir}
}

// Resolve downloads or locates a package and returns the local ZIP path.
// Supported schemes: file://, http://, https://, nacos://
func (p *PackageResolver) Resolve(ctx context.Context, uri string) (string, error) {
	if uri == "" {
		return "", nil
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("invalid package URI %q: %w", uri, err)
	}

	switch parsed.Scheme {
	case "file":
		return p.resolveFile(parsed)
	case "http", "https":
		return p.resolveHTTP(ctx, uri)
	case "nacos":
		return p.resolveNacos(ctx, parsed)
	default:
		// Treat as relative MinIO path (e.g. "packages/alice.zip")
		// First check local import dir
		localPath := filepath.Join(p.ImportDir, filepath.Base(uri))
		if _, err := os.Stat(localPath); err == nil {
			return localPath, nil
		}
		// Download from MinIO hiclaw-config/ prefix
		storagePrefix := os.Getenv("HICLAW_STORAGE_PREFIX")
		if storagePrefix == "" {
			storagePrefix = "hiclaw/hiclaw-storage"
		}
		minioPath := fmt.Sprintf("%s/hiclaw-config/%s", storagePrefix, uri)
		destPath := filepath.Join(p.ImportDir, filepath.Base(uri))
		cmd := exec.CommandContext(ctx, "mc", "cp", minioPath, destPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to download %s from MinIO: %s: %w", minioPath, string(out), err)
		}
		return destPath, nil
	}
}

// ResolveAndExtract downloads/locates a package, extracts it, and returns the
// extracted directory path. The directory follows the standard package layout:
//
//	{extractDir}/{name}/
//	├── config/
//	│   ├── SOUL.md
//	│   └── AGENTS.md (optional)
//	├── skills/ (optional)
//	└── Dockerfile (optional)
func (p *PackageResolver) ResolveAndExtract(ctx context.Context, uri, name string) (string, error) {
	if uri == "" {
		return "", nil
	}

	zipPath, err := p.Resolve(ctx, uri)
	if err != nil {
		return "", fmt.Errorf("resolve package: %w", err)
	}

	destDir := filepath.Join(p.ExtractDir, name)
	os.RemoveAll(destDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create extract dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "unzip", "-q", "-o", zipPath, "-d", destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract ZIP %s: %s: %w", zipPath, string(out), err)
	}

	// Validate: SOUL.md must exist (check both root and config/ subdirectory)
	soulPaths := []string{
		filepath.Join(destDir, "SOUL.md"),
		filepath.Join(destDir, "config", "SOUL.md"),
	}
	found := false
	for _, sp := range soulPaths {
		if _, err := os.Stat(sp); err == nil {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("invalid package: SOUL.md not found in %s (checked root and config/)", destDir)
	}

	return destDir, nil
}

// DeployToMinIO copies extracted package contents to the worker's MinIO agent space.
// This ensures SOUL.md, custom skills, etc. are in place before create-worker.sh runs.
func (p *PackageResolver) DeployToMinIO(ctx context.Context, extractedDir, workerName string) error {
	agentDir := fmt.Sprintf("/root/hiclaw-fs/agents/%s", workerName)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// Copy config/ contents (SOUL.md, AGENTS.md, etc.) to agent root
	configDir := filepath.Join(extractedDir, "config")
	if info, err := os.Stat(configDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(configDir)
		for _, e := range entries {
			src := filepath.Join(configDir, e.Name())
			dst := filepath.Join(agentDir, e.Name())
			if data, err := os.ReadFile(src); err == nil {
				os.WriteFile(dst, data, 0644)
			}
		}
	} else {
		// Fallback: SOUL.md at root level
		src := filepath.Join(extractedDir, "SOUL.md")
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(filepath.Join(agentDir, "SOUL.md"), data, 0644)
		}
	}

	// Copy custom skills/ directory if present
	skillsDir := filepath.Join(extractedDir, "skills")
	if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
		cpCmd := exec.CommandContext(ctx, "cp", "-r", skillsDir, filepath.Join(agentDir, "custom-skills"))
		cpCmd.CombinedOutput()
	}

	// Push to MinIO
	storagePrefix := os.Getenv("HICLAW_STORAGE_PREFIX")
	if storagePrefix == "" {
		storagePrefix = "hiclaw/hiclaw-storage"
	}
	minioDest := fmt.Sprintf("%s/agents/%s/", storagePrefix, workerName)
	mcCmd := exec.CommandContext(ctx, "mc", "mirror", agentDir+"/", minioDest, "--overwrite")
	if out, err := mcCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mc mirror to %s failed: %s: %w", minioDest, string(out), err)
	}

	return nil
}

// --- Private resolve methods ---

func (p *PackageResolver) resolveFile(u *url.URL) (string, error) {
	filename := filepath.Base(u.Path)
	localPath := filepath.Join(p.ImportDir, filename)

	if _, err := os.Stat(localPath); err != nil {
		if _, err2 := os.Stat(u.Path); err2 != nil {
			return "", fmt.Errorf("file package not found at %s or %s", localPath, u.Path)
		}
		return u.Path, nil
	}
	return localPath, nil
}

func (p *PackageResolver) resolveHTTP(ctx context.Context, uri string) (string, error) {
	filename := filepath.Base(uri)
	if !strings.HasSuffix(filename, ".zip") {
		filename += ".zip"
	}
	destPath := filepath.Join(p.ImportDir, filename)

	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for %s: %w", uri, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download %s: %w", uri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s returned status %d", uri, resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create %s: %w", destPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("failed to write %s: %w", destPath, err)
	}

	return destPath, nil
}

// resolveNacos pulls a package from Nacos configuration center.
// URI format: nacos://{instance-id}/{namespace}/{group}/{data-id}/{version}
func (p *PackageResolver) resolveNacos(ctx context.Context, u *url.URL) (string, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid nacos URI: expected nacos://{instance}/{namespace}/{group}/{data-id}[/{version}], got %s", u.String())
	}

	instanceID := u.Host
	namespace := parts[0]
	group := parts[1]
	dataID := parts[2]
	version := ""
	if len(parts) >= 4 {
		version = parts[3]
	}

	nacosAddr := os.Getenv("HICLAW_NACOS_ADDR")
	if nacosAddr == "" {
		return "", fmt.Errorf("HICLAW_NACOS_ADDR not set (required for nacos:// packages, instance=%s)", instanceID)
	}
	nacosToken := os.Getenv("HICLAW_NACOS_TOKEN")

	apiURL := fmt.Sprintf("%s/nacos/v1/cs/configs?tenant=%s&group=%s&dataId=%s",
		strings.TrimRight(nacosAddr, "/"),
		url.QueryEscape(namespace),
		url.QueryEscape(group),
		url.QueryEscape(dataID),
	)
	if version != "" {
		apiURL += "&tag=" + url.QueryEscape(version)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create nacos request: %w", err)
	}
	if nacosToken != "" {
		req.Header.Set("accessToken", nacosToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch from nacos (%s): %w", apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nacos returned status %d for %s/%s/%s", resp.StatusCode, namespace, group, dataID)
	}

	destName := fmt.Sprintf("%s-%s.zip", dataID, version)
	if version == "" {
		destName = dataID + ".zip"
	}
	destPath := filepath.Join(p.ImportDir, destName)

	out, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create %s: %w", destPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("failed to write %s: %w", destPath, err)
	}

	return destPath, nil
}
