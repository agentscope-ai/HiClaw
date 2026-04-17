package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update a resource",
	}
	cmd.AddCommand(updateWorkerCmd())
	cmd.AddCommand(updateTeamCmd())
	cmd.AddCommand(updateHumanCmd())
	cmd.AddCommand(updateManagerCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// update worker
// ---------------------------------------------------------------------------

func updateWorkerCmd() *cobra.Command {
	var (
		name       string
		model      string
		runtime    string
		image      string
		identity   string
		soul       string
		skills     string
		mcpServers string
		packageURI string
		expose     string
		role       string
		team       string
	)

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Update a Worker",
		Long: `Update an existing Worker resource. Only specified fields are changed.

  hiclaw update worker --name alice --model claude-sonnet-4-6
  hiclaw update worker --name alice --image hiclaw/worker-agent:v1.2.0
  hiclaw update worker --name alpha-dev --role team_worker --team alpha
  hiclaw update worker --name alpha-dev --role standalone --team ""  # demote to standalone`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			if packageURI != "" {
				var err error
				packageURI, err = expandPackageURI(packageURI)
				if err != nil {
					return err
				}
			}

			req := map[string]interface{}{}
			setIfNotEmpty(req, "model", model)
			setIfNotEmpty(req, "runtime", runtime)
			setIfNotEmpty(req, "image", image)
			setIfNotEmpty(req, "identity", identity)
			setIfNotEmpty(req, "soul", soul)
			setIfNotEmpty(req, "package", packageURI)
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
			// --team uses pointer semantics on the server side: nil keeps
			// the current value, "" clears teamRef (demote to standalone),
			// non-empty replaces it. The flag being Changed drives the
			// distinction; unset flag means "leave unchanged".
			if cmd.Flags().Changed("team") {
				req["teamRef"] = team
			}

			if len(req) == 0 {
				return fmt.Errorf("at least one field must be specified for update")
			}

			client := NewAPIClient()
			var resp map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/workers/"+name, req, &resp); err != nil {
				return fmt.Errorf("update worker: %w", err)
			}
			fmt.Printf("worker/%s configured\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Worker name (required)")
	cmd.Flags().StringVar(&model, "model", "", "LLM model ID")
	cmd.Flags().StringVar(&runtime, "runtime", "", "Agent runtime (openclaw|copaw)")
	cmd.Flags().StringVar(&image, "image", "", "Container image override")
	cmd.Flags().StringVar(&identity, "identity", "", "Worker identity description")
	cmd.Flags().StringVar(&soul, "soul", "", "Worker SOUL.md content")
	cmd.Flags().StringVar(&skills, "skills", "", "Comma-separated built-in skills")
	cmd.Flags().StringVar(&mcpServers, "mcp-servers", "", "Comma-separated MCP servers")
	cmd.Flags().StringVar(&packageURI, "package", "", "Package URI")
	cmd.Flags().StringVar(&expose, "expose", "", "Comma-separated ports to expose")
	cmd.Flags().StringVar(&role, "role", "", "Worker role (standalone|team_leader|team_worker)")
	cmd.Flags().StringVar(&team, "team", "", "Team name (set to empty string to clear)")
	return cmd
}

// ---------------------------------------------------------------------------
// update team — slim: only the team-level fields left on the CR.
//
// Leader model is no longer a Team field. Use `hiclaw update worker` against
// the Leader Worker to change its model, runtime, etc.
// ---------------------------------------------------------------------------

func updateTeamCmd() *cobra.Command {
	var (
		name                 string
		description          string
		leaderHeartbeatEvery string
		workerIdleTimeout    string
	)

	cmd := &cobra.Command{
		Use:   "team",
		Short: "Update a Team",
		Long: `Update an existing Team resource. Only specified fields are changed.

Leader model is not a Team-level field; use hiclaw update worker --name <leader> --model <M>.

  hiclaw update team --name alpha --description "Updated description"
  hiclaw update team --name alpha --leader-heartbeat-every 30m --worker-idle-timeout 12h`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			req := map[string]interface{}{}
			setIfNotEmpty(req, "description", description)
			setIfNotEmpty(req, "workerIdleTimeout", workerIdleTimeout)
			if leaderHeartbeatEvery != "" {
				req["heartbeat"] = map[string]interface{}{
					"enabled": true,
					"every":   leaderHeartbeatEvery,
				}
			}

			if len(req) == 0 {
				return fmt.Errorf("at least one field must be specified for update")
			}

			client := NewAPIClient()
			var resp map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/teams/"+name, req, &resp); err != nil {
				return fmt.Errorf("update team: %w", err)
			}
			fmt.Printf("team/%s configured\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Team name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Team description")
	cmd.Flags().StringVar(&leaderHeartbeatEvery, "leader-heartbeat-every", "", "Leader heartbeat interval (e.g. 30m)")
	cmd.Flags().StringVar(&workerIdleTimeout, "worker-idle-timeout", "", "Idle timeout before the leader may sleep workers (e.g. 12h)")
	return cmd
}

// ---------------------------------------------------------------------------
// update human — covers the new superAdmin / teamAccess / workerAccess model.
//
// Pointer semantics matter for --super-admin: user may want to set it false
// to revoke super-admin. A simple bool flag with Changed detection handles
// this — if Changed then send the pointer, otherwise omit so the server
// leaves it alone.
// ---------------------------------------------------------------------------

func updateHumanCmd() *cobra.Command {
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
		Short: "Update a Human",
		Long: `Update an existing Human resource. Only specified fields are changed.

  hiclaw update human --name bob --display-name "Bob Chen"
  hiclaw update human --name bob --super-admin=true
  hiclaw update human --name bob --team-access alpha:admin,beta:member
  hiclaw update human --name bob --worker-access w1,w2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			teamAccessList, err := parseTeamAccessCSV(teamAccess)
			if err != nil {
				return err
			}
			// An empty string for --team-access or --worker-access still
			// counts as "leave unchanged" here because parseTeamAccessCSV
			// and splitCSV both return nil for the empty input. Passing a
			// non-empty value always replaces the whole slice.
			workerAccessList := splitCSV(workerAccess)

			req := map[string]interface{}{}
			setIfNotEmpty(req, "displayName", displayName)
			setIfNotEmpty(req, "email", email)
			setIfNotEmpty(req, "note", note)
			if cmd.Flags().Changed("super-admin") {
				req["superAdmin"] = superAdmin
			}
			if cmd.Flags().Changed("team-access") {
				if teamAccessList == nil {
					req["teamAccess"] = []map[string]string{}
				} else {
					req["teamAccess"] = teamAccessList
				}
			}
			if cmd.Flags().Changed("worker-access") {
				if workerAccessList == nil {
					req["workerAccess"] = []string{}
				} else {
					req["workerAccess"] = workerAccessList
				}
			}

			if len(req) == 0 {
				return fmt.Errorf("at least one field must be specified for update")
			}

			client := NewAPIClient()
			var resp map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/humans/"+name, req, &resp); err != nil {
				return fmt.Errorf("update human: %w", err)
			}
			fmt.Printf("human/%s configured\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Human username (required)")
	cmd.Flags().StringVar(&displayName, "display-name", "", "Display name")
	cmd.Flags().StringVar(&email, "email", "", "Email address")
	cmd.Flags().StringVar(&note, "note", "", "Note for the Human user")
	cmd.Flags().BoolVar(&superAdmin, "super-admin", false, "Set super-admin flag (use --super-admin=false to revoke)")
	cmd.Flags().StringVar(&teamAccess, "team-access", "", "Comma-separated team access entries (name:admin|member); replaces current list")
	cmd.Flags().StringVar(&workerAccess, "worker-access", "", "Comma-separated worker names; replaces current list")
	return cmd
}

// ---------------------------------------------------------------------------
// update manager
// ---------------------------------------------------------------------------

func updateManagerCmd() *cobra.Command {
	var (
		name    string
		model   string
		runtime string
		image   string
		soul    string
	)

	cmd := &cobra.Command{
		Use:   "manager",
		Short: "Update a Manager",
		Long: `Update an existing Manager resource. Only specified fields are changed.

  hiclaw update manager --name default --model claude-sonnet-4-6
  hiclaw update manager --name default --image hiclaw/manager-agent:v1.2.0`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			req := map[string]interface{}{}
			setIfNotEmpty(req, "model", model)
			setIfNotEmpty(req, "runtime", runtime)
			setIfNotEmpty(req, "image", image)
			setIfNotEmpty(req, "soul", soul)

			if len(req) == 0 {
				return fmt.Errorf("at least one field must be specified for update")
			}

			client := NewAPIClient()
			var resp map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/managers/"+name, req, &resp); err != nil {
				return fmt.Errorf("update manager: %w", err)
			}
			fmt.Printf("manager/%s configured\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Manager name (required)")
	cmd.Flags().StringVar(&model, "model", "", "LLM model ID")
	cmd.Flags().StringVar(&runtime, "runtime", "", "Agent runtime (openclaw|copaw)")
	cmd.Flags().StringVar(&image, "image", "", "Container image override")
	cmd.Flags().StringVar(&soul, "soul", "", "Manager SOUL.md content")
	return cmd
}
