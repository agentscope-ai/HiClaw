package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func debugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "DebugWorker management",
	}
	cmd.AddCommand(debugCreateCmd())
	cmd.AddCommand(debugListCmd())
	cmd.AddCommand(debugGetCmd())
	cmd.AddCommand(debugDeleteCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// debug create
// ---------------------------------------------------------------------------

func debugCreateCmd() *cobra.Command {
	var (
		name              string
		model             string
		targets           []string
		allowedUsers      []string
		matrixUserID      string
		matrixAccessToken string
		hiclawVersion     string
		outputFmt         string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a DebugWorker",
		Long: `Create a DebugWorker to analyze and debug target Workers.

  hiclaw debug create --target backend --target frontend
  hiclaw debug create --name debug-team --target backend,frontend --model qwen3-235b-a22b
  hiclaw debug create --target backend --matrix-user-id @boss:matrix.local --matrix-access-token syt_xxx
  hiclaw debug create --target backend --allowed-user @dev-leader:matrix.local`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Flatten comma-separated targets
			var allTargets []string
			for _, t := range targets {
				for _, item := range splitCSV(t) {
					allTargets = append(allTargets, item)
				}
			}

			if len(allTargets) == 0 {
				return fmt.Errorf("at least one --target is required")
			}

			// Flatten comma-separated allowed users
			var allAllowedUsers []string
			for _, u := range allowedUsers {
				for _, item := range splitCSV(u) {
					allAllowedUsers = append(allAllowedUsers, item)
				}
			}

			req := map[string]interface{}{
				"targets": allTargets,
			}
			setIfNotEmpty(req, "name", name)
			setIfNotEmpty(req, "model", model)
			setIfNotEmpty(req, "hiclawVersion", hiclawVersion)

			if len(allAllowedUsers) > 0 {
				req["allowedUsers"] = allAllowedUsers
			}

			if matrixUserID != "" && matrixAccessToken != "" {
				req["matrixCredential"] = map[string]string{
					"userID":      matrixUserID,
					"accessToken": matrixAccessToken,
				}
			}

			client := NewAPIClient()
			var resp debugWorkerResp
			if err := client.DoJSON("POST", "/api/v1/debugworkers", req, &resp); err != nil {
				return fmt.Errorf("create debugworker: %w", err)
			}
			if outputFmt == "json" {
				printJSON(resp)
			} else {
				fmt.Printf("debugworker/%s created (targets: %s)\n", resp.Name, strings.Join(resp.Targets, ", "))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "DebugWorker name (auto-generated if empty)")
	cmd.Flags().StringVar(&model, "model", "", "LLM model ID (default: qwen3-235b-a22b)")
	cmd.Flags().StringArrayVar(&targets, "target", nil, "Target Worker name (repeatable or comma-separated)")
	cmd.Flags().StringArrayVar(&allowedUsers, "allowed-user", nil, "Matrix user IDs allowed to interact (repeatable or comma-separated)")
	cmd.Flags().StringVar(&matrixUserID, "matrix-user-id", "", "Matrix user ID for message export (e.g. @boss:matrix.local)")
	cmd.Flags().StringVar(&matrixAccessToken, "matrix-access-token", "", "Matrix access token for message export")
	cmd.Flags().StringVar(&hiclawVersion, "hiclaw-version", "", "HiClaw version/branch for source code cross-referencing")
	cmd.Flags().StringVarP(&outputFmt, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// debug list
// ---------------------------------------------------------------------------

func debugListCmd() *cobra.Command {
	var outputFmt string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all DebugWorkers",
		Long: `List all DebugWorker resources.

  hiclaw debug list
  hiclaw debug list -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()
			var resp debugWorkerListResp
			if err := client.DoJSON("GET", "/api/v1/debugworkers", nil, &resp); err != nil {
				return fmt.Errorf("list debugworkers: %w", err)
			}
			if outputFmt == "json" {
				printJSON(resp)
				return nil
			}
			if resp.Total == 0 {
				fmt.Println("No debugworkers found.")
				return nil
			}
			headers := []string{"NAME", "PHASE", "MODEL", "TARGETS"}
			var rows [][]string
			for _, dw := range resp.DebugWorkers {
				rows = append(rows, []string{
					dw.Name,
					or(dw.Phase, "Pending"),
					dw.Model,
					strings.Join(dw.Targets, ","),
				})
			}
			printTable(headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// debug get
// ---------------------------------------------------------------------------

func debugGetCmd() *cobra.Command {
	var outputFmt string

	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Get a DebugWorker",
		Long: `Get details for a specific DebugWorker.

  hiclaw debug get debug-backend
  hiclaw debug get debug-backend -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()
			var resp debugWorkerResp
			if err := client.DoJSON("GET", "/api/v1/debugworkers/"+args[0], nil, &resp); err != nil {
				return fmt.Errorf("get debugworker: %w", err)
			}
			if outputFmt == "json" {
				printJSON(resp)
				return nil
			}
			printDetail(debugWorkerDetail(resp))
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputFmt, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// debug delete
// ---------------------------------------------------------------------------

func debugDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a DebugWorker",
		Long: `Delete a DebugWorker and its child Worker (cascade).

  hiclaw debug delete debug-backend`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()
			if err := client.DoJSON("DELETE", "/api/v1/debugworkers/"+args[0], nil, nil); err != nil {
				return fmt.Errorf("delete debugworker: %w", err)
			}
			fmt.Printf("debugworker/%s deleted\n", args[0])
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

type debugWorkerResp struct {
	Name    string   `json:"name"`
	Phase   string   `json:"phase"`
	Model   string   `json:"model,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Message string   `json:"message,omitempty"`
}

type debugWorkerListResp struct {
	DebugWorkers []debugWorkerResp `json:"debugWorkers"`
	Total        int               `json:"total"`
}

// ---------------------------------------------------------------------------
// Detail formatter
// ---------------------------------------------------------------------------

func debugWorkerDetail(dw debugWorkerResp) []KeyValue {
	return []KeyValue{
		{"Name", dw.Name},
		{"Phase", or(dw.Phase, "Pending")},
		{"Model", dw.Model},
		{"Targets", strings.Join(dw.Targets, ", ")},
		{"Message", dw.Message},
	}
}
