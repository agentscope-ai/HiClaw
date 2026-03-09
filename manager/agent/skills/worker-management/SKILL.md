---
name: worker-management
description: Manage the full lifecycle of Worker Agents (create, configure, monitor, reset, update model). Use when the human admin requests creating a new worker, resetting a worker, or switching a worker's model to a different one.
---

# Worker Management

## Overview

This skill allows you to manage the full lifecycle of Worker Agents: creation, configuration, monitoring, and reset. Workers are lightweight containers that connect to the Manager via Matrix and use the centralized file system.

## Environment Variables

These environment variables are pre-configured in the Manager container:

```bash
# Core configuration (set by hiclaw-install.sh)
HICLAW_MATRIX_DOMAIN       # Matrix server domain (e.g., matrix-local.hiclaw.io:8080)
HICLAW_AI_GATEWAY_DOMAIN   # AI Gateway domain (e.g., aigw-local.hiclaw.io)
HICLAW_FS_DOMAIN           # MinIO file system domain (e.g., fs-local.hiclaw.io)
HICLAW_ADMIN_USER          # Admin username
HICLAW_DEFAULT_MODEL       # Default LLM model (e.g., qwen3.5-plus)
HICLAW_REGISTRATION_TOKEN  # Token for registering Matrix users
HICLAW_MANAGER_PASSWORD    # Manager's Matrix password
HICLAW_WORKER_IMAGE        # Worker container image URL
```

No need to set defaults - these are always available in the container environment.

## Create a Worker

### Step 0: Receive configuration from AGENTS.md interaction

By the time you reach this skill, the admin has already confirmed:
- Worker name, role description, and any custom model/MCP server preferences
- `enable_find_skills`: true/false
- `skills_api_url`: custom URL or empty (uses `${HICLAW_SKILLS_API_URL:-https://skills.sh}` as default)

These are determined during the Task Workflow Step 0 / Step 4 interaction in AGENTS.md. Do not re-ask.

### Step 1: Write SOUL.md

Write the Worker's identity file based on the human admin's description:

```bash
mkdir -p ~/hiclaw-fs/agents/<WORKER_NAME>
cat > ~/hiclaw-fs/agents/<WORKER_NAME>/SOUL.md << 'EOF'
# Worker Agent - <WORKER_NAME>
... (role, skills, communication rules, security rules, etc.)
EOF
```

### Step 1.5: Determine skills based on worker role

**This step is mandatory before running the create script.** The available skills grow over time — never rely on memory. Always re-scan the skill definitions and read each one's assignment condition fresh.

1. List all available skills:
   ```bash
   ls ~/worker-skills/
   ```

2. Read the YAML frontmatter at the top of each skill's `SKILL.md` to get its `assign_when` condition:
   ```bash
   head -8 ~/worker-skills/<skill-name>/SKILL.md
   ```
   Each `SKILL.md` starts with:
   ```yaml
   ---
   name: <skill-name>
   description: <one-line summary of what this skill does>
   assign_when: <description of what role/responsibility warrants this skill>
   ---
   ```

3. Match each skill's `assign_when` against the Worker's role description and SOUL.md content. If it fits, include the skill.

4. Collect all matched skills. `file-sync` does not need to be specified — the script adds it automatically.

**When in doubt, assign more rather than fewer** — a missing skill blocks the Worker from completing tasks and can only be fixed later, while an extra skill causes no harm.

Pass the matched skills as a comma-separated string to `--skills`, e.g. `file-sync,github-operations`

### Step 2: Run create-worker script

The script handles everything: Matrix registration, room creation, Higress consumer, AI/MCP authorization, config generation, MinIO sync, skills push, and container startup.

```bash
bash /opt/hiclaw/agent/skills/worker-management/scripts/create-worker.sh --name <WORKER_NAME> [--model <MODEL_ID>] [--mcp-servers s1,s2] [--skills s1,s2] [--find-skills] [--skills-api-url <URL>] [--remote] [--runtime openclaw|copaw]
```

**Parameters**:
- `--name` (required): Worker name
- `--model`: optional, bare model name (e.g. `qwen3.5-plus`). Defaults to `${HICLAW_DEFAULT_MODEL}`
- `--mcp-servers`: optional, comma-separated MCP server names. Defaults to all existing MCP servers
- `--skills`: comma-separated skill names determined in Step 1.5 (e.g. `file-sync,github-operations`). Defaults to `file-sync` if omitted. `file-sync` is always included automatically
- `--find-skills`: enable find-skills capability (allows Worker to discover and install skills from skills.sh or private registry)
- `--skills-api-url`: custom skills registry URL (default: https://skills.sh). Only used when `--find-skills` is set
- `--remote`: force output install command instead of starting container locally
- `--runtime`: `openclaw` (default) or `copaw`. Use `copaw` for Python-based Workers that run via `pip install copaw-worker` instead of a container image

**Runtime: `copaw`**

When `--runtime copaw` is specified:
- Container startup is skipped entirely (copaw workers are pip-installed processes, not containers)
- `status` is always `"pending_install"` — the admin must run the `install_cmd` on the target machine
- The install command does **not** include `--console-port` by default (~100MB RAM). To enable the web console, append `--console-port 8088` to the command — note this adds ~500MB RAM overhead. If port 8088 is already in use, change it to another free port.
- The worker entry in `workers-registry.json` will have `"runtime": "copaw"`
- Lifecycle management (auto-stop/start) is skipped for copaw workers — they are treated like remote workers

**Deployment behavior** (without `--remote`):
- If container socket is available: auto-starts Worker container locally
- If no socket: falls back to outputting install command

The script outputs a JSON result after `---RESULT---`:

```json
{
  "worker_name": "xiaozhang",
  "matrix_user_id": "@xiaozhang:matrix-local.hiclaw.io:8080",
  "room_id": "!abc:matrix-local.hiclaw.io:8080",
  "consumer": "worker-xiaozhang",
  "skills": ["file-sync", "github-operations"],
  "mode": "local",
  "container_id": "abc123...",
  "status": "ready"
}
```

**`status` values:**
- `"ready"` — Worker container is running and the OpenClaw gateway confirmed healthy. Safe to report success to admin.
- `"starting"` — Container is running but the gateway health check timed out (120 s). The Worker may still be initializing (e.g. slow MinIO sync on first boot). Report this to admin and suggest they check `container_logs_worker` after a minute.
- `"pending_install"` — Local container runtime not available. Admin must run the `install_cmd` on the target machine.

Report the result to the human admin. If `status` is `"pending_install"`, provide the `install_cmd` from the JSON output. Also remind the admin that for remote deployment, the Worker machine must be able to resolve these domains to the Manager's IP (via DNS or `/etc/hosts`):

- `${HICLAW_MATRIX_DOMAIN}` (Matrix homeserver, e.g. `matrix-local.hiclaw.io`)
- `${HICLAW_AI_GATEWAY_DOMAIN}` (AI Gateway for LLM and MCP, e.g. `aigw-local.hiclaw.io`)
- `${HICLAW_FS_DOMAIN}` (MinIO file system, e.g. `fs-local.hiclaw.io`)

For local deployment these are auto-resolved via container ExtraHosts.

### Post-creation verification

After a local deployment (`mode: "local"`), verify the Worker is running:

```bash
bash -c 'source /opt/hiclaw/scripts/lib/container-api.sh && container_status_worker "<WORKER_NAME>"'
bash -c 'source /opt/hiclaw/scripts/lib/container-api.sh && container_logs_worker "<WORKER_NAME>" 20'
```

### Post-creation greeting

Once the Worker is confirmed `ready`, send a message in the Worker's Room to kick off the introduction:

```
@<WORKER_NAME>:${HICLAW_MATRIX_DOMAIN} You're all set! Please introduce yourself to everyone in this room.
```

The Worker will greet the room. After the Worker's greeting, send a follow-up addressed to the admin:

```
@${HICLAW_ADMIN_USER}:${HICLAW_MATRIX_DOMAIN} <WORKER_NAME> is ready. When giving them tasks or instructions, remember to @mention them so they see your message.

Note: By default, Workers can only be @mentioned by you (Manager) and the human admin — not by other Workers. This prevents accidental mutual-mention loops between Workers. If a project requires Workers to coordinate directly with each other, that can be enabled explicitly per-project.
```

## Monitor Workers

### Heartbeat Check (automated every 15 minutes)

The heartbeat prompt triggers automatically. When it fires:

1. Scan `~/hiclaw-fs/shared/tasks/*/meta.json` to find all tasks with `"status": "assigned"`
2. For each in-progress task, read `assigned_to` and `room_id` from its meta.json
3. Ask the assigned Worker for status in their Room
4. If a Worker confirms completion, update the task's meta.json: `"status": "completed"`, fill in `completed_at`
5. Assess capacity vs pending tasks (count `"status": "assigned"` tasks vs idle Workers)

### Manual Status Check

```bash
# List all in-progress tasks with their assigned Workers:
for meta in ~/hiclaw-fs/shared/tasks/*/meta.json; do
  jq -r '[.task_id, .assigned_to, .status] | @tsv' "$meta"
done

# Check a Worker's Room for recent activity:
curl -s "http://127.0.0.1:6167/_matrix/client/v3/rooms/<ROOM_ID>/messages?dir=b&limit=5" \
  -H "Authorization: Bearer <MANAGER_TOKEN>" | jq '.chunk[].content.body'
```

## Worker Lifecycle Management

The Manager automatically detects idle Workers during Heartbeat and stops their containers; when assigning tasks it automatically wakes up stopped containers. All state is persisted in `~/worker-lifecycle.json` (local only, never synced to MinIO).

### worker-lifecycle.json Structure

```json
{
  "version": 1,
  "idle_timeout_minutes": 30,
  "updated_at": "2026-02-21T10:00:00Z",
  "workers": {
    "alice": {
      "container_status": "stopped",
      "idle_since": "2026-02-21T10:00:00Z",
      "auto_stopped_at": "2026-02-21T10:31:00Z",
      "last_started_at": "2026-02-21T08:00:00Z"
    }
  }
}
```

Fields:
- `container_status`: actual status synced from the Docker API (`running` / `stopped` / `not_found` / `remote`)
- `idle_since`: timestamp when the Worker last had no active finite tasks; set to null when a finite task is active
- `auto_stopped_at`: when the Manager auto-stopped the container (audit trail)
- `last_started_at`: when the Manager last started/woke the container

`container_status = "remote"` means the Worker is remotely deployed (no container API access) and is excluded from automatic lifecycle management.

### Manual Commands

```bash
# Sync all Worker container statuses into the lifecycle file
bash /opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh --action sync-status

# Check for idle Workers and auto-stop those that have exceeded the timeout
bash /opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh --action check-idle

# Manually stop a Worker container
bash /opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh --action stop --worker <name>

# Manually wake up (start) a stopped Worker container
bash /opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh --action start --worker <name>

# Update a Worker's model (patches openclaw.json in MinIO and notifies the Worker to reload)
bash /opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh --action update-model --worker <name> --model <model-id>
```

The `update-model` action:
1. Resolves the correct `contextWindow` and `maxTokens` for the given model (same mapping as Manager startup)
2. Patches the Worker's `openclaw.json` in MinIO in-place (preserves all other config)
3. Updates `workers-registry.json` with the new model name
4. Sends a Matrix @mention to the Worker asking it to use the `file-sync` skill to pick up the change

If the Worker container is stopped, the config is still updated in MinIO — it will take effect on next start.

### Changing the Idle Timeout

Edit `~/worker-lifecycle.json` directly and update the `idle_timeout_minutes` field (default: 30):

```bash
# Example: change to 60 minutes
jq '.idle_timeout_minutes = 60' ~/worker-lifecycle.json > /tmp/lc.json && mv /tmp/lc.json ~/worker-lifecycle.json
```

### start vs create

| Situation | Command | Notes |
|-----------|---------|-------|
| Container is stopped | `lifecycle-worker.sh --action start` | Restarts the existing container, preserving all config and mounts |
| Container does not exist (`not_found`) | `create-worker.sh` | Rebuilds from image; full registration flow required |
| Worker needs reset or config update | `create-worker.sh` (removes old container first) | Full rebuild; Matrix account is reused |
| copaw runtime worker | `copaw-worker run --name <name> ...` (on target machine) | Not container-managed; lifecycle scripts skip these workers |

## Enable Peer Mentions Between Workers

By default, Workers can only be @mentioned by Manager and the human admin — not by each other. This prevents infinite mutual-mention loops in project rooms.

When the human admin explicitly requests that certain Workers should be able to trigger each other directly (e.g., for async handoffs without Manager relay), use:

```bash
bash /opt/hiclaw/agent/skills/worker-management/scripts/enable-peer-mentions.sh \
    --workers alice,bob,charlie
```

This script:
1. Adds each Worker in the group to every other Worker's `groupAllowFrom`
2. Pushes the updated `openclaw.json` to MinIO for each affected Worker
3. Sends a Matrix @mention to each updated Worker asking them to run `hiclaw-sync`

**Important**: Brief the Workers after enabling peer mentions — remind them **not to @mention each other in celebration or acknowledgment messages**, only when they have blocking information that cannot go through Manager. Uncontrolled inter-worker @mentions cause response loops.

## Reset a Worker

1. Revoke the Worker's Higress Consumer (or update credentials)
2. Remove Worker from AI route auth configs (`/v1/ai/routes` — GET, remove from allowedConsumers, PUT)
3. Remove Worker from MCP Server consumer lists (`/v1/mcpServer/consumers`)
4. Delete Worker's config directory: `rm -rf ~/hiclaw-fs/agents/<WORKER_NAME>/`
5. Re-create: write a new SOUL.md and run `create-worker.sh` again (the script handles re-registration gracefully)

## Manage Worker Skills

Manager centrally manages all Worker skills. The canonical skill definitions live in `~/worker-skills/`. Worker skill assignments are tracked in `~/workers-registry.json`.

### workers-registry.json

Location: `~/workers-registry.json`

Format:
```json
{
  "version": 1,
  "updated_at": "2026-01-01T00:00:00Z",
  "workers": {
    "<worker-name>": {
      "matrix_user_id": "@<name>:<domain>",
      "room_id": "!xxx:<domain>",
      "runtime": "openclaw",
      "skills": ["file-sync", "github-operations"],
      "created_at": "2026-01-01T00:00:00Z",
      "skills_updated_at": "2026-01-01T00:00:00Z"
    }
  }
}
```

`runtime` is `"openclaw"` (default, container-based) or `"copaw"` (pip-installed Python process). Omitted field defaults to `"openclaw"` for backward compatibility.

`file-sync` is the bootstrap skill (image-managed) and is always included.

### worker-skills/ Directory Structure

```
~/worker-skills/
├── README.md
└── github-operations/
    └── SKILL.md
```

To add a new skill, create a new subdirectory here with a `SKILL.md` (with `assign_when` frontmatter) and optional `scripts/`.

### push-worker-skills.sh

```bash
# Push all skills for a specific worker
bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh --worker <name>

# Push a skill to all workers that have it (e.g., after updating the skill definition)
bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh --skill <skill-name>

# Add a new skill to a worker and push it
bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh --worker <name> --add-skill <skill-name>

# Remove a skill from a worker (updates registry; skill files remain in MinIO until manually removed)
bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh --worker <name> --remove-skill <skill-name>

# Skip Matrix notification (e.g., when worker is not yet running)
bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh --worker <name> --no-notify
```

After pushing skills, the script notifies the affected Worker(s) via Matrix @mention to use the `file-sync` skill. Workers' periodic 5-minute sync also serves as a fallback.

### How to Add a New Custom Skill

1. Create the skill directory under `~/worker-skills/<skill-name>/` and write its files (`SKILL.md` must include `name`, `description`, and `assign_when` frontmatter; place any scripts under `scripts/`). The manager workspace is local only — use `push-worker-skills.sh` to distribute skills to workers.

2. Assign to Worker：
   ```bash
   bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh \
     --worker <name> --add-skill <skill-name>
   ```

## Important Notes

- Workers are **stateless containers** -- all state is in MinIO. Resetting a Worker just means recreating its config files
- Worker Matrix accounts persist in Tuwunel (cannot be deleted via API). Reuse same username on reset
- OpenClaw config hot-reload: file-watch (~300ms) or `config.patch` API
- **File sync**: after writing any file that a Worker (or another Worker) needs to read, always notify the target Worker via Matrix to use their `file-sync` skill. This applies to config updates, task briefs, shared data, and cross-Worker collaboration artifacts. The exact sync command varies by runtime — the Worker's `file-sync` SKILL.md defines how to execute it. Background periodic sync (every 5 minutes) serves as fallback only
- **Skills are Manager-controlled**: Workers cannot modify their own skills (local→remote sync excludes `skills/**`). Only Manager can push skill changes via `push-worker-skills.sh`

