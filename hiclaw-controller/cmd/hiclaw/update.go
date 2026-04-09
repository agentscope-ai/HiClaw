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
	)

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Update a Worker",
		Long: `Update an existing Worker resource. Only specified fields are changed.

  hiclaw update worker --name alice --model claude-sonnet-4-6
  hiclaw update worker --name alice --image hiclaw/worker-agent:v1.2.0
  hiclaw update worker --name alice --skills github-operations,code-review`,
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
			if skills != "" {
				req["skills"] = splitCSV(skills)
			}
			if mcpServers != "" {
				req["mcpServers"] = splitCSV(mcpServers)
			}
			if expose != "" {
				req["expose"] = parseExposePorts(expose)
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
	return cmd
}

// ---------------------------------------------------------------------------
// update team
// ---------------------------------------------------------------------------

func updateTeamCmd() *cobra.Command {
	var (
		name        string
		description string
		leaderModel string
	)

	cmd := &cobra.Command{
		Use:   "team",
		Short: "Update a Team",
		Long: `Update an existing Team resource. Only specified fields are changed.

  hiclaw update team --name alpha --description "Updated description"
  hiclaw update team --name alpha --leader-model claude-sonnet-4-6`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			req := map[string]interface{}{}
			setIfNotEmpty(req, "description", description)
			if leaderModel != "" {
				req["leader"] = map[string]interface{}{
					"model": leaderModel,
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
	cmd.Flags().StringVar(&leaderModel, "leader-model", "", "Leader LLM model")
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
