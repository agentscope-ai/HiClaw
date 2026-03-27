package executor

import (
	"context"
	"crypto/sha256"
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

// Resolve downloads or locates a package and returns the local path.
// For nacos:// URIs the result is a directory; for all others it is a ZIP file.
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
	case "oss":
		return p.resolveOSS(ctx, parsed)
	default:
		// Treat as relative MinIO path (e.g. "packages/alice.zip")
		// Use content-addressable cache: download to /tmp/import/{md5}.zip
		// If the same content already exists locally, skip re-download.
		storagePrefix := os.Getenv("HICLAW_STORAGE_PREFIX")
		if storagePrefix == "" {
			storagePrefix = "hiclaw/hiclaw-storage"
		}
		minioPath := fmt.Sprintf("%s/hiclaw-config/%s", storagePrefix, uri)

		// Get remote file's ETag (MD5) via mc stat
		etag := getMinIOETag(ctx, minioPath)
		if etag == "" {
			// Fallback: use URI hash if mc stat fails
			h := sha256.Sum256([]byte(uri))
			etag = fmt.Sprintf("%x", h[:8])
		}

		destPath := filepath.Join(p.ImportDir, etag+".zip")
		if _, err := os.Stat(destPath); err == nil {
			return destPath, nil // cache hit, same content
		}

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

	resolved, err := p.Resolve(ctx, uri)
	if err != nil {
		return "", fmt.Errorf("resolve package: %w", err)
	}

	// If Resolve already returned a directory (e.g. nacos://), use it directly.
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		if err := validatePackageDir(resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}

	// Otherwise treat as ZIP and extract.
	destDir := filepath.Join(p.ExtractDir, name)
	os.RemoveAll(destDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create extract dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "unzip", "-q", "-o", resolved, "-d", destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract ZIP %s: %s: %w", resolved, string(out), err)
	}

	if err := validatePackageDir(destDir); err != nil {
		return "", err
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
	// For AGENTS.md: wrap custom content with builtin markers so upgrade-builtins
	// can merge without destroying user content.
	configDir := filepath.Join(extractedDir, "config")
	if info, err := os.Stat(configDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(configDir)
		for _, e := range entries {
			src := filepath.Join(configDir, e.Name())
			dst := filepath.Join(agentDir, e.Name())
			if e.IsDir() {
				// Recursively copy subdirectories (e.g. memory/)
				cpCmd := exec.CommandContext(ctx, "cp", "-r", src, dst)
				cpCmd.CombinedOutput()
				continue
			}
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			if e.Name() == "AGENTS.md" {
				// Wrap user AGENTS.md content after builtin markers
				data = wrapWithBuiltinMarkers(data)
			}
			os.WriteFile(dst, data, 0644)
		}
	} else {
		// Fallback: SOUL.md at root level
		src := filepath.Join(extractedDir, "SOUL.md")
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(filepath.Join(agentDir, "SOUL.md"), data, 0644)
		}
	}

	// Copy custom skills/ directory if present — merge into skills/ alongside builtins
	skillsDir := filepath.Join(extractedDir, "skills")
	if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
		destSkills := filepath.Join(agentDir, "skills")
		os.MkdirAll(destSkills, 0755)
		// Use cp -r with trailing /. to merge contents into existing skills/ dir
		cpCmd := exec.CommandContext(ctx, "cp", "-r", skillsDir+"/.", destSkills+"/")
		cpCmd.CombinedOutput()
	}

	// Copy crons/ to .openclaw/cron/ if present (OpenClaw native cron jobs)
	// If jobs.json is a bare array, wrap it in {"version":1,"jobs":[...]} format
	cronsDir := filepath.Join(extractedDir, "crons")
	if info, err := os.Stat(cronsDir); err == nil && info.IsDir() {
		destCron := filepath.Join(agentDir, ".openclaw", "cron")
		os.MkdirAll(destCron, 0755)
		jobsFile := filepath.Join(cronsDir, "jobs.json")
		if data, err := os.ReadFile(jobsFile); err == nil {
			trimmed := strings.TrimSpace(string(data))
			if strings.HasPrefix(trimmed, "[") {
				// Bare array — wrap in OpenClaw expected format
				wrapped := fmt.Sprintf(`{"version":1,"jobs":%s}`, trimmed)
				os.WriteFile(filepath.Join(destCron, "jobs.json"), []byte(wrapped), 0644)
			} else {
				os.WriteFile(filepath.Join(destCron, "jobs.json"), data, 0644)
			}
		}
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

// validatePackageDir checks that a SOUL.md exists in the package directory.
func validatePackageDir(dir string) error {
	for _, rel := range []string{"SOUL.md", "config/SOUL.md"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("invalid package: SOUL.md not found in %s (checked root and config/)", dir)
}

// wrapWithBuiltinMarkers wraps user AGENTS.md content with hiclaw-builtin markers.
// Uses the same format as builtin-merge.sh (BUILTIN_HEADER + BUILTIN_END) so that
// upgrade-builtins.sh can fill the builtin section without destroying user content.
// If markers already exist, the content is returned as-is.
// YAML frontmatter (---...---) is preserved before the markers.
func wrapWithBuiltinMarkers(data []byte) []byte {
	content := string(data)
	if strings.Contains(content, "<!-- hiclaw-builtin-start -->") {
		return data // already has markers
	}

	var frontmatter, body string
	// Extract YAML frontmatter if present
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---\n"); end >= 0 {
			fmEnd := 4 + end + 4 // past the closing "---\n"
			frontmatter = content[:fmEnd]
			body = content[fmEnd:]
		} else {
			body = content
		}
	} else {
		body = content
	}

	// Match the exact format from builtin-merge.sh BUILTIN_HEADER + BUILTIN_END
	wrapped := ""
	if frontmatter != "" {
		wrapped += frontmatter + "\n"
	}
	wrapped += "<!-- hiclaw-builtin-start -->\n" +
		"> ⚠️ **DO NOT EDIT** this section. It is managed by HiClaw and will be automatically\n" +
		"> replaced on upgrade. To customize, add your content **after** the\n" +
		"> `<!-- hiclaw-builtin-end -->` marker below.\n" +
		"\n" +
		"<!-- hiclaw-builtin-end -->\n\n" +
		body
	return []byte(wrapped)
}

// getMinIOETag returns the ETag (content MD5) of a MinIO object via mc stat.
// Returns empty string if mc stat fails.
func getMinIOETag(ctx context.Context, minioPath string) string {
	cmd := exec.CommandContext(ctx, "mc", "stat", "--json", minioPath)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// mc stat --json outputs {"etag":"xxx",...}
	// Simple extraction without json dependency
	s := string(out)
	if idx := strings.Index(s, `"etag":"`); idx >= 0 {
		rest := s[idx+8:]
		if end := strings.Index(rest, `"`); end >= 0 {
			etag := rest[:end]
			// Remove quotes and dashes from ETag
			etag = strings.ReplaceAll(etag, "-", "")
			if etag != "" {
				return etag
			}
		}
	}
	return ""
}

// --- Private resolve methods ---

// resolveOSS downloads a package from MinIO/OSS storage.
// URI format: oss://hiclaw-config/packages/{name}-{md5}.zip
// The filename contains the content hash, so it's naturally content-addressable:
// same hash → same content → cache hit.
func (p *PackageResolver) resolveOSS(ctx context.Context, u *url.URL) (string, error) {
	// oss://hiclaw-config/packages/alice-abc123.zip → hiclaw-config/packages/alice-abc123.zip
	ossPath := strings.TrimPrefix(u.Host+u.Path, "/")
	filename := filepath.Base(ossPath)

	// Content-addressable cache: filename includes MD5, so same file = cache hit
	destPath := filepath.Join(p.ImportDir, filename)
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil // cache hit
	}

	// Download from MinIO
	storagePrefix := os.Getenv("HICLAW_STORAGE_PREFIX")
	if storagePrefix == "" {
		storagePrefix = "hiclaw/hiclaw-storage"
	}
	minioPath := fmt.Sprintf("%s/%s", storagePrefix, ossPath)

	cmd := exec.CommandContext(ctx, "mc", "cp", minioPath, destPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to download oss://%s from MinIO: %s: %w", ossPath, string(out), err)
	}

	return destPath, nil
}

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

// resolveNacos downloads a Worker template from Nacos via the AgentSpec client API.
// URI format: nacos://{instance-id}/{namespace}/{agentspec-name}[/{version}]
// Requires HICLAW_NACOS_ADDR for connection info (supports user:pass@host:port for auth).
func (p *PackageResolver) resolveNacos(ctx context.Context, u *url.URL) (string, error) {
	// Parse URI path segments: /{namespace}/{agentspec-name}/{version}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid nacos URI: expected nacos://{instance}/{namespace}/{agentspec-name}[/{version}], got %s", u.String())
	}

	instanceID := u.Host
	namespace := parts[0]
	specName := parts[1]
	version := ""
	if len(parts) >= 3 {
		version = parts[2]
	}

	nacosAddr := os.Getenv("HICLAW_NACOS_ADDR")
	if nacosAddr == "" {
		return "", fmt.Errorf("HICLAW_NACOS_ADDR not set (required for nacos:// packages, instance=%s)", instanceID)
	}

	outputDir := filepath.Join(p.ImportDir, "nacos")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create nacos import dir %s: %w", outputDir, err)
	}
	destPath := filepath.Join(outputDir, specName)
	if err := os.RemoveAll(destPath); err != nil {
		return "", fmt.Errorf("failed to clean previous nacos package %s: %w", destPath, err)
	}

	client, err := newNacosAgentSpecClient(ctx, nacosAddr, namespace)
	if err != nil {
		return "", err
	}

	label := ""
	if strings.HasPrefix(version, "label:") {
		label = strings.TrimPrefix(version, "label:")
		version = ""
	}

	if err := client.GetAgentSpec(ctx, specName, outputDir, version, label); err != nil {
		return "", fmt.Errorf("fetch agentspec %s from nacos failed: %w", specName, err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return "", fmt.Errorf("agentspec download finished but %s was not created: %w", destPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agentspec output %s is not a directory", destPath)
	}

	return destPath, nil
}
