package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// promoteCmd groups commands that atomically change role assignments
// within a team. Today only `promote worker` is exposed; additional
// operations (demote, swap) can slot in here without reshuffling flags.
func promoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote a resource (role transitions)",
	}
	cmd.AddCommand(promoteWorkerCmd())
	return cmd
}

// promoteWorkerCmd performs a two-step PUT sequence:
//  1. If the team already has a leader, demote that leader to team_worker.
//  2. Promote the named worker to team_leader with teamRef set.
//
// This is intentionally NOT atomic at the HTTP level — the controller
// webhook enforces "at most one leader per team", so step 2 will be
// rejected if step 1 was skipped (pre-existing leader still present).
// On step-2 failure after step-1 succeeded, the team temporarily has no
// leader; the user is prompted to re-run or fix manually.
func promoteWorkerCmd() *cobra.Command {
	var asLeaderOf string

	cmd := &cobra.Command{
		Use:   "worker <name>",
		Short: "Promote a Worker to team leader",
		Long: `Promote a Worker to be the leader of a team. If the team already has a
leader, that leader is first demoted to team_worker.

  hiclaw promote worker alpha-dev --as-leader-of alpha`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asLeaderOf == "" {
				return fmt.Errorf("--as-leader-of is required")
			}
			workerName := args[0]
			teamName := asLeaderOf

			client := NewAPIClient()

			var team teamResp
			if err := client.DoJSON("GET", "/api/v1/teams/"+teamName, nil, &team); err != nil {
				return fmt.Errorf("get team/%s: %w", teamName, err)
			}

			if team.LeaderName != "" && team.LeaderName != workerName {
				demote := map[string]interface{}{"role": "team_worker"}
				if err := client.DoJSON("PUT", "/api/v1/workers/"+team.LeaderName, demote, nil); err != nil {
					return fmt.Errorf("demote current leader worker/%s: %w", team.LeaderName, err)
				}
				fmt.Printf("worker/%s demoted to team_worker\n", team.LeaderName)
			}

			promote := map[string]interface{}{
				"role":    "team_leader",
				"teamRef": teamName,
			}
			if err := client.DoJSON("PUT", "/api/v1/workers/"+workerName, promote, nil); err != nil {
				return fmt.Errorf("promote worker/%s (team now has no leader; re-run promote or restore manually): %w", workerName, err)
			}
			fmt.Printf("worker/%s promoted to team_leader of team/%s\n", workerName, teamName)
			return nil
		},
	}

	cmd.Flags().StringVar(&asLeaderOf, "as-leader-of", "", "Team name the worker should lead (required)")
	return cmd
}
