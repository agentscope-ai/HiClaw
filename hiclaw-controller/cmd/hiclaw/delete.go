package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func deleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a resource",
	}
	cmd.AddCommand(deleteWorkerCmd())
	cmd.AddCommand(deleteTeamCmd())
	cmd.AddCommand(deleteHumanCmd())
	cmd.AddCommand(deleteManagerCmd())
	return cmd
}

func deleteWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker <name>",
		Short: "Delete a Worker",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deleteResource("worker", args[0])
		},
	}
}

// deleteTeamCmd defaults to cascade (bundle endpoint) which deletes every
// Worker whose teamRef points at this team, strips teamAccess entries for
// this team from every Human, then deletes the Team CR itself. Pass
// --orphan-workers to keep Workers around (they fall back to
// standalone-style operation per TeamRefResolved=False).
func deleteTeamCmd() *cobra.Command {
	var orphanWorkers bool

	cmd := &cobra.Command{
		Use:   "team <name>",
		Short: "Delete a Team (cascade by default)",
		Long: `Delete a Team resource. Default cascades to Workers and detaches admin Humans.

  hiclaw delete team alpha                    # cascade: delete workers + detach admins + delete team
  hiclaw delete team alpha --orphan-workers   # delete only the Team CR; Workers keep running as orphans`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client := NewAPIClient()

			if orphanWorkers {
				if err := client.DoJSON("DELETE", "/api/v1/teams/"+name, nil, nil); err != nil {
					return fmt.Errorf("delete team: %w", err)
				}
				fmt.Printf("team/%s deleted (workers orphaned)\n", name)
				return nil
			}

			resp, statusCode, err := doBundleRequest(client, "DELETE", "/api/v1/bundles/team/"+name, nil)
			if err != nil {
				return fmt.Errorf("delete team bundle: %w", err)
			}
			fatal := printBundleResponse(resp)
			if fatal {
				return fmt.Errorf("team bundle delete partially failed (HTTP %d)", statusCode)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&orphanWorkers, "orphan-workers", false, "Delete only the Team CR; leave Workers untouched")
	return cmd
}

func deleteHumanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "human <name>",
		Short: "Delete a Human",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deleteResource("human", args[0])
		},
	}
}

func deleteManagerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "manager <name>",
		Short: "Delete a Manager",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deleteResource("manager", args[0])
		},
	}
}

func deleteResource(kind, name string) error {
	client := NewAPIClient()
	if err := client.DoJSON("DELETE", fmt.Sprintf("/api/v1/%ss/%s", kind, name), nil, nil); err != nil {
		return fmt.Errorf("delete %s: %w", kind, err)
	}
	fmt.Printf("%s/%s deleted\n", kind, name)
	return nil
}
