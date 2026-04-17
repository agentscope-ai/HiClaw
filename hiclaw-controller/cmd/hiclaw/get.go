package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func getCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	cmd.AddCommand(getWorkersCmd())
	cmd.AddCommand(getTeamsCmd())
	cmd.AddCommand(getHumansCmd())
	cmd.AddCommand(getManagersCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// get workers
// ---------------------------------------------------------------------------

func getWorkersCmd() *cobra.Command {
	var team string
	var output string

	cmd := &cobra.Command{
		Use:   "workers [name]",
		Short: "Display Workers",
		Long: `List all Workers or get a specific Worker by name.

  hiclaw get workers
  hiclaw get workers alice
  hiclaw get workers --team alpha
  hiclaw get workers alice -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()

			if len(args) == 1 {
				var resp workerResp
				if err := client.DoJSON("GET", "/api/v1/workers/"+args[0], nil, &resp); err != nil {
					return fmt.Errorf("get worker: %w", err)
				}
				if output == "json" {
					printJSON(resp)
					return nil
				}
				printDetail(workerDetail(resp))
				return nil
			}

			path := "/api/v1/workers"
			if team != "" {
				path += "?team=" + team
			}
			var resp workerListResp
			if err := client.DoJSON("GET", path, nil, &resp); err != nil {
				return fmt.Errorf("list workers: %w", err)
			}
			if output == "json" {
				printJSON(resp)
				return nil
			}
			if resp.Total == 0 {
				fmt.Println("No workers found.")
				return nil
			}
			headers := []string{"NAME", "PHASE", "MODEL", "TEAM", "ROLE", "RUNTIME"}
			var rows [][]string
			for _, w := range resp.Workers {
				rows = append(rows, []string{
					w.Name,
					or(w.Phase, "Pending"),
					w.Model,
					or(w.TeamRef, "-"),
					or(w.Role, "-"),
					or(w.Runtime, "openclaw"),
				})
			}
			printTable(headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&team, "team", "", "Filter by team name")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// get teams
// ---------------------------------------------------------------------------

func getTeamsCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "teams [name]",
		Short: "Display Teams",
		Long: `List all Teams or get a specific Team by name.

  hiclaw get teams
  hiclaw get teams alpha
  hiclaw get teams alpha -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()

			if len(args) == 1 {
				var resp teamResp
				if err := client.DoJSON("GET", "/api/v1/teams/"+args[0], nil, &resp); err != nil {
					return fmt.Errorf("get team: %w", err)
				}
				if output == "json" {
					printJSON(resp)
					return nil
				}
				printDetail(teamDetail(resp))
				return nil
			}

			var resp teamListResp
			if err := client.DoJSON("GET", "/api/v1/teams", nil, &resp); err != nil {
				return fmt.Errorf("list teams: %w", err)
			}
			if output == "json" {
				printJSON(resp)
				return nil
			}
			if resp.Total == 0 {
				fmt.Println("No teams found.")
				return nil
			}
			headers := []string{"NAME", "PHASE", "LEADER", "MEMBERS", "READY"}
			var rows [][]string
			for _, t := range resp.Teams {
				ready := fmt.Sprintf("%d/%d", t.ReadyMembers, t.TotalMembers)
				rows = append(rows, []string{
					t.Name,
					or(t.Phase, "Pending"),
					or(t.LeaderName, "-"),
					strings.Join(memberNames(t.Members), ","),
					ready,
				})
			}
			printTable(headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// get humans
// ---------------------------------------------------------------------------

func getHumansCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "humans [name]",
		Short: "Display Humans",
		Long: `List all Humans or get a specific Human by name.

  hiclaw get humans
  hiclaw get humans bob
  hiclaw get humans bob -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()

			if len(args) == 1 {
				var resp humanResp
				if err := client.DoJSON("GET", "/api/v1/humans/"+args[0], nil, &resp); err != nil {
					return fmt.Errorf("get human: %w", err)
				}
				if output == "json" {
					printJSON(resp)
					return nil
				}
				printDetail(humanDetail(resp))
				return nil
			}

			var resp humanListResp
			if err := client.DoJSON("GET", "/api/v1/humans", nil, &resp); err != nil {
				return fmt.Errorf("list humans: %w", err)
			}
			if output == "json" {
				printJSON(resp)
				return nil
			}
			if resp.Total == 0 {
				fmt.Println("No humans found.")
				return nil
			}
			headers := []string{"NAME", "PHASE", "DISPLAY-NAME", "SUPER-ADMIN", "MATRIX-ID"}
			var rows [][]string
			for _, h := range resp.Humans {
				rows = append(rows, []string{
					h.Name,
					or(h.Phase, "Pending"),
					h.DisplayName,
					boolDisplay(h.SuperAdmin, "yes", "-"),
					or(h.MatrixUserID, "-"),
				})
			}
			printTable(headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// get managers
// ---------------------------------------------------------------------------

func getManagersCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "managers [name]",
		Short: "Display Managers",
		Long: `List all Managers or get a specific Manager by name.

  hiclaw get managers
  hiclaw get managers default
  hiclaw get managers default -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := NewAPIClient()

			if len(args) == 1 {
				var resp managerResp
				if err := client.DoJSON("GET", "/api/v1/managers/"+args[0], nil, &resp); err != nil {
					return fmt.Errorf("get manager: %w", err)
				}
				if output == "json" {
					printJSON(resp)
					return nil
				}
				printDetail(managerDetail(resp))
				return nil
			}

			var resp managerListResp
			if err := client.DoJSON("GET", "/api/v1/managers", nil, &resp); err != nil {
				return fmt.Errorf("list managers: %w", err)
			}
			if output == "json" {
				printJSON(resp)
				return nil
			}
			if resp.Total == 0 {
				fmt.Println("No managers found.")
				return nil
			}
			headers := []string{"NAME", "PHASE", "MODEL", "RUNTIME"}
			var rows [][]string
			for _, m := range resp.Managers {
				rows = append(rows, []string{
					m.Name,
					or(m.Phase, "Pending"),
					m.Model,
					or(m.Runtime, "openclaw"),
				})
			}
			printTable(headers, rows)
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format (json)")
	return cmd
}

// ---------------------------------------------------------------------------
// Response types (lightweight, no K8s dependency)
// ---------------------------------------------------------------------------

type workerResp struct {
	Name           string `json:"name"`
	Phase          string `json:"phase"`
	Model          string `json:"model,omitempty"`
	Runtime        string `json:"runtime,omitempty"`
	Image          string `json:"image,omitempty"`
	ContainerState string `json:"containerState,omitempty"`
	MatrixUserID   string `json:"matrixUserID,omitempty"`
	RoomID         string `json:"roomID,omitempty"`
	Message        string `json:"message,omitempty"`
	TeamRef        string `json:"teamRef,omitempty"`
	Role           string `json:"role,omitempty"`
}

type workerListResp struct {
	Workers []workerResp `json:"workers"`
	Total   int          `json:"total"`
}

type teamResp struct {
	Name               string             `json:"name"`
	Phase              string             `json:"phase"`
	Description        string             `json:"description,omitempty"`
	Heartbeat          *teamHeartbeatResp `json:"heartbeat,omitempty"`
	WorkerIdleTimeout  string             `json:"workerIdleTimeout,omitempty"`
	TeamRoomID         string             `json:"teamRoomID,omitempty"`
	LeaderDMRoomID     string             `json:"leaderDMRoomID,omitempty"`
	LeaderName         string             `json:"leaderName,omitempty"`
	LeaderMatrixUserID string             `json:"leaderMatrixUserID,omitempty"`
	LeaderReady        bool               `json:"leaderReady"`
	Members            []teamMemberInfo   `json:"members,omitempty"`
	Admins             []teamAdminInfo    `json:"admins,omitempty"`
	TotalMembers       int                `json:"totalMembers,omitempty"`
	ReadyMembers       int                `json:"readyMembers,omitempty"`
	Message            string             `json:"message,omitempty"`
}

type teamMemberInfo struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
	Ready        bool   `json:"ready"`
}

type teamAdminInfo struct {
	HumanName    string `json:"humanName"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
}

type teamHeartbeatResp struct {
	Enabled bool   `json:"enabled,omitempty"`
	Every   string `json:"every,omitempty"`
}

type teamListResp struct {
	Teams []teamResp `json:"teams"`
	Total int        `json:"total"`
}

type humanResp struct {
	Name            string            `json:"name"`
	Phase           string            `json:"phase"`
	DisplayName     string            `json:"displayName"`
	Email           string            `json:"email,omitempty"`
	MatrixUserID    string            `json:"matrixUserID,omitempty"`
	InitialPassword string            `json:"initialPassword,omitempty"`
	Rooms           []string          `json:"rooms,omitempty"`
	SuperAdmin      bool              `json:"superAdmin,omitempty"`
	TeamAccess      []teamAccessEntry `json:"teamAccess,omitempty"`
	WorkerAccess    []string          `json:"workerAccess,omitempty"`
	Message         string            `json:"message,omitempty"`
}

type teamAccessEntry struct {
	Team string `json:"team"`
	Role string `json:"role"`
}

type humanListResp struct {
	Humans []humanResp `json:"humans"`
	Total  int         `json:"total"`
}

type managerResp struct {
	Name         string `json:"name"`
	Phase        string `json:"phase"`
	Model        string `json:"model,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Image        string `json:"image,omitempty"`
	MatrixUserID string `json:"matrixUserID,omitempty"`
	RoomID       string `json:"roomID,omitempty"`
	Version      string `json:"version,omitempty"`
	Message      string `json:"message,omitempty"`
}

type managerListResp struct {
	Managers []managerResp `json:"managers"`
	Total    int           `json:"total"`
}

// bundleResponseWire matches the server's BundleResponse shape; used by
// createTeamCmd / deleteTeamCmd to decode 207 and 400 responses uniformly.
type bundleResponseWire struct {
	Items []bundleItemResp `json:"items"`
}

type bundleItemResp struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Warning bool   `json:"warning,omitempty"`
}

// ---------------------------------------------------------------------------
// Detail formatters
// ---------------------------------------------------------------------------

func workerDetail(w workerResp) []KeyValue {
	return []KeyValue{
		{"Name", w.Name},
		{"Phase", or(w.Phase, "Pending")},
		{"Model", w.Model},
		{"Runtime", or(w.Runtime, "openclaw")},
		{"ContainerState", w.ContainerState},
		{"Image", w.Image},
		{"TeamRef", w.TeamRef},
		{"Role", w.Role},
		{"MatrixUserID", w.MatrixUserID},
		{"RoomID", w.RoomID},
		{"Message", w.Message},
	}
}

func teamDetail(t teamResp) []KeyValue {
	return []KeyValue{
		{"Name", t.Name},
		{"Phase", or(t.Phase, "Pending")},
		{"Description", t.Description},
		{"Leader", t.LeaderName},
		{"LeaderMatrixUserID", t.LeaderMatrixUserID},
		{"LeaderHeartbeat", teamHeartbeatText(t.Heartbeat)},
		{"WorkerIdleTimeout", t.WorkerIdleTimeout},
		{"LeaderReady", strconv.FormatBool(t.LeaderReady)},
		{"Members", joinTeamMembers(t.Members)},
		{"ReadyMembers", fmt.Sprintf("%d/%d", t.ReadyMembers, t.TotalMembers)},
		{"Admins", joinTeamAdmins(t.Admins)},
		{"TeamRoomID", t.TeamRoomID},
		{"LeaderDMRoomID", t.LeaderDMRoomID},
		{"Message", t.Message},
	}
}

func teamHeartbeatText(hb *teamHeartbeatResp) string {
	if hb == nil {
		return ""
	}
	if hb.Every != "" {
		return hb.Every
	}
	if hb.Enabled {
		return "enabled"
	}
	return "disabled"
}

func humanDetail(h humanResp) []KeyValue {
	return []KeyValue{
		{"Name", h.Name},
		{"Phase", or(h.Phase, "Pending")},
		{"DisplayName", h.DisplayName},
		{"Email", h.Email},
		{"SuperAdmin", boolDisplay(h.SuperAdmin, "yes", "no")},
		{"TeamAccess", joinTeamAccess(h.TeamAccess)},
		{"WorkerAccess", strings.Join(h.WorkerAccess, ", ")},
		{"MatrixUserID", h.MatrixUserID},
		{"InitialPassword", h.InitialPassword},
		{"Rooms", strings.Join(h.Rooms, ", ")},
		{"Message", h.Message},
	}
}

func managerDetail(m managerResp) []KeyValue {
	return []KeyValue{
		{"Name", m.Name},
		{"Phase", or(m.Phase, "Pending")},
		{"Model", m.Model},
		{"Runtime", or(m.Runtime, "openclaw")},
		{"Image", m.Image},
		{"MatrixUserID", m.MatrixUserID},
		{"RoomID", m.RoomID},
		{"Version", m.Version},
		{"Message", m.Message},
	}
}

// memberNames returns just the Name field of each member, preserving order.
func memberNames(members []teamMemberInfo) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.Name)
	}
	return out
}

// joinTeamMembers formats members as "name(role, ready)" joined by ", ".
func joinTeamMembers(members []teamMemberInfo) string {
	if len(members) == 0 {
		return ""
	}
	parts := make([]string, 0, len(members))
	for _, m := range members {
		readiness := "not-ready"
		if m.Ready {
			readiness = "ready"
		}
		parts = append(parts, fmt.Sprintf("%s(%s, %s)", m.Name, m.Role, readiness))
	}
	return strings.Join(parts, ", ")
}

// joinTeamAdmins formats admins as "humanName[@matrixID]" joined by ", ".
func joinTeamAdmins(admins []teamAdminInfo) string {
	if len(admins) == 0 {
		return ""
	}
	parts := make([]string, 0, len(admins))
	for _, a := range admins {
		if a.MatrixUserID != "" {
			parts = append(parts, fmt.Sprintf("%s@%s", a.HumanName, a.MatrixUserID))
			continue
		}
		parts = append(parts, a.HumanName)
	}
	return strings.Join(parts, ", ")
}
