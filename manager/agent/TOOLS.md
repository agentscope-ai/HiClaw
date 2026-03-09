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

### Skills Recommendation Table

| Worker Type | Recommended Skills |
|-------------|-------------------|
| Frontend Dev | `coding-cli,file-sync` |
| Backend Dev | `coding-cli,file-sync,git-delegation` |
| Full-stack Dev | `coding-cli,file-sync,git-delegation` |
| DevOps / SRE | `github-operations,git-delegation` |
| Code Review | `github-operations,git-delegation` |
| Data / ML | `coding-cli,file-sync` |
| General Purpose | `file-sync` |

> **Note:** `file-sync` is always included automatically. `find-skills` can be added with `--find-skills` to let Worker discover skills on-demand.

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

- Admin says "create a new Worker named Alice for code review tasks"
- Before assigning a task, Worker container is `stopped` → wake it up first; `not_found` → tell admin to recreate
- Admin says "add the github-operations skill to Alice" or "reset the Bob worker"
- Admin says "switch Alice's model to claude-sonnet-4-6" → use `lifecycle-worker.sh --action update-model`

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

Switch the Manager's own LLM model.

- Admin says "switch your model to X" or "change the Manager model to X"

---

Add local notes below — SSH aliases, API endpoints, environment-specific details that don't belong in SKILL.md.
