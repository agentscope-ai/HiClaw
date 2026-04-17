package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func createCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a resource",
	}
	cmd.AddCommand(createWorkerCmd())
	cmd.AddCommand(createTeamCmd())
	cmd.AddCommand(createHumanCmd())
	cmd.AddCommand(createManagerCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// create worker
// ---------------------------------------------------------------------------

func createWorkerCmd() *cobra.Command {
	var (
		name        string
		model       string
		runtime     string
		image       string
		identity    string
		soul        string
		soulFile    string
		skills      string
		mcpServers  string
		packageURI  string
		expose      string
		team        string
		role        string
		outputFmt   string
		waitTimeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Create a Worker",
		Long: `Create a new Worker resource via the controller REST API.

  hiclaw create worker --name alice --model qwen3.5-plus
  hiclaw create worker --name alice --soul-file /path/to/SOUL.md --skills github-operations
  hiclaw create worker --name bob --model claude-sonnet-4-6 --mcp-servers github -o json
  hiclaw create worker --name charlie --runtime copaw --expose 8080,3000
  hiclaw create worker --name alpha-dev --role team_worker --team alpha`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if err := validateWorkerName(name); err != nil {
				return err
			}
			if model == "" {
				model = "qwen3.5-plus"
			}
			if soulFile != "" {
				data, err := os.ReadFile(soulFile)
				if err != nil {
					return fmt.Errorf("read --soul-file %q: %w", soulFile, err)
				}
				soul = string(data)
			}
			if packageURI != "" {
				var err error
				packageURI, err = expandPackageURI(packageURI)
				if err != nil {
					return err
				}
			}

			req := map[string]interface{}{
				"name":  name,
				"model": model,
			}
			setIfNotEmpty(req, "runtime", runtime)
			setIfNotEmpty(req, "image", image)
			setIfNotEmpty(req, "identity", identity)
			setIfNotEmpty(req, "soul", soul)
			setIfNotEmpty(req, "package", packageURI)
			setIfNotEmpty(req, "teamRef", team)
			setIfNotEmpty(req, "role", role)
			if skills != "" {
				req["skills"] = splitCSV(skills)
			}
			if mcpServers != "" {
				req["mcpServers"] = splitCSV(mcpServers)
			}
			if expose != "" {
				req["expose"] = parseExposePorts(expose)
			}

			client := NewAPIClient()
			var createResp map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/workers", req, &createResp); err != nil {
				return fmt.Errorf("create worker: %w", err)
			}

			finalStatus, err := waitForWorkerReady(client, name, waitTimeout)
			if err != nil {
				return err
			}

			if outputFmt == "json" {
				printJSON(finalStatus)
			} else {
				fmt.Printf("worker/%s ready\n", name)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Worker name (required)")
	cmd.Flags().StringVar(&model, "model", "", "LLM model ID (default: qwen3.5-plus)")
	cmd.Flags().StringVar(&runtime, "runtime", "", "Agent runtime (openclaw|copaw)")
	cmd.Flags().StringVar(&image, "image", "", "Container image override")
	cmd.Flags().StringVar(&identity, "identity", "", "Worker identity description")
	cmd.Flags().StringVar(&soul, "soul", "", "Worker SOUL.md content (inline)")
	cmd.Flags().StringVar(&soulFile, "soul-file", "", "Path to SOUL.md file (overrides --soul)")
	cmd.Flags().StringVar(&skills, "skills", "", "Comma-separated built-in skills")
	cmd.Flags().StringVar(&mcpServers, "mcp-servers", "", "Comma-separated MCP servers")
	cmd.Flags().StringVar(&packageURI, "package", "", "Package URI (nacos://, http://, oss://) or shorthand")
	cmd.Flags().StringVar(&expose, "expose", "", "Comma-separated ports to expose (e.g. 8080,3000)")
	cmd.Flags().StringVar(&team, "team", "", "Team name (sets spec.teamRef)")
	cmd.Flags().StringVar(&role, "role", "", "Worker role (standalone|team_leader|team_worker)")
	cmd.Flags().StringVarP(&outputFmt, "output", "o", "", "Output format (json)")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 3*time.Minute, "Maximum time to wait for the Worker to report Ready")
	return cmd
}

func waitForWorkerReady(client *APIClient, name string, timeout time.Duration) (*workerResp, error) {
	deadline := time.Now().Add(timeout)
	last := &workerResp{Name: name, Phase: "Pending"}

	for {
		var resp workerResp
		err := client.DoJSON("GET", "/api/v1/workers/"+name+"/status", nil, &resp)
		if err == nil {
			last = &resp
			switch resp.Phase {
			case "Ready":
				return &resp, nil
			case "Failed":
				return nil, fmt.Errorf("worker/%s failed during startup: %s", name, renderWorkerStatusSummary(&resp))
			}
		} else {
			var apiErr *APIError
			if !isRetryableWorkerStatusError(err, &apiErr) {
				return nil, fmt.Errorf("wait for worker/%s ready: %w", name, err)
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("worker/%s did not become ready within %s (last status: %s)", name, timeout, renderWorkerStatusSummary(last))
		}

		time.Sleep(2 * time.Second)
	}
}

func isRetryableWorkerStatusError(err error, apiErr **APIError) bool {
	if err == nil {
		return false
	}
	typed, ok := err.(*APIError)
	if !ok {
		return false
	}
	if apiErr != nil {
		*apiErr = typed
	}
	return typed.StatusCode == 404 || typed.StatusCode >= 500
}

func renderWorkerStatusSummary(resp *workerResp) string {
	if resp == nil {
		return "unknown"
	}

	parts := []string{}
	if phase := strings.TrimSpace(resp.Phase); phase != "" {
		parts = append(parts, "phase="+phase)
	}
	if state := strings.TrimSpace(resp.ContainerState); state != "" {
		parts = append(parts, "state="+state)
	}
	if msg := strings.TrimSpace(resp.Message); msg != "" {
		parts = append(parts, "message="+msg)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// create team — calls POST /api/v1/bundles/team (TeamBundleRequest).
//
// Server-side the bundle endpoint creates Team CR, Leader Worker CR, each
// Member Worker CR, and patches Human.teamAccess for every --admins entry.
// It always responds with 207 Multi-Status carrying per-resource outcomes
// (or 400 with validation items when the dry-run pass fails). This CLI
// decodes the body uniformly via decodeBundleResponse and prints it.
// ---------------------------------------------------------------------------

// workerItem is a single parsed entry from --workers CSV.
type workerItem struct {
	Name  string
	Model string // empty → fallback to --worker-model
}

// parseWorkersCSV accepts a comma-separated list where each entry is either
// "name" or "name:model". A trailing ":" with an empty model is treated as
// "no override" (equivalent to bare "name"). Empty segments are skipped so
// that a trailing comma does not create a phantom member.
func parseWorkersCSV(raw string) ([]workerItem, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []workerItem
	for _, piece := range strings.Split(raw, ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		nameAndModel := strings.SplitN(piece, ":", 2)
		name := strings.TrimSpace(nameAndModel[0])
		if name == "" {
			return nil, fmt.Errorf("invalid --workers entry %q: missing name", piece)
		}
		model := ""
		if len(nameAndModel) == 2 {
			model = strings.TrimSpace(nameAndModel[1])
		}
		out = append(out, workerItem{Name: name, Model: model})
	}
	return out, nil
}

func createTeamCmd() *cobra.Command {
	var (
		name                 string
		leaderName           string
		leaderModel          string
		leaderHeartbeatEvery string
		workerIdleTimeout    string
		workers              string
		workerModelDefault   string
		admins               string
		description          string
	)

	cmd := &cobra.Command{
		Use:   "team",
		Short: "Create a Team",
		Long: `Create a new Team together with its Leader and Member Workers in one call.

The command targets POST /api/v1/bundles/team; the controller creates the
Team CR, the Leader Worker CR, each Member Worker CR, and patches the
specified Admin Humans to include this team in their teamAccess list. A
207 Multi-Status response surfaces per-resource outcomes.

  hiclaw create team --name alpha --leader-name alpha-lead --leader-model claude-sonnet-4-6
  hiclaw create team --name alpha --leader-name alpha-lead --workers alice,bob
  hiclaw create team --name alpha --leader-name alpha-lead \
      --workers alpha-dev:claude-sonnet-4-6,alpha-qa:qwen3.5-plus \
      --admins zhangsan --description "Frontend team"
  hiclaw create team --name alpha --leader-name alpha-lead \
      --leader-heartbeat-every 30m --worker-idle-timeout 12h`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if leaderName == "" {
				return fmt.Errorf("--leader-name is required")
			}

			workerItems, err := parseWorkersCSV(workers)
			if err != nil {
				return err
			}

			leader := map[string]interface{}{
				"name": leaderName,
			}
			setIfNotEmpty(leader, "model", leaderModel)

			workerList := make([]map[string]interface{}, 0, len(workerItems))
			for _, w := range workerItems {
				entry := map[string]interface{}{"name": w.Name}
				model := w.Model
				if model == "" {
					model = workerModelDefault
				}
				setIfNotEmpty(entry, "model", model)
				workerList = append(workerList, entry)
			}

			req := map[string]interface{}{
				"name":   name,
				"leader": leader,
			}
			if len(workerList) > 0 {
				req["workers"] = workerList
			}
			setIfNotEmpty(req, "description", description)
			setIfNotEmpty(req, "workerIdleTimeout", workerIdleTimeout)
			if leaderHeartbeatEvery != "" {
				req["heartbeat"] = map[string]interface{}{
					"enabled": true,
					"every":   leaderHeartbeatEvery,
				}
			}
			if adminList := splitCSV(admins); len(adminList) > 0 {
				req["admins"] = adminList
			}

			client := NewAPIClient()
			resp, statusCode, err := doBundleRequest(client, "POST", "/api/v1/bundles/team", req)
			if err != nil {
				return fmt.Errorf("create team: %w", err)
			}

			fatal := printBundleResponse(resp)
			if fatal {
				if statusCode == 400 {
					return fmt.Errorf("team bundle rejected (HTTP %d): validation failed", statusCode)
				}
				return fmt.Errorf("team bundle partially failed (HTTP %d)", statusCode)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Team name (required)")
	cmd.Flags().StringVar(&leaderName, "leader-name", "", "Leader worker name (required)")
	cmd.Flags().StringVar(&leaderModel, "leader-model", "", "Leader LLM model")
	cmd.Flags().StringVar(&leaderHeartbeatEvery, "leader-heartbeat-every", "", "Leader heartbeat interval (e.g. 30m)")
	cmd.Flags().StringVar(&workerIdleTimeout, "worker-idle-timeout", "", "Idle timeout before the leader may sleep workers (e.g. 12h)")
	cmd.Flags().StringVar(&workers, "workers", "", "Comma-separated worker specs (name or name:model)")
	cmd.Flags().StringVar(&workerModelDefault, "worker-model", "", "Default model for --workers entries without inline :model")
	cmd.Flags().StringVar(&admins, "admins", "", "Comma-separated Human names to grant team admin access")
	cmd.Flags().StringVar(&description, "description", "", "Team description")
	return cmd
}

// doBundleRequest issues a bundle endpoint call and decodes the response
// body regardless of whether the status is 207 (normal) or 400 (dry-run
// validation failure). Both status codes carry a BundleResponse payload,
// so the caller uniformly prints the per-item outcome and decides whether
// the overall call should be considered fatal.
func doBundleRequest(client *APIClient, method, path string, body interface{}) (*bundleResponseWire, int, error) {
	resp, err := client.Do(method, path, body)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	switch {
	case resp.StatusCode == 207 || resp.StatusCode == 400:
		var br bundleResponseWire
		if len(respBody) > 0 {
			if err := json.Unmarshal(respBody, &br); err != nil {
				return nil, resp.StatusCode, fmt.Errorf("decode bundle response: %w", err)
			}
		}
		return &br, resp.StatusCode, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return &bundleResponseWire{}, resp.StatusCode, nil
	default:
		msg := strings.TrimSpace(string(respBody))
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		return nil, resp.StatusCode, &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
}

// ---------------------------------------------------------------------------
// create human
// ---------------------------------------------------------------------------

// parseTeamAccessCSV turns "alpha:admin,beta:member" into []{team, role}.
// Every entry must contain a colon; role must be admin|member.
func parseTeamAccessCSV(raw string) ([]map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []map[string]string
	for _, piece := range strings.Split(raw, ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		parts := strings.SplitN(piece, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --team-access entry %q: expected name:role", piece)
		}
		team := strings.TrimSpace(parts[0])
		role := strings.TrimSpace(parts[1])
		if team == "" {
			return nil, fmt.Errorf("invalid --team-access entry %q: missing team name", piece)
		}
		if role != "admin" && role != "member" {
			return nil, fmt.Errorf("invalid --team-access entry %q: role must be admin or member", piece)
		}
		out = append(out, map[string]string{"team": team, "role": role})
	}
	return out, nil
}

func createHumanCmd() *cobra.Command {
	var (
		name         string
		displayName  string
		email        string
		note         string
		superAdmin   bool
		teamAccess   string
		workerAccess string
	)

	cmd := &cobra.Command{
		Use:   "human",
		Short: "Create a Human user",
		Long: `Create a new Human resource (Matrix account + room access).

  hiclaw create human --name bob --display-name "Bob Chen"
  hiclaw create human --name alice --display-name "Alice" --email alice@example.com --super-admin
  hiclaw create human --name carol --display-name "Carol" --team-access alpha:admin,beta:member
  hiclaw create human --name dave --display-name "Dave" --worker-access w1,w2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if displayName == "" {
				return fmt.Errorf("--display-name is required")
			}

			teamAccessList, err := parseTeamAccessCSV(teamAccess)
			if err != nil {
				return err
			}
			workerAccessList := splitCSV(workerAccess)

			if superAdmin && (len(teamAccessList) > 0 || len(workerAccessList) > 0) {
				return fmt.Errorf("--super-admin cannot be combined with --team-access or --worker-access")
			}

			req := map[string]interface{}{
				"name":        name,
				"displayName": displayName,
			}
			setIfNotEmpty(req, "email", email)
			setIfNotEmpty(req, "note", note)
			if superAdmin {
				req["superAdmin"] = true
			}
			if len(teamAccessList) > 0 {
				req["teamAccess"] = teamAccessList
			}
			if len(workerAccessList) > 0 {
				req["workerAccess"] = workerAccessList
			}

			client := NewAPIClient()
			var resp map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/humans", req, &resp); err != nil {
				return fmt.Errorf("create human: %w", err)
			}
			fmt.Printf("human/%s created\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Human username (required)")
	cmd.Flags().StringVar(&displayName, "display-name", "", "Display name (required)")
	cmd.Flags().StringVar(&email, "email", "", "Email address")
	cmd.Flags().StringVar(&note, "note", "", "Note for the Human user")
	cmd.Flags().BoolVar(&superAdmin, "super-admin", false, "Grant global super-admin access (mutually exclusive with --team-access/--worker-access)")
	cmd.Flags().StringVar(&teamAccess, "team-access", "", "Comma-separated team access entries (name:admin|member)")
	cmd.Flags().StringVar(&workerAccess, "worker-access", "", "Comma-separated worker names for direct access")
	return cmd
}

// ---------------------------------------------------------------------------
// create manager
// ---------------------------------------------------------------------------

func createManagerCmd() *cobra.Command {
	var (
		name    string
		model   string
		runtime string
		image   string
		soul    string
	)

	cmd := &cobra.Command{
		Use:   "manager",
		Short: "Create a Manager agent",
		Long: `Create a new Manager resource.

  hiclaw create manager --name default --model qwen3.5-plus
  hiclaw create manager --name default --model claude-sonnet-4-6 --runtime copaw`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if model == "" {
				return fmt.Errorf("--model is required")
			}

			req := map[string]interface{}{
				"name":  name,
				"model": model,
			}
			setIfNotEmpty(req, "runtime", runtime)
			setIfNotEmpty(req, "image", image)
			setIfNotEmpty(req, "soul", soul)

			client := NewAPIClient()
			var resp map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/managers", req, &resp); err != nil {
				return fmt.Errorf("create manager: %w", err)
			}
			fmt.Printf("manager/%s created\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Manager name (required)")
	cmd.Flags().StringVar(&model, "model", "", "LLM model ID (required)")
	cmd.Flags().StringVar(&runtime, "runtime", "", "Agent runtime (openclaw|copaw)")
	cmd.Flags().StringVar(&image, "image", "", "Container image override")
	cmd.Flags().StringVar(&soul, "soul", "", "Manager SOUL.md content")
	return cmd
}

// ---------------------------------------------------------------------------
// Helpers (migrated from old main.go)
// ---------------------------------------------------------------------------

var workerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func validateWorkerName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("invalid worker name: name is required")
	}
	if !workerNamePattern.MatchString(name) {
		return fmt.Errorf("invalid worker name %q: must start with a lowercase letter or digit and contain only lowercase letters, digits, and hyphens", name)
	}
	return nil
}

func expandPackageURI(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") {
		return raw, nil
	}

	base := strings.TrimSpace(os.Getenv("HICLAW_NACOS_REGISTRY_URI"))
	if base == "" {
		base = "nacos://market.hiclaw.io:80/public"
	}
	if !strings.HasPrefix(base, "nacos://") {
		return "", fmt.Errorf("invalid HICLAW_NACOS_REGISTRY_URI %q: must start with nacos://", base)
	}
	base = strings.TrimRight(base, "/")
	if base == "nacos:" || base == "nacos:/" || base == "nacos://" {
		return "", fmt.Errorf("invalid HICLAW_NACOS_REGISTRY_URI %q: missing host/namespace", base)
	}

	parts := strings.Split(raw, "/")
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("invalid package shorthand %q: empty path segment", raw)
		}
		encoded = append(encoded, url.PathEscape(part))
	}

	return base + "/" + strings.Join(encoded, "/"), nil
}

func splitCSV(s string) []string {
	var result []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func parseExposePorts(s string) []map[string]interface{} {
	var ports []map[string]interface{}
	for _, p := range splitCSV(s) {
		port := map[string]interface{}{"port": p}
		ports = append(ports, port)
	}
	return ports
}

func setIfNotEmpty(m map[string]interface{}, key, value string) {
	if value != "" {
		m[key] = value
	}
}
