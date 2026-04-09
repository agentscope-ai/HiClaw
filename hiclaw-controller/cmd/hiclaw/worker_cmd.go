package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func workerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Worker lifecycle operations",
	}
	cmd.AddCommand(workerWakeCmd())
	cmd.AddCommand(workerSleepCmd())
	cmd.AddCommand(workerEnsureReadyCmd())
	cmd.AddCommand(workerStatusCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// worker wake
// ---------------------------------------------------------------------------

func workerWakeCmd() *cobra.Command {
	var (
		name string
		team string
	)

	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Wake a sleeping Worker",
		Long: `Start a stopped/sleeping Worker container.

  hiclaw worker wake --name alice
  hiclaw worker wake --name alpha-dev --team alpha-team`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			_ = team
			client := NewAPIClient()
			var resp lifecycleResp
			if err := client.DoJSON("POST", "/api/v1/workers/"+name+"/wake", nil, &resp); err != nil {
				return fmt.Errorf("wake worker: %w", err)
			}
			fmt.Printf("worker/%s phase=%s\n", resp.Name, resp.Phase)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Worker name (required)")
	cmd.Flags().StringVar(&team, "team", "", "Team name context (optional)")
	return cmd
}

// ---------------------------------------------------------------------------
// worker sleep
// ---------------------------------------------------------------------------

func workerSleepCmd() *cobra.Command {
	var (
		name string
		team string
	)

	cmd := &cobra.Command{
		Use:   "sleep",
		Short: "Put a Worker to sleep",
		Long: `Stop a running Worker container (preserves state).

  hiclaw worker sleep --name alice
  hiclaw worker sleep --name alpha-dev --team alpha-team`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			_ = team
			client := NewAPIClient()
			var resp lifecycleResp
			if err := client.DoJSON("POST", "/api/v1/workers/"+name+"/sleep", nil, &resp); err != nil {
				return fmt.Errorf("sleep worker: %w", err)
			}
			fmt.Printf("worker/%s phase=%s\n", resp.Name, resp.Phase)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Worker name (required)")
	cmd.Flags().StringVar(&team, "team", "", "Team name context (optional)")
	return cmd
}

// ---------------------------------------------------------------------------
// worker ensure-ready
// ---------------------------------------------------------------------------

func workerEnsureReadyCmd() *cobra.Command {
	var (
		name string
		team string
	)

	cmd := &cobra.Command{
		Use:   "ensure-ready",
		Short: "Ensure a Worker is running and ready",
		Long: `Start the Worker if sleeping, then report current phase.

  hiclaw worker ensure-ready --name alice
  hiclaw worker ensure-ready --name alpha-dev --team alpha-team`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			_ = team
			client := NewAPIClient()
			var resp lifecycleResp
			if err := client.DoJSON("POST", "/api/v1/workers/"+name+"/ensure-ready", nil, &resp); err != nil {
				return fmt.Errorf("ensure-ready: %w", err)
			}
			fmt.Printf("worker/%s phase=%s\n", resp.Name, resp.Phase)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Worker name (required)")
	cmd.Flags().StringVar(&team, "team", "", "Team name context (optional)")
	return cmd
}

// ---------------------------------------------------------------------------
// worker status
// ---------------------------------------------------------------------------

func workerStatusCmd() *cobra.Command {
	var (
		name   string
		team   string
		output string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Worker runtime status",
		Long: `Show runtime status for a single Worker or all Workers in a team.

  hiclaw worker status --name alice
  hiclaw worker status --team alpha-team`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && team == "" {
				return fmt.Errorf("--name or --team is required")
			}

			client := NewAPIClient()

			if name != "" {
				var resp workerResp
				if err := client.DoJSON("GET", "/api/v1/workers/"+name+"/status", nil, &resp); err != nil {
					return fmt.Errorf("worker status: %w", err)
				}
				if output == "json" {
					printJSON(resp)
					return nil
				}
				printDetail(workerDetail(resp))
				return nil
			}

			// --team: list all workers in team, show runtime summary table
			var resp workerListResp
			if err := client.DoJSON("GET", "/api/v1/workers?team="+team, nil, &resp); err != nil {
				return fmt.Errorf("list team workers: %w", err)
			}
			if output == "json" {
				printJSON(resp)
				return nil
			}
			if resp.Total == 0 {
				fmt.Printf("No workers found in team %s.\n", team)
				return nil
			}
			headers := []string{"NAME", "PHASE", "STATE", "MODEL", "RUNTIME"}
			var rows [][]string
			for _, w := range resp.Workers {
				var detail workerResp
				if err := client.DoJSON("GET", "/api/v1/workers/"+w.Name+"/status", nil, &detail); err != nil {
					return fmt.Errorf("worker %s status: %w", w.Name, err)
				}
				rows = append(rows, []string{
					detail.Name,
					or(detail.Phase, "Pending"),
					or(detail.ContainerState, "unknown"),
					detail.Model,
					or(detail.Runtime, "openclaw"),
				})
			}
			printTable(headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Worker name")
	cmd.Flags().StringVar(&team, "team", "", "Team name (show all workers in team)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// Response type
// ---------------------------------------------------------------------------

type lifecycleResp struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}
