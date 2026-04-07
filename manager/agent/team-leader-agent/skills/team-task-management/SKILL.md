---
name: team-task-management
description: Use when you need to assign tasks to team workers, track team task progress, or manage team-state.json. Use send-team-message.sh to communicate with workers. Never do worker tasks yourself.
---

# Team Task Management

Manage individual tasks within your team. For complex multi-worker tasks with dependencies, use `team-project-management` instead.

## CRITICAL: You Are a Coordinator, Not an Executor

**NEVER write code, design APIs, create deliverables, or do any domain work yourself.**
If you catch yourself doing a worker's job — STOP and delegate instead.

## How to Assign a Task to a Worker

Follow these steps IN ORDER. Do NOT skip any step.

### Step 1: Write spec.md to MinIO

Write the task spec and push to MinIO. Workers pull from MinIO via file-sync.

```bash
# Create spec locally in shared/tasks/
mkdir -p /root/hiclaw-fs/shared/tasks/st-01
cat > /root/hiclaw-fs/shared/tasks/st-01/spec.md << 'EOF'
# Task: Design API endpoints
(your task description here)
EOF

# Push to MinIO team shared
mc cp /root/hiclaw-fs/shared/tasks/st-01/spec.md \
  hiclaw/hiclaw-storage/teams/<TEAM_NAME>/shared/tasks/st-01/spec.md
```

The MinIO path MUST be `hiclaw/hiclaw-storage/teams/<your team name>/shared/tasks/<task-id>/spec.md`.

### Step 2: Send @mention to worker in Team Room

Your Coordination section (in AGENTS.md, already in your system prompt) has:
- **Team Room** ID
- **Team Workers** — each worker's Matrix ID

```bash
bash ./skills/team-task-management/scripts/send-team-message.sh \
  --room-id '<Team Room from Coordination section>' \
  --to '<worker Matrix ID from Coordination section>' \
  --message '@worker-name:domain New task [st-01]: Design API endpoints. Please file-sync and read teams/<team>/tasks/st-01/spec.md. @mention me when complete.'
```

The message MUST tell the worker to file-sync and give the `shared/tasks/<id>/spec.md` path (workers see it locally after sync).

### Step 3: Track in team-state.json

```bash
bash ./skills/team-task-management/scripts/manage-team-state.sh \
  --action add-finite --task-id st-01 --title "Design API endpoints" \
  --assigned-to <worker-name> --room-id '<Team Room>' \
  --source team-admin --requester '<admin Matrix ID>'
```

## Task Sources

| Source | Channel | Report to |
|--------|---------|-----------|
| Manager | Leader Room @mention | Manager in Leader Room |
| Team Admin | Leader DM message | Team Admin in Leader DM |

## Key Scripts

```bash
# Send @mention to a worker (REQUIRED for task assignment)
bash ./skills/team-task-management/scripts/send-team-message.sh \
  --room-id '!room' --to '@worker:domain' --message 'message'

# Track task state
bash ./skills/team-task-management/scripts/manage-team-state.sh \
  --action add-finite --task-id ID --title TITLE \
  --assigned-to WORKER --room-id ROOM --source SOURCE

# Mark task complete
bash ./skills/team-task-management/scripts/manage-team-state.sh \
  --action complete --task-id ID

# List active tasks
bash ./skills/team-task-management/scripts/manage-team-state.sh --action list
```

## References

Read the relevant doc **before** executing. Do not load all of them.

| Situation | Read |
|---|---|
| Assign a task or handle completion | `references/finite-tasks.md` |
| State management details | `references/state-management.md` |
