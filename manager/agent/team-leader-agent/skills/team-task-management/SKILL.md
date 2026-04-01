---
name: team-task-management
description: Use when you need to assign tasks to team workers, track team task progress, or manage team-state.json. Use send-team-message.sh to communicate with workers. Never do worker tasks yourself.
---

# Team Task Management

Manage individual tasks within your team. For complex multi-worker tasks with dependencies, use `team-project-management` instead.

## CRITICAL: You Are a Coordinator, Not an Executor

**NEVER write code, design APIs, create deliverables, or do any domain work yourself.**
Your ONLY job is to:
1. Decompose tasks into sub-tasks
2. Assign sub-tasks to workers via `send-team-message.sh`
3. Monitor progress and aggregate results

If you catch yourself doing a worker's job — STOP and delegate instead.

## How to Assign Tasks to Workers

Your Coordination section (in AGENTS.md, already loaded in your system prompt) has everything you need:
- **Team Room** ID — the room where you @mention workers
- **Team Workers** — each worker's full Matrix ID and Room ID

Use these values directly with `send-team-message.sh`:

```bash
bash ./skills/team-task-management/scripts/send-team-message.sh \
  --room-id '<Team Room from Coordination section>' \
  --to '<worker Matrix ID from Coordination section>' \
  --message '@worker-name:domain New task [st-01]: ... Please file-sync. @mention me when complete.'
```

Workers ONLY process messages with `m.mentions` — they will ignore plain text. The script handles this automatically.

## Task Sources

| Source | Channel | Result destination | Report to |
|--------|---------|-------------------|-----------|
| Manager | Leader Room @mention | `shared/tasks/{parent-task-id}/result.md` | Manager in Leader Room |
| Team Admin | Leader DM message | `teams/{team}/tasks/{task-id}/result.md` | Team Admin in Leader DM |

## Key Scripts

```bash
# Send @mention to a worker in Team Room (REQUIRED for task assignment)
bash ./skills/team-task-management/scripts/send-team-message.sh \
  --room-id '!teamroom:domain' --to '@worker:domain' \
  --message '@worker:domain Your task message here'

# Track task state
bash ./skills/team-task-management/scripts/manage-team-state.sh \
  --action add-finite --task-id st-01 --title "Task title" \
  --assigned-to worker-name --room-id '!room:domain' \
  --source team-admin --requester '@admin:domain'

# Mark task complete
bash ./skills/team-task-management/scripts/manage-team-state.sh \
  --action complete --task-id st-01

# List all active team tasks and projects
bash ./skills/team-task-management/scripts/manage-team-state.sh --action list
```

## Gotchas

- **Use send-team-message.sh for ALL worker communication** — workers ignore messages without m.mentions
- **Always push to MinIO before notifying workers** — workers need to file-sync to get specs
- **Always pull from MinIO before reading results** — workers push results there
- **Route completion by source** — check `source` field to decide where to report

## References

Read the relevant doc **before** executing. Do not load all of them.

| Situation | Read |
|---|---|
| Assign a task or handle completion | `references/finite-tasks.md` |
| State management details | `references/state-management.md` |
