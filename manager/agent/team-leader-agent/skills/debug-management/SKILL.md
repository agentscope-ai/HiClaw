---
name: debug-management
description: Use when you need to create a DebugWorker to investigate issues in your team. Creates a DebugWorker that can access team members' workspaces and Matrix messages for diagnosis.
---

# Debug Management

Use this skill when a team member is behaving unexpectedly, producing wrong results, or appears stuck, and you need to investigate by inspecting their workspace files, LLM session logs, or Matrix conversation history.

A DebugWorker is a temporary Worker with read-only access to your team members' workspaces. It has a dedicated `debug-analysis` skill for syncing workspace files, exporting Matrix messages, and reviewing LLM session logs.

## Create DebugWorker for your Team

When called without `--target`, the controller automatically fills in all team members (leader + workers) as targets and uses your team name for the DebugWorker name.

```bash
# Create a DebugWorker targeting all members of your team (recommended)
hiclaw debug create

# Create with explicit targets (specific workers only)
hiclaw debug create --target <worker-name-1> --target <worker-name-2>

# Create with Matrix credentials for message export
hiclaw debug create \
  --matrix-user-id '@your-matrix-id:domain' \
  --matrix-access-token 'syt_xxx'

# Create with a custom name and model
hiclaw debug create --name debug-my-team --model qwen3-235b-a22b --target <worker-name>
```

## Check DebugWorker Status

```bash
# List all DebugWorkers
hiclaw debug list

# Get details of a specific DebugWorker
hiclaw debug get <debugworker-name>
```

Wait for the DebugWorker's phase to become `Running` before interacting with it.

## Delete DebugWorker

Delete the DebugWorker when debugging is complete. This also removes the underlying Worker and cleans up resources.

```bash
hiclaw debug delete <debugworker-name>
```

## When to Use

- A worker is producing incorrect or incomplete results
- A worker appears stuck and is not making progress
- You need to review the conversation history between agents in a Matrix room
- You suspect a misconfiguration in a worker's openclaw.json or SOUL.md
- You need to inspect LLM session logs to understand a worker's decision-making

## What the DebugWorker Can Do

Once running, the DebugWorker has:
- **Read-only access** to all target workers' OSS workspaces (synced to `~/debug-targets/<worker-name>/`)
- **Matrix message export** capability (if matrixCredential was provided)
- **debug-analysis skill** with commands for workspace sync and message export

You can message the DebugWorker in its Matrix room to ask it to investigate specific issues. It will sync target workspaces, read logs, and provide diagnostic reports.

## Lifecycle

- DebugWorkers are **temporary** — create one when needed, delete when done
- Deleting the DebugWorker CRD cascade-deletes the underlying Worker Pod
- There is no automatic cleanup; you must delete it yourself when finished
