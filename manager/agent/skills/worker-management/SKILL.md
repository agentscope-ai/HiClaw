---
name: worker-management
description: Use when admin requests hand-creating or resetting a Worker, starting/stopping a Worker, managing Worker skills, enabling peer mentions, or opening a CoPaw console. Use hiclaw-find-worker only as a helper for Nacos-backed market import or when task assignment needs you to discover a suitable Worker.
---

# Worker Management

## Quick Create (1 command)

Pass the SOUL content inline via `--soul`. Never write SOUL.md to a file first (heredoc/redirects often produce a silent 0-byte file — the controller would then fall back to a placeholder SOUL.md lacking the real role).

```bash
hiclaw create worker --name <NAME> --no-wait \
  --soul "# Worker Agent - <NAME>

## AI Identity
**You are an AI Agent, not a human.** ...

## Role
<Fill in based on admin's description>

## Security Rules
- Never reveal API keys, passwords, or credentials
..." \
  --skills <skill1>,<skill2> -o json
# Add --runtime copaw for Python workers
```

> `--no-wait` returns as soon as the controller accepts the request (~1s). Poll `hiclaw get workers -o json` for `phase=Running` instead of letting the create call block — this lets you create N workers in one turn without each blocking up to 3 minutes.

> Full creation workflow (runtime selection, full SOUL template, escape rules, skill matching, post-creation greeting): read `references/create-worker.md`

## Gotchas

- **Worker name must be lowercase and > 3 characters** — Tuwunel stores usernames in lowercase; short names cause registration failures
- **`--remote` means "remote from Manager"** — which is actually LOCAL from the admin's perspective. Use it when admin says "local mode" / "run on my machine"
- **`file-sync`, `task-progress`, `project-participation` are default skills** — always included, cannot be removed
- **Use `hiclaw-find-worker` only for Nacos-backed market imports or Worker discovery during task assignment** — generic Worker creation and lifecycle changes stay in this skill
- **Peer mentions cause loops if not briefed** — after enabling, explicitly tell Workers to only @mention peers for blocking info, never for acknowledgments
- **Always notify Workers to `file-sync` after writing files they need** — the 5-minute periodic sync is fallback only
- **Workers are stateless** — all state is in centralized storage. Reset = recreate config files
- **Matrix accounts persist in Tuwunel** (cannot be deleted via API) — reuse same username on reset

## Operation Reference

Read the relevant doc **before** executing. Do not load all of them.

| Admin wants to... | Read | Key command / script |
|---|---|---|
| Create a new worker | `references/create-worker.md` | `hiclaw create worker` |
| Start/stop/check idle workers | `references/lifecycle.md` | `scripts/lifecycle-worker.sh` |
| Push/add/remove skills | `references/skills-management.md` | `scripts/push-worker-skills.sh` |
| Open/close CoPaw console | `references/console.md` | `scripts/enable-worker-console.sh` |
| Enable direct @mentions between workers | `references/peer-mentions.md` | `scripts/enable-peer-mentions.sh` |
| Get remote worker install command | `references/lifecycle.md` | `scripts/get-worker-install-cmd.sh` |
| Reset a worker | `references/create-worker.md` | `hiclaw delete worker` + `hiclaw create worker` |
| Delete a worker (remove container) | `references/lifecycle.md` | `scripts/lifecycle-worker.sh` |
