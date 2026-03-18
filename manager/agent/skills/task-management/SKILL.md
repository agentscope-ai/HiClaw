---
name: task-management
description: Assign and track tasks for Worker Agents. Use when the human admin gives a task to delegate, when a Worker reports completion, or when managing recurring scheduled tasks.
---

# Task Management

## Step 0: Worker Availability Check (when no Worker is explicitly specified)

**Trigger**: Admin gives a task without naming a Worker.

Run the find-worker script to get a consolidated view of all Workers — registry info, active tasks, container status, and role — in one call:

```bash
# All workers with full availability info
bash /opt/hiclaw/agent/skills/task-management/scripts/find-worker.sh

# Filter by required skills (only show workers that have ALL listed skills)
bash /opt/hiclaw/agent/skills/task-management/scripts/find-worker.sh --skills github-operations,git-delegation
```

The output is JSON with a `summary` (idle/busy/stopped/unavailable counts) and a `workers` array. Each worker entry includes:

| Field | Meaning |
|-------|---------|
| `availability` | `idle` (ready to assign), `busy` (has finite tasks), `stopped` (needs wake-up), `unavailable` (container gone, needs recreate) |
| `role` | First line from SOUL.md's `## Role` section — use this to match task requirements |
| `skills` | Skill list from registry — use this to check capability fit |
| `finite_tasks` / `infinite_tasks` | Current workload counts |
| `active_tasks` | List of `{task_id, type, title}` for tasks currently assigned |
| `container_status` | Raw status: `running`, `stopped`, `not_found`, `remote`, `unknown` |

**Decision flow based on output (delegation-first — always prefer assigning to a Worker):**

1. **Idle workers exist** → pick the best match by role + skills, present to admin as **Option A** (strongly recommended)
2. **Only busy workers** → present workload info, suggest **Option B** (create new Worker) or wait for a Worker to become idle
3. **No workers at all** → suggest **Option B** (create new Worker). Only fall back to **Option C** if the admin explicitly requests it

Present options to admin:
- **Option A** (preferred) — Assign to an idle existing Worker (show name + role + skills + current workload from the script output)
- **Option B** — Create a new Worker (suggest name/role/skills/model based on task type; ask about find-skills, see Step 4)
- **Option C** (last resort) — Handle it yourself. Only choose this when the admin explicitly says "do it yourself", or the task falls within your management skills listed in `TOOLS.md` (e.g., worker-management, mcp-server-management, model-switch). Never default to this just because no Worker is available — propose creating one first.

Act on choice: A → ensure container ready then assign; B → create Worker then assign; C → work directly (no task directory needed).

**Skip Step 0 when**: admin explicitly names a Worker, says "do it yourself", or it's a heartbeat-triggered infinite task. In YOLO mode, autonomously pick the best Worker or create one — still prefer delegation over self-execution.

**Step 4 — Find-Skills (only when creating a new Worker):**

Check default: `echo "${HICLAW_SKILLS_API_URL}"`

Ask admin: enable find-skills (recommended) or disable; optionally provide custom registry URL. Pass to `create-worker.sh` via `--find-skills` / `--skills-api-url`.

---

## Before Assigning Tasks: Worker Status Check

The `find-worker.sh` output already includes `container_status` and `availability`. Use it directly:

1. `availability = "idle"` or `"busy"` → container is running, assign directly
2. `availability = "stopped"` → wake up first: `lifecycle-worker.sh --action ensure-ready --worker <name>`, wait for the output to confirm `status=started`, then assign
3. `availability = "unavailable"` → try `lifecycle-worker.sh --action ensure-ready --worker <name>` first (it will attempt to recreate); if `status=failed`, notify admin that the Worker must be recreated via `create-worker.sh`

If you already ran `find-worker.sh` in Step 0, you do NOT need a separate container status check — the information is already in the output. Only run a standalone check when assigning to an explicitly named Worker (Step 0 was skipped):

```bash
bash /opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh \
  --action ensure-ready --worker <name>
```

---

## Choosing Task Type: Finite vs Infinite

Before creating a task, determine the correct type:

- **Finite** — the task has a clear end state. Once the Worker delivers the result, it's done. Examples: "implement login page", "fix bug #123", "write a report on X", "review this PR".
- **Infinite** — the task repeats on a schedule with no natural end. The Worker executes it periodically and reports back each time. Examples: "run security scan every day at 9am", "check service health every hour", "sync data from API every Monday".

**Decision rule**: if the admin's request contains a recurring schedule (daily, hourly, every N minutes, cron expression) or implies ongoing monitoring/polling, use infinite. Everything else is finite. When ambiguous, ask the admin to clarify.

---

## Assigning a Finite Task

1. Generate task ID: `task-YYYYMMDD-HHMMSS`
2. Create task directory and write files:
   ```bash
   mkdir -p /root/hiclaw-fs/shared/tasks/{task-id}
   # meta.json
   {
     "task_id": "task-YYYYMMDD-HHMMSS",
     "type": "finite",
     "assigned_to": "<worker-name>",
     "room_id": "<room-id>",
     "status": "assigned",
     "assigned_at": "<ISO-8601>",
     "completed_at": null
   }
   # spec.md — complete requirements, acceptance criteria, context
   ```
3. Push task files to MinIO immediately:
   ```bash
   mc cp /root/hiclaw-fs/shared/tasks/{task-id}/meta.json ${HICLAW_STORAGE_PREFIX}/shared/tasks/{task-id}/meta.json
   mc cp /root/hiclaw-fs/shared/tasks/{task-id}/spec.md ${HICLAW_STORAGE_PREFIX}/shared/tasks/{task-id}/spec.md
   ```
4. Notify Worker in their Room:
   ```
   @{worker}:{domain} New task [{task-id}]: {title}. Use your file-sync skill to pull the spec: shared/tasks/{task-id}/spec.md. @mention me when complete.
   ```
   - If Worker has `find-skills` skill (`test -d /root/hiclaw-fs/agents/{worker}/skills/find-skills`), add: `💡 Run \`skills find <keyword>\` if you need additional capabilities.`
5. Add to `state.json`:
   ```bash
   bash /opt/hiclaw/agent/skills/task-management/scripts/manage-state.sh \
     --action add-finite --task-id {task-id} --title "{short description}" \
     --assigned-to {worker} --room-id {room-id}
   ```
   If the task belongs to a project, append `--project-room-id {project-room-id}`.
6. On completion: pull the task directory from MinIO first (Worker has pushed results), then update `meta.json` status=completed + completed_at, and remove from `state.json`:
   ```bash
   mc mirror ${HICLAW_STORAGE_PREFIX}/shared/tasks/{task-id}/ /root/hiclaw-fs/shared/tasks/{task-id}/ --overwrite
   # Update meta.json, then:
   mc cp /root/hiclaw-fs/shared/tasks/{task-id}/meta.json ${HICLAW_STORAGE_PREFIX}/shared/tasks/{task-id}/meta.json
   bash /opt/hiclaw/agent/skills/task-management/scripts/manage-state.sh \
     --action complete --task-id {task-id}
   ```
   Log to `memory/YYYY-MM-DD.md`.
7. Notify admin that the task is complete. **Read SOUL.md first** — use the identity, personality, and user's preferred language defined there when composing the notification.

   Resolve the notification channel:
   ```bash
   bash /opt/hiclaw/agent/skills/task-management/scripts/resolve-notify-channel.sh
   ```
   The script outputs JSON with `channel`, `target`, and `via` fields. Use the `message` tool with those values:
   - If `channel` is not `"none"`: send `[Task Completed] {task-id}: {title} — assigned to {worker}. {one-line summary from result.md}` to the resolved `target`.
   - If `channel` is `"none"`: the admin DM room has not been discovered yet. Log a warning and skip notification (heartbeat will catch up).

   Compose the message in the persona and language from SOUL.md. Keep it concise — one or two sentences summarizing the outcome.

**Task directory layout:**
```
shared/tasks/{task-id}/
├── meta.json     # Manager-maintained
├── spec.md       # Manager-written
├── base/         # Manager-maintained reference files (Workers must not overwrite)
├── plan.md       # Worker-written execution plan
├── result.md     # Worker-written final result
└── *             # Intermediate artifacts
```

---

## Infinite Task Workflow

For recurring/scheduled tasks:

1. Create task directory and write files:
   ```bash
   # meta.json
   {
     "task_id": "task-YYYYMMDD-HHMMSS",
     "type": "infinite",
     "assigned_to": "<worker-name>",
     "room_id": "<room-id>",
     "status": "active",
     "schedule": "0 9 * * *",
     "timezone": "Asia/Shanghai",
     "assigned_at": "<ISO-8601>"
   }
   # spec.md — task spec including per-run execution guidelines
   ```
   - `status` is always `"active"`, never `"completed"`
   - `schedule`: standard 5-field cron; `timezone`: tz database name
2. Push task files to MinIO immediately:
   ```bash
   mc cp /root/hiclaw-fs/shared/tasks/{task-id}/meta.json ${HICLAW_STORAGE_PREFIX}/shared/tasks/{task-id}/meta.json
   mc cp /root/hiclaw-fs/shared/tasks/{task-id}/spec.md ${HICLAW_STORAGE_PREFIX}/shared/tasks/{task-id}/spec.md
   ```
3. Add to `state.json`:
   ```bash
   bash /opt/hiclaw/agent/skills/task-management/scripts/manage-state.sh \
     --action add-infinite --task-id {task-id} --title "{short description}" \
     --assigned-to {worker} --room-id {room-id} \
     --schedule "{cron}" --timezone "{tz}" --next-scheduled-at "{ISO-8601}"
   ```

### Triggering execution (heartbeat only)

Infinite tasks are triggered **exclusively by heartbeat** when `now > next_scheduled_at + 30min` and `last_executed_at < next_scheduled_at`. See HEARTBEAT.md Step 3 for the full logic.

Trigger message: `@{worker}:{domain} Execute recurring task {task-id}: {title}. Report back with "executed" when done.`

### Recording execution completion (on Worker report)

When a Worker reports `executed` for an infinite task, you MUST **only** update `state.json` — nothing else:

```bash
bash /opt/hiclaw/agent/skills/task-management/scripts/manage-state.sh \
  --action executed --task-id {task-id} --next-scheduled-at "{new-ISO-8601}"
```

**⚠️ CRITICAL: Do NOT @mention the Worker after recording execution.** "Recording completion" and "triggering next execution" are two completely independent actions. Recording happens immediately when the Worker reports back. Triggering happens later, only during heartbeat, only when the schedule says it's time. If you @mention the Worker here, you create a rapid-fire loop where the Worker executes → reports → you trigger again → Worker executes → reports → ... burning tokens continuously.

---

## State File (state.json)

Path: `~/state.json`

Single source of truth for active tasks. Heartbeat reads this instead of scanning all meta.json files.

**Always use `manage-state.sh` to modify this file** — never edit it manually with jq or text editors. The script handles initialization, deduplication, and atomic writes.

```bash
STATE_SCRIPT=/opt/hiclaw/agent/skills/task-management/scripts/manage-state.sh
```

### Structure

```json
{
  "admin_dm_room_id": "!abc:matrix-domain",
  "active_tasks": [
    {
      "task_id": "task-20260219-120000",
      "title": "Implement login page",
      "type": "finite",
      "assigned_to": "alice",
      "room_id": "!xxx:matrix-domain"
    },
    {
      "task_id": "task-20260219-130000",
      "title": "Daily security scan",
      "type": "infinite",
      "assigned_to": "bob",
      "room_id": "!yyy:matrix-domain",
      "schedule": "0 9 * * *",
      "timezone": "Asia/Shanghai",
      "last_executed_at": null,
      "next_scheduled_at": "2026-02-20T01:00:00Z"
    }
  ],
  "updated_at": "2026-02-19T15:00:00Z"
}
```

- `admin_dm_room_id`: Cached room ID for the Manager-Admin DM room. Set once via `set-admin-dm`, used by heartbeat to report findings directly to the admin.

### Script Reference

| When | Command |
|------|---------|
| Ensure file exists | `bash $STATE_SCRIPT --action init` |
| Assign a finite task | `bash $STATE_SCRIPT --action add-finite --task-id T --title TITLE --assigned-to W --room-id R` |
| Create an infinite task | `bash $STATE_SCRIPT --action add-infinite --task-id T --title TITLE --assigned-to W --room-id R --schedule CRON --timezone TZ --next-scheduled-at ISO` |
| Finite task completed | `bash $STATE_SCRIPT --action complete --task-id T` |
| Infinite task executed | `bash $STATE_SCRIPT --action executed --task-id T --next-scheduled-at ISO` |
| Cache admin DM room | `bash $STATE_SCRIPT --action set-admin-dm --room-id R` |
| View active tasks | `bash $STATE_SCRIPT --action list` |

The script auto-creates `~/state.json` if missing, skips duplicate additions, and updates `updated_at` on every write.
