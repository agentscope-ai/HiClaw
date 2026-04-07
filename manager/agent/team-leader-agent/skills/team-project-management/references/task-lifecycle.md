# Task Lifecycle (within Team Projects)

## Task Directory Convention

All team project tasks live under:
```
teams/{team-name}/tasks/{task-id}/
├── meta.json      # Task metadata (Leader-owned)
├── spec.md        # Requirements (Leader-owned, read-only to workers)
├── result.md      # Outcome (Worker-written)
├── plan.md        # Execution plan (Worker-written)
├── base/          # Reference files (Leader-maintained, read-only to workers)
└── workspace/     # Shared workspace (Worker + Leader)
```

## Assign a Task

### 1. Create task files

```bash
TASK_ID="st-01"
TASK_DIR="/root/hiclaw-fs/shared/tasks/${TASK_ID}"
mkdir -p "${TASK_DIR}"
```

Write `meta.json`:
```json
{
  "task_id": "st-01",
  "project_id": "tp-20260331-100000",
  "task_title": "Design database schema",
  "assigned_to": "alice",
  "status": "assigned",
  "depends_on": [],
  "assigned_at": "2026-03-31T10:00:00Z"
}
```

Write `spec.md` with:
- Task title and project context
- Deliverables and acceptance criteria
- Constraints and references
- Task Directory Convention reminder:
  - Create `plan.md` before starting
  - All artifacts stay in the task directory
  - Write `result.md` when done
  - Push with: `mc mirror ... --overwrite --exclude "spec.md" --exclude "base/"`

### 2. Sync to MinIO

```bash
mc cp ${TASK_DIR}/meta.json ${HICLAW_STORAGE_PREFIX}/teams/${TEAM_NAME}/shared/tasks/${TASK_ID}/meta.json
mc cp ${TASK_DIR}/spec.md ${HICLAW_STORAGE_PREFIX}/teams/${TEAM_NAME}/shared/tasks/${TASK_ID}/spec.md
```

### 3. Update plan.md

Change `[ ]` to `[~]` for this task. Sync plan.md to MinIO.

### 4. Register in state

```bash
bash ./skills/team-task-management/scripts/manage-team-state.sh \
  --action add-finite --task-id st-01 --title "Design database schema" \
  --assigned-to alice --room-id '!teamroom:domain' \
  --source manager --parent-task-id task-xxx
```

### 5. @mention Worker

In Team Room:
```
@alice:{domain} New task [st-01]: Design database schema
Pull spec: shared/tasks/st-01/spec.md
Please file-sync, read the spec, create plan.md before starting. @mention me when complete.
```

## Handle Completion

### 1. Pull results

```bash
mc mirror ${HICLAW_STORAGE_PREFIX}/teams/${TEAM_NAME}/shared/tasks/${TASK_ID}/ ${TASK_DIR}/ --overwrite
```

### 2. Read result.md

Check the `Outcome` → `Status` field.

### 3. SUCCESS / SUCCESS_WITH_NOTES

1. Update `meta.json`: `status → completed`, fill `completed_at`
2. Update plan.md: `[~]` → `[x]`, add Change Log entry
3. Complete in state: `manage-team-state.sh --action complete --task-id st-01`
4. Sync to MinIO
5. Run `resolve-dag.sh --action ready` to find next tasks

### 4. REVISION_NEEDED

1. Update plan.md: `[~]` → `[→]`
2. Create revision task with `is_revision_for` and `triggered_by` in meta.json
3. Assign revision to original worker (or as specified)
4. **Do NOT proceed to dependent tasks** until revision completes

### 5. BLOCKED

1. Update plan.md: `[~]` → `[!]`
2. Escalate based on source:
   - source=manager → @mention Manager in Leader Room
   - source=team-admin → @mention Team Admin in Leader DM

## result.md Format

```markdown
# Task Result: {title}

**Task ID**: {task-id}
**Completed**: {ISO datetime}

## Outcome

**Status**: SUCCESS | SUCCESS_WITH_NOTES | REVISION_NEEDED | BLOCKED

## Summary

{Brief summary of what was done}

## Deliverables

{List of completed deliverables}

## Notes

{Any notes, issues, or suggestions}
```
