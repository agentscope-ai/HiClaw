---
name: file-sync
description: Sync files with centralized storage. Use when your coordinator or another Worker notifies you of file updates (config changes, task files, shared data, collaboration artifacts).
---

# File Sync (Hermes Worker)

## Sync agent config files

When your coordinator notifies you that your config has been updated (e.g., model switch, skill update), trigger an immediate sync:

```bash
hermes-sync
```

This pulls `openclaw.json`, `SOUL.md`, `AGENTS.md`, and skills from MinIO and re-bridges the config into `~/.hiclaw-worker/<your-name>/.hermes/` (config.yaml + .env).

**Hot reload caveats:**
- Hermes-agent reloads `SOUL.md` on the next message — no restart needed
- Skill changes in `~/.hiclaw-worker/<your-name>/.hermes/skills/` are picked up on the next message as well
- Changes that affect `config.yaml` (model, terminal, matrix behavior) require a process restart — your coordinator handles this when needed

**Automatic background sync:**
- Background sync also runs every 300 seconds (5 minutes) as a fallback
- Most file changes are picked up without manual sync

## Sync task / shared files

The `shared/` directory is automatically mirrored from MinIO at startup and every sync cycle. No manual pull is needed.

Task and project files are at:

| Local path (auto-synced) |
|---|
| `shared/tasks/{task-id}/` |
| `shared/projects/{project-id}/` |

```bash
# Read the spec (already synced locally)
cat shared/tasks/{task-id}/spec.md

# Push your results back to MinIO (push is still manual)
bash ./skills/file-sync/scripts/push-shared.sh tasks/{task-id}/ --exclude "spec.md" --exclude "base/"
```

The `push-shared.sh` script automatically detects your team and pushes to the correct MinIO path.

**When to use:**
- When you finish work: push results back to MinIO using `push-shared.sh`
- When told files have been updated urgently: run `hermes-sync` to trigger an immediate pull

Always confirm to the sender after push completes.

**Example workflow:**
```bash
# Coordinator assigns task: "New task [st-01]. Please file-sync and read shared/tasks/st-01/spec.md"
# Run file-sync to pull latest
hermes-sync

# Read the spec
cat shared/tasks/st-01/spec.md

# ... do the work ...

# Push results
bash ./skills/file-sync/scripts/push-shared.sh tasks/st-01/ --exclude "spec.md" --exclude "base/"

# Confirm to coordinator
"Task complete. Results pushed to MinIO."
```
