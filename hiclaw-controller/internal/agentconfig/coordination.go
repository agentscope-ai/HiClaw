package agentconfig

import (
	"fmt"
	"strings"
)

// CoordinationContext describes the team/coordination context to inject into AGENTS.md.
type CoordinationContext struct {
	WorkerName        string
	Role              string // "worker", "team_leader", "standalone"
	MatrixDomain      string
	TeamName          string
	TeamLeaderName    string
	TeamAdminID       string // full Matrix ID of team admin
	TeamRoomID        string
	LeaderDMRoomID    string
	HeartbeatEvery    string
	WorkerIdleTimeout string
	TeamWorkers       []TeamWorkerInfo // for leaders: list of team workers
}

// TeamWorkerInfo describes a team worker for leader context injection.
type TeamWorkerInfo struct {
	Name   string
	RoomID string
}

const (
	teamCtxStart = "<!-- hiclaw-team-context-start -->"
	teamCtxEnd   = "<!-- hiclaw-team-context-end -->"
)

// InjectCoordinationContext inserts the team-context block into AGENTS.md content.
// It replaces any existing team-context block, or appends after the builtin-end marker.
func InjectCoordinationContext(agentsContent string, ctx CoordinationContext) string {
	block := buildCoordinationBlock(ctx)
	cleaned := removeCoordinationBlock(agentsContent)

	if strings.Contains(cleaned, BuiltinEnd) {
		idx := strings.LastIndex(cleaned, BuiltinEnd)
		before := cleaned[:idx+len(BuiltinEnd)]
		after := cleaned[idx+len(BuiltinEnd):]
		return before + "\n" + block + after
	}

	return strings.TrimRight(cleaned, "\n") + "\n" + block
}

func buildCoordinationBlock(ctx CoordinationContext) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(teamCtxStart)
	b.WriteString("\n## Coordination\n\n")

	switch ctx.Role {
	case "team_leader":
		fmt.Fprintf(&b, "- **Upstream coordinator**: @manager:%s (Manager) — you receive tasks from Manager\n", ctx.MatrixDomain)
		if ctx.TeamAdminID != "" {
			fmt.Fprintf(&b, "- **Team Admin**: %s — can assign tasks and make decisions within the team\n", ctx.TeamAdminID)
		}
		fmt.Fprintf(&b, "- **Team**: %s\n", ctx.TeamName)
		if ctx.TeamRoomID != "" {
			fmt.Fprintf(&b, "- **Team Room**: %s — @mention workers here for task assignment\n", ctx.TeamRoomID)
		}
		if ctx.LeaderDMRoomID != "" {
			fmt.Fprintf(&b, "- **Leader DM**: %s — Team Admin communicates with you here\n", ctx.LeaderDMRoomID)
		}
		if ctx.HeartbeatEvery != "" {
			fmt.Fprintf(&b, "- **Heartbeat interval**: %s\n", ctx.HeartbeatEvery)
		}
		if ctx.WorkerIdleTimeout != "" {
			fmt.Fprintf(&b, "- **Worker idle timeout**: %s\n", ctx.WorkerIdleTimeout)
		}
		if len(ctx.TeamWorkers) > 0 {
			b.WriteString("- **Team Workers**:\n")
			for _, w := range ctx.TeamWorkers {
				roomInfo := "unknown"
				if w.RoomID != "" {
					roomInfo = w.RoomID
				}
				fmt.Fprintf(&b, "  - @%s:%s — Room: %s\n", w.Name, ctx.MatrixDomain, roomInfo)
			}
		}
		b.WriteString("- You decompose tasks from Manager or Team Admin and assign sub-tasks to your team workers\n")
		b.WriteString("- @mention workers in the Team Room for task assignment\n")
		b.WriteString("- Use team-state.json as the source of truth for task activity before deciding whether a worker is idle\n")
		b.WriteString("- You decide when to wake or sleep team workers; the controller only executes the lifecycle action you request\n")
		b.WriteString("- Report results to Manager (in Leader Room) or Team Admin (in Leader DM) based on task source\n")
		b.WriteString("- @mention Manager only for: task completion, blockers, escalations\n")

	case "worker":
		fmt.Fprintf(&b, "- **Coordinator**: @%s:%s (Team Leader of %s)\n", ctx.TeamLeaderName, ctx.MatrixDomain, ctx.TeamName)
		if ctx.TeamAdminID != "" {
			fmt.Fprintf(&b, "- **Team Admin**: %s (has admin authority within this team)\n", ctx.TeamAdminID)
		}
		b.WriteString("- Report task completion, blockers, and questions to your coordinator\n")
		if ctx.TeamAdminID != "" {
			b.WriteString("- Respond to @mentions from your coordinator, Team Admin, and global Admin\n")
		} else {
			b.WriteString("- Respond to @mentions from your coordinator and global Admin\n")
		}
		b.WriteString("- Do NOT @mention Manager directly — all communication goes through your Team Leader\n")

	default: // standalone
		fmt.Fprintf(&b, "- **Coordinator**: @manager:%s (Manager)\n", ctx.MatrixDomain)
		b.WriteString("- Report task completion, blockers, and questions to your coordinator\n")
		b.WriteString("- Only respond to @mentions from your coordinator and Admin\n")
	}

	b.WriteString(teamCtxEnd)
	b.WriteString("\n")
	return b.String()
}

func removeCoordinationBlock(content string) string {
	for {
		startIdx := strings.Index(content, teamCtxStart)
		if startIdx < 0 {
			break
		}
		endIdx := strings.Index(content[startIdx:], teamCtxEnd)
		if endIdx < 0 {
			break
		}
		endIdx += startIdx + len(teamCtxEnd)
		// Consume trailing newline
		if endIdx < len(content) && content[endIdx] == '\n' {
			endIdx++
		}
		content = content[:startIdx] + content[endIdx:]
	}
	return content
}
