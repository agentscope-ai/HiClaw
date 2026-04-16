---
name: debug-worker-management
description: Use when admin asks to create a DebugWorker to diagnose, investigate, or monitor Workers or Teams. DebugWorkers are temporary diagnostic agents with read-only access to target Workers' workspaces and Matrix messages.
---

# Debug Worker Management

DebugWorkers are temporary Workers created to diagnose issues with other Workers. They have read-only access to target Workers' workspaces, LLM session logs, and Matrix messages.

## Create DebugWorker

```bash
# Debug specific workers (auto-adds your Matrix ID to allowedUsers)
hiclaw debug create --target <worker1> --target <worker2>

# Debug with explicit allowed users (so they can @mention the DebugWorker)
hiclaw debug create --target <worker1> \
  --allowed-user '@dev-leader:hiclaw-tuwunel.hiclaw.svc.cluster.local'

# Debug with a custom name
hiclaw debug create --name debug-my-issue --target <worker1>

# Debug with Matrix message export (provide your credentials)
hiclaw debug create --target <worker1> \
  --matrix-user-id '@admin:hiclaw-tuwunel.hiclaw.svc.cluster.local' \
  --matrix-access-token '<your-access-token>'

# Debug with source code cross-referencing
hiclaw debug create --target <worker1> --hiclaw-version main
```

**IMPORTANT**: If a Team Leader or Human needs to interact with the DebugWorker directly, you MUST pass their Matrix user ID via `--allowed-user`. Without this, only you (Manager) and Admin can @mention the DebugWorker. You can find user Matrix IDs from the Team or Human CRD status.

The DebugWorker will be created as a standard Worker with:
- Read-only OSS access to all target Workers' workspaces
- `debug-analysis` skill for workspace sync, message export, and report generation
- A SOUL.md tailored for diagnostic tasks

## Check Status

```bash
hiclaw debug list
hiclaw debug get <name>
```

Wait for phase `Running` before interacting with the DebugWorker.

## Interact with DebugWorker

Once running, the DebugWorker joins a Matrix room. You can @mention it to ask for diagnostics:

```
@<debugworker-name> Please diagnose <worker-name>:
1. Sync their workspace and list files
2. Review recent LLM session logs for errors
3. Export their Matrix messages from the last 6 hours
4. Provide a diagnostic report
```

## Delete DebugWorker

Always clean up when done — DebugWorkers are temporary.

```bash
hiclaw debug delete <name>
```

## When to Use

- A Worker is producing incorrect results or appears stuck
- You need to review conversation history between agents
- You suspect misconfiguration in a Worker's SOUL.md or openclaw.json
- You need to inspect LLM session logs to understand decision-making
- Admin asks to "debug", "diagnose", "investigate", or "monitor" a Worker or Team
