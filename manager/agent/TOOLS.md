# Management Skills — Quick Reference

Each skill has a full `SKILL.md` in `skills/<name>/`. This file is your cheat sheet for when to reach for each one.

---

## 🚀 Quick: Create Worker

**Copy-paste ready — use this directly:**

```bash
# Step 1: Create worker directory and SOUL.md
mkdir -p ~/hiclaw-fs/agents/<NAME>
cat > ~/hiclaw-fs/agents/<NAME>/SOUL.md << 'EOF'
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

### Runtime Selection (MUST check before running create script)

| Admin says (any of these keywords) | Runtime flag |
|-------------------------------------|-------------|
| "copaw", "CoPaw", "Python worker", "pip worker", "host worker" | `--runtime copaw` |
| "openclaw", "container worker", "docker worker", or **none of the above** | _(default, no flag needed)_ |

If the admin mentions "copaw" anywhere in the request, always pass `--runtime copaw`.

### Skills Recommendation Table

| Worker Type | Skills | Flags |
|-------------|--------|-------|
| Development (coding, DevOps, review) | `coding-cli,github-operations,git-delegation` | `--find-skills` |
| Data / Analysis | `coding-cli` | `--find-skills` |
| General Purpose | _(default)_ | `--find-skills` |

> `file-sync` is always auto-included. `--find-skills` lets the Worker discover and install additional skills on-demand. Trim skills that clearly don't apply (e.g., drop `github-operations` for a pure frontend worker).

---

## task-management

Assign, track, and complete tasks for Workers.

- Admin gives a task and no Worker is specified → Worker availability check (Step 0)
- Assigning a finite or infinite task to a Worker → create task directory, write `meta.json` + `spec.md`, notify Worker
- Worker @mentions you with completion → update `meta.json`, remove from `state.json`, log to memory

## task-coordination

Must wrap any shared task directory modification.

- About to run git-delegation or coding-cli → use this first to check/create `.processing` marker
- Git or CLI work completes → use this to remove the marker and sync to MinIO

## git-delegation-management

Workers can't run git; execute git ops on their behalf.

- Worker sends: `task-20260220-100000 git-request: operations: [git clone ..., git checkout -b feature-x]`
- Worker asks you to commit and push their changes, rebase a branch, or resolve a conflict

## coding-cli-management

Run AI coding CLI in a Worker's workspace on their behalf.

- First coding task arrives and `~/coding-cli-config.json` doesn't exist → detect available CLIs, ask admin, write config
- Worker sends: `task-20260220-100000 coding-request: ---PROMPT--- [prompt] ---END---`

## worker-management

Full lifecycle of Worker containers and skill assignments.

- Admin says "create a copaw worker" or "create a copaw named Alice" → use `--runtime copaw` (see Runtime Selection table above)
- Admin says "create a new Worker named Alice for code review tasks"
- Before assigning a task, Worker container is `stopped` → wake it up first; `not_found` → tell admin to recreate
- Admin says "add the github-operations skill to Alice" or "reset the Bob worker"

**After creating a Worker**, always tell the admin:
1. A 3-person room (Human + Manager + Worker) has been created — please check your Matrix invitations and accept it
2. In any group room with 3+ people, you must **@mention** the person you want to respond — they only wake up when explicitly mentioned
3. You can also click the Worker's avatar to open a **direct message** with them — no @mention needed, and the conversation is private (Manager cannot see it)

## project-management

> **Rule: if the admin explicitly wants multiple Workers to collaborate on something, always use this skill — do not assign tasks individually.**

Multi-Worker collaborative projects.

- Admin says "kick off the website redesign project with Alice and Bob"
- Worker @mentions you with task completion in a project room → update `plan.md`, assign next task
- A task reports `REVISION_NEEDED` → trigger revision workflow; or a task is `BLOCKED` → escalate

## channel-management

Multi-channel admin identity and primary notification routing.

- Admin messages from any non-Matrix channel for the first time → run first-contact protocol, ask about primary channel
- Admin says "switch my primary channel to Discord"
- Working in a Matrix room and need an urgent admin decision → cross-channel escalation

## higress-gateway-management

Higress AI Gateway: consumers, routes, LLM providers.

- Creating a new Worker → create its Higress consumer and grant it AI route access
- Admin provides a DeepSeek API key and wants to add it as a new LLM provider
- Need to rotate an expired API key for an existing provider

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

Switch the **Manager's own** LLM model. Do NOT use this for Workers.

- Admin says "switch your model to X" or "change the Manager model to X"

## worker-model-switch

Switch a **Worker's** LLM model. Do NOT use this for the Manager.

- Admin says "switch Alice's model to claude-sonnet-4-6" or "change the Worker model to X"
- Patches the Worker's `openclaw.json` in MinIO, updates registry, and notifies the Worker to reload via file-sync

> **Model switch cheat sheet:** Manager model → `model-switch` skill. Worker model → `worker-model-switch` skill. Never mix them up.
>
> **⚠️ MANDATORY:** When switching any model (Manager or Worker), you MUST use the corresponding skill script above. Do NOT use `session_status` tool, do NOT call Higress API directly, do NOT manually edit `openclaw.json` or any config file. The scripts handle gateway testing, config patching, registry updates, and Worker notification — skipping them will cause inconsistent state.

---

Add local notes below — SSH aliases, API endpoints, environment-specific details that don't belong in SKILL.md.
