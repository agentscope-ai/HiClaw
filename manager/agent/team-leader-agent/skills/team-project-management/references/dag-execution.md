# DAG Execution

## Overview

After creating a project and filling in plan.md with the DAG task plan, follow this workflow to execute tasks respecting dependency order.

## Execution Loop

```
1. resolve-dag.sh --action ready → get unblocked pending tasks
2. For each ready task:
   a. Create task directory: teams/{team}/shared/tasks/{task-id}/
   b. Write meta.json + spec.md
   c. Push to MinIO
   d. Update plan.md: [ ] → [~]
   e. Register in team-state.json: manage-team-state.sh --action add-finite
   f. @mention worker in Team Room
3. Wait for worker completion
4. On completion:
   a. Pull task directory from MinIO
   b. Read result.md
   c. Update plan.md: [~] → [x] (or [!] if blocked, [→] if revision)
   d. Update team-state.json: manage-team-state.sh --action complete
   e. Sync plan.md to MinIO
   f. Go to step 1 (resolve next wave)
5. When all tasks [x] → aggregate results → complete project
```

## Step-by-Step

### Assign Ready Tasks

```bash
# Get ready tasks
READY=$(bash ./skills/team-project-management/scripts/resolve-dag.sh \
  --plan /root/hiclaw-fs/shared/projects/{project-id}/plan.md \
  --action ready)

# For each ready task, create task files
TASK_ID="st-01"
TASK_DIR="/root/hiclaw-fs/shared/tasks/${TASK_ID}"
mkdir -p "${TASK_DIR}"
```

Write `meta.json`:
```json
{
  "task_id": "st-01",
  "project_id": "tp-xxx",
  "task_title": "Design database schema",
  "assigned_to": "alice",
  "status": "assigned",
  "depends_on": [],
  "assigned_at": "ISO-8601"
}
```

Write `spec.md` with: task title, project context, deliverables, constraints, and the Task Directory Convention (worker creates plan.md, writes result.md when done).

Push to MinIO:
```bash
mc cp ${TASK_DIR}/meta.json ${HICLAW_STORAGE_PREFIX}/teams/{team}/shared/tasks/${TASK_ID}/meta.json
mc cp ${TASK_DIR}/spec.md ${HICLAW_STORAGE_PREFIX}/teams/{team}/shared/tasks/${TASK_ID}/spec.md
```

Update plan.md marker from `[ ]` to `[~]`. Sync plan.md to MinIO.

@mention worker in Team Room:
```
@alice:{domain} New task [st-01]: Design database schema
Pull spec: shared/tasks/st-01/spec.md
@mention me when complete.
```

### Handle Completion

When worker @mentions you with completion:

1. Pull from MinIO:
```bash
mc mirror ${HICLAW_STORAGE_PREFIX}/teams/{team}/shared/tasks/${TASK_ID}/ ${TASK_DIR}/ --overwrite
```

2. Read `result.md` for outcome status.

3. Based on outcome:

| Outcome | Action |
|---------|--------|
| `SUCCESS` | Update plan.md `[~]` → `[x]`, complete in state.json, resolve next wave |
| `SUCCESS_WITH_NOTES` | Same as SUCCESS, record notes |
| `REVISION_NEEDED` | Update plan.md `[~]` → `[→]`, create revision task |
| `BLOCKED` | Update plan.md `[~]` → `[!]`, escalate to Manager or Team Admin |

4. After marking `[x]`, immediately run:
```bash
bash ./skills/team-project-management/scripts/resolve-dag.sh \
  --plan /root/hiclaw-fs/shared/projects/{project-id}/plan.md \
  --action ready
```

5. Assign all newly unblocked tasks (they may be parallelizable).

### Parallel Execution

When `resolve-dag.sh --action ready` returns multiple tasks:
- Assign **all** of them simultaneously
- Workers execute in parallel
- As each completes, run `resolve-dag.sh --action ready` again to check for newly unblocked tasks

### Project Completion

When all tasks in plan.md are `[x]`:

1. Aggregate results from all task `result.md` files
2. Check `source` in project meta.json:
   - **source=manager**: Write aggregated `result.md` to `shared/tasks/{parent-task-id}/result.md`, push to MinIO, @mention Manager in Leader Room
   - **source=team-admin**: Write summary in project directory, @mention Team Admin in Leader DM
3. Update project meta.json: `status → completed`
4. Update team-state.json: `manage-team-state.sh --action complete-project --project-id P`
5. Sync to MinIO

## Heartbeat Integration

During heartbeat, for each active project:
1. Pull plan.md from MinIO
2. Run `resolve-dag.sh --action ready`
3. If ready tasks exist but are not assigned → assign them (may have been missed)
4. If `[~]` tasks have been in-progress too long → follow up with worker
5. If worker unresponsive after 2 cycles → escalate based on source
