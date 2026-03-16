# Management Skills — Quick Reference

Each skill has a full `SKILL.md` in `skills/<name>/`. This file is your cheat sheet for when to reach for each one.

---

## 🚀 Quick: Create Worker

**Copy-paste ready — use this directly:**

```bash
# Step 1: Create worker directory and SOUL.md
mkdir -p /root/hiclaw-fs/agents/<NAME>
cat > /root/hiclaw-fs/agents/<NAME>/SOUL.md << 'EOF'
# <NAME> - Worker Agent

## AI Identity

**You are an AI Agent, not a human.**

- Both you and the Manager are AI agents that can work 24/7
- You do not need rest, sleep, or "off-hours"
- You can immediately start the next task after completing one
- Your time units are **minutes and hours**, not "days"

## Role
- **Name:** <NAME>
- **Role:** <DESCRIPTION>
- **Language:** zh (or en)

## Behavior
- Be helpful and concise
- Report progress regularly
EOF

# Step 2: Create worker with skills
bash /opt/hiclaw/agent/skills/worker-management/scripts/create-worker.sh \
  --name <NAME> \
  --skills <skill1>,<skill2>
```

### Runtime Selection

| Runtime | Memory | Deployment | Description |
|---------|--------|------------|-------------|
| `openclaw` | ~500MB | local only | Node.js container |
| `copaw` | ~150MB | local + remote (`--remote`) | Python container; with `--remote`, runs as native process on admin's machine instead |

Default runtime is set by `HICLAW_DEFAULT_WORKER_RUNTIME` (chosen during installation). Only pass `--runtime` explicitly when:
- The admin requests a specific runtime (e.g., "create a copaw worker" → `--runtime copaw`)
- You recommend a specific runtime to solve a problem (see below)

**Local environment access (--remote flag):** If the admin wants the Worker to run on their local machine — e.g., "create a local worker", "local mode", "access my local environment", open a browser, run desktop apps, access local files — add `--remote` to the create command. This works with copaw runtime only: `--runtime copaw --remote`. It outputs a `pip install copaw-worker && copaw-worker ...` command for the admin to run on their machine. The registry will have `"deployment": "remote"` for this Worker. Ask the admin to confirm before proceeding.

**Important:** Without `--remote`, a copaw Worker is a normal container — just like openclaw. Do not confuse `--runtime copaw` (container) with `--runtime copaw --remote` (admin's local machine).

> **Terminology note:** `--remote` means "remote from the Manager's perspective" (i.e., not a container managed by the Manager). From the admin's perspective, this is actually the **local** deployment — the Worker runs as a native process on the admin's own machine.

### Skills Recommendation Table

| Worker Type | Skills | Flags |
|-------------|--------|-------|
| Development (coding, DevOps, review) | `github-operations,git-delegation` | `--find-skills` |
| Data / Analysis | _(default)_ | `--find-skills` |
| General Purpose | _(default)_ | `--find-skills` |

> `file-sync` is always auto-included. `--find-skills` lets the Worker discover and install additional skills on-demand. Trim skills that clearly don't apply (e.g., drop `github-operations` for a pure frontend worker).

---

# Skills

Each `##` below is a skill (`skills/<name>/SKILL.md`). Use the skill name to locate its full documentation.

## task-management

Assign, track, and complete tasks for Workers.

- Admin gives a task and no Worker is specified → run `find-worker.sh` (Step 0) to get a consolidated view of all Workers (availability, workload, container status, role, skills) in one call
- Filter by required skills: `find-worker.sh --skills github-operations` — only returns workers that have all listed skills
- Assigning a finite task to a Worker → create task directory, write `meta.json` (type=finite) + `spec.md`, notify Worker
- Admin says "run a security scan every day at 9am" or any request with a recurring schedule → create an **infinite** task with `meta.json` (type=infinite, schedule, timezone) + `spec.md`, notify Worker. Heartbeat will trigger execution on schedule.
- Worker @mentions you with completion → update `meta.json`, run `manage-state.sh --action complete`, log to memory

## task-coordination

Must wrap any shared task directory modification.

- About to run git-delegation → use this first to check/create `.processing` marker
- Git work completes → use this to remove the marker and sync to MinIO

## git-delegation-management

Workers can't run git; execute git ops on their behalf.

- Worker sends: `task-20260220-100000 git-request: operations: [git clone ..., git checkout -b feature-x]`
- Worker asks you to commit and push their changes, rebase a branch, or resolve a conflict

## worker-management

Full lifecycle of Worker containers and skill assignments.

- Admin says "create a copaw worker" or "create a copaw named Alice" → use `--runtime copaw`
- Admin says "create a new Worker named Alice for code review tasks" → use default runtime (no `--runtime` flag)
- Admin says "local worker", "local mode", "access my local environment", "run on my machine", or wants Worker to control their local machine → always use `--runtime copaw --remote` (outputs a `pip install copaw-worker` command for the admin to run locally)
- Before assigning a task, Worker container is `stopped` → wake it up first; `not_found` → tell admin to recreate
- Admin says "add the github-operations skill to Alice" or "reset the Bob worker"
- Admin says "open console", "open terminal", "debug the worker" → run `enable-worker-console.sh --name <NAME>`, report the URL `http://<manager-host>:<console_host_port>` (CoPaw console is a browser-based management dashboard; only works for local CoPaw containers; `--action disable` to free ~500MB RAM)

**After creating a Worker**, always tell the admin:
1. A 3-person room (Human + Manager + Worker) has been created — please check your Matrix invitations and accept it
2. In any group room with 3+ people, you must **@mention** the person you want to respond — they only wake up when explicitly mentioned
3. You can also click the Worker's avatar to open a **direct message** with them — no @mention needed, and the conversation is private (Manager cannot see it)
4. In Element and other clients, type `@` then the first letter(s) of the worker's nickname to trigger the nickname autocomplete suggestions

## project-management

> **Rule: if the admin explicitly wants multiple Workers to collaborate on something, always use this skill — do not assign tasks individually.**

Multi-Worker collaborative projects.

- Admin says "kick off the website redesign project with Alice and Bob"
- Worker @mentions you with task completion in a project room → update `plan.md`, assign next task
- A task reports `REVISION_NEEDED` → trigger revision workflow; or a task is `BLOCKED` → escalate

## channel-management

Multi-channel identity recognition, permission enforcement, and primary notification routing.

Check current primary channel:
```bash
bash /opt/hiclaw/agent/skills/channel-management/scripts/manage-primary-channel.sh --action show
```

- In a group room with multiple human users → identify each sender as admin, trusted contact, or unknown (ignore unknown)
- Admin messages from a non-Matrix channel that doesn't match current primary channel → run first-contact protocol, ask about primary channel
- Admin says "switch my primary channel to Discord"
- Admin says "you can talk to the person who just messaged" → add trusted contact
- Working in a Matrix room and need an urgent admin decision → cross-channel escalation

## matrix-server-management

Direct Matrix homeserver operations (Worker/project creation use dedicated scripts — this skill is for explicit standalone requests only).

- Admin says "create a room for X", "invite Y to the project room"
- Admin says "register a Matrix account for my colleague"
- Admin asks you to send a file (task output, report, any artifact) → upload via media API, send as `m.file` message, reply with `MEDIA: <mxc://...>`

## mcp-server-management

MCP Server lifecycle and per-consumer access control.

- Admin provides a GitHub token and asks to enable the GitHub MCP server
- Need to grant a newly created Worker access to an existing MCP server
- Admin asks to restrict which MCP tools a specific Worker can call

## model-switch

Switch the **Manager's own** primary LLM model. Do NOT use this for Workers.

- Admin says "switch your model to X" or "change the Manager model to X"
- After the script runs, you must run `openclaw gateway restart` to apply the change

## worker-model-switch

Switch a **Worker's** primary LLM model. Do NOT use this for the Manager.

- Admin says "switch Alice's model to claude-sonnet-4-6" or "change the Worker model to X"
- Patches the Worker's `openclaw.json` in MinIO, updates registry, and notifies the Worker to file-sync
- After the script runs, you must recreate the Worker container to apply the change

> **Model switch cheat sheet:** Manager model → `model-switch` skill. Worker model → `worker-model-switch` skill. Never mix them up. Both require a restart after running the script.
>
> **⚠️ MANDATORY:** When switching any model (Manager or Worker), you MUST use the corresponding skill script above. Do NOT call Higress API directly, do NOT manually edit `openclaw.json` or any config file. The scripts handle gateway testing, config patching, registry updates, and Worker notification — skipping them will cause inconsistent state.

---

# Operations (not skills)

## 📥 Pulling Files from MinIO (File Sync)

Workers push their output (task results, artifacts, etc.) to MinIO. Your local `/root/hiclaw-fs/` is NOT automatically synced in real time — you must pull explicitly.

**When a Worker reports task completion**, always pull the task directory before reading:

```bash
mc mirror hiclaw/hiclaw-storage/shared/tasks/{task-id}/ /root/hiclaw-fs/shared/tasks/{task-id}/ --overwrite
cat /root/hiclaw-fs/shared/tasks/{task-id}/result.md
```

**When a Worker says they've uploaded a file but you can't find it locally**, ask the Worker to confirm the exact MinIO path, then pull it:

```bash
# Single file
mc cp hiclaw/hiclaw-storage/<path-worker-gave-you> /root/hiclaw-fs/<same-path>

# Directory
mc mirror hiclaw/hiclaw-storage/<dir>/ /root/hiclaw-fs/<dir>/ --overwrite
```

**File sync rules you must follow:**

1. When you write files to `/root/hiclaw-fs/`, always push to MinIO immediately via `mc cp` or `mc mirror`, then notify the target Worker via Matrix @mention to use their file-sync skill
2. When a Worker tells you they've pushed files to MinIO, always pull from MinIO before reading — never assume your local copy is up to date
3. If a local file is missing or stale after a Worker notification, pull it from MinIO directly — do not wait for background sync

---

Add local notes below — SSH aliases, API endpoints, environment-specific details that don't belong in SKILL.md.
