---
name: project-management
description: Manage multi-worker collaborative projects. Use when the human admin asks to start a project, when a Worker @mentions you with task completion in a project room, or when project plan changes are needed.
---

# Project Management

## Overview

This skill enables project-based collaboration across multiple Workers. A project has:
- A **Project Room** (Matrix room with Human + Manager + all participating Workers)
- A **plan.md** that tracks all tasks, assignees, dependencies, and progress
- A **meta.json** that tracks project-level metadata
- Individual **task files** under the standard `shared/tasks/{task-id}/` structure, referenced from plan.md

Storage layout:
```
shared/projects/{project-id}/
├── meta.json    # Project metadata
└── plan.md      # Living project plan (single source of truth)
```

---

## Step 1: Initiate a Project

When the human admin asks to start a project:

### 1a. Analyze and decompose

Break the project goal into phases and tasks. For each task identify:
- A clear title and deliverable
- Which Worker role is best suited
- Dependencies on other tasks (what must complete first)
- Expected output format

### 1b. Create project directory and files

```bash
PROJECT_ID="proj-$(date +%Y%m%d-%H%M%S)"
mkdir -p ~/hiclaw-fs/shared/projects/${PROJECT_ID}
```

Write **meta.json**:
```bash
cat > ~/hiclaw-fs/shared/projects/${PROJECT_ID}/meta.json << 'EOF'
{
  "project_id": "proj-YYYYMMDD-HHMMSS",
  "title": "<project title>",
  "project_room_id": null,
  "status": "planning",
  "workers": ["<worker1>", "<worker2>"],
  "created_at": "<ISO-8601>",
  "confirmed_at": null
}
EOF
```

Write **plan.md** (see format below):
```bash
cat > ~/hiclaw-fs/shared/projects/${PROJECT_ID}/plan.md << 'EOF'
...plan content...
EOF
```

### 1c. Create the Project Room

Use the `create-project.sh` script — it creates the room, invites all participants, and updates `openclaw.json` `groupAllowFrom` so Worker @mentions in the project room trigger you:

```bash
bash ~/hiclaw-fs/agents/manager/skills/project-management/scripts/create-project.sh \
  --id "${PROJECT_ID}" \
  --title "<title>" \
  --workers "worker1,worker2,worker3"
```

**MANDATORY**: The project room MUST always include the human admin (`@${HICLAW_ADMIN_USER}:${HICLAW_MATRIX_DOMAIN}`). The script handles this automatically. If you ever create a project room manually, the admin invite is non-negotiable — they must be able to see all project activity.

Save the returned `room_id` and update meta.json with it.

### 1d. Present plan to human for confirmation

Post the full plan.md content into the **DM with human admin** (not the project room yet) asking for confirmation:

```
I've drafted the project plan for "<title>". Please review and confirm to start:

[paste plan.md content here]

If you'd like changes, let me know. Otherwise, reply "确认" to begin.
```

Wait for human confirmation before proceeding.

> **YOLO mode**: Skip the confirmation gate. Auto-confirm immediately — update meta.json `status → active`, set `confirmed_at` to now, and proceed directly to Step 1e.

### 1e. After confirmation

1. Update meta.json: `"status": "planning" → "active"`, set `confirmed_at`
2. Sync to MinIO: `mc mirror ~/hiclaw-fs/shared/projects/${PROJECT_ID}/ hiclaw/hiclaw-storage/shared/projects/${PROJECT_ID}/ --overwrite`
3. Verify the human admin is in the project room — if not, invite them immediately:
   ```bash
   curl -X POST "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ID}/invite" \
     -H "Authorization: Bearer ${MANAGER_MATRIX_TOKEN}" \
     -H 'Content-Type: application/json' \
     -d "{\"user_id\": \"@${HICLAW_ADMIN_USER}:${HICLAW_MATRIX_DOMAIN}\"}"
   ```
4. Post the project plan in the project room (for all participants to see)
5. Assign the first task(s) by @mentioning the assigned Worker(s) in the project room

---

## plan.md Format

```markdown
# Project: {title}

**ID**: {project-id}
**Status**: planning | active | completed
**Room**: {room-id}
**Created**: {ISO date}
**Confirmed**: {ISO date or "pending"}

## Team

- @manager:{domain} — Project Manager
- @{worker1}:{domain} — {role description}
- @{worker2}:{domain} — {role description}

## Task Plan

### Phase 1: {phase name}

- [ ] {task-id} — {task title} (assigned: @{worker}:{domain})
  - Spec: ~/hiclaw-fs/shared/tasks/{task-id}/spec.md
  - Result: ~/hiclaw-fs/shared/tasks/{task-id}/result.md

### Phase 2: {phase name}

- [ ] {task-id} — {task title} (assigned: @{worker}:{domain}, depends on: {task-id})
  - Spec: ~/hiclaw-fs/shared/tasks/{task-id}/spec.md
  - Result: ~/hiclaw-fs/shared/tasks/{task-id}/result.md

## Change Log

- {ISO datetime}: Project initiated
- {ISO datetime}: Plan confirmed by human
```

**Task status markers:**
- `[ ]` — pending (not yet started)
- `[~]` — in-progress (task assigned, Worker is working)
- `[x]` — completed
- `[!]` — blocked (Worker reported a blocker, needs attention)

## plan.md Format

```markdown
# Project: {title}

**ID**: {project-id}
**Status**: planning | active | completed
**Room**: {room-id}
**Created**: {ISO date}
**Confirmed**: {ISO date or "pending"}

## Team

- @manager:{domain} — Project Manager
- @{worker1}:{domain} — {role description}
- @{worker2}:{domain} — {role description}

## Task Plan

### Phase 1: {phase name}

- [ ] {task-id} — {task title} (assigned: @{worker}:{domain})
  - Spec: ~/hiclaw-fs/shared/tasks/{task-id}/spec.md
  - Result: ~/hiclaw-fs/shared/tasks/{task-id}/result.md

### Phase 2: {phase name}

- [ ] {task-id} — {task title} (assigned: @{worker}:{domain}, depends on: {task-id})
  - Spec: ~/hiclaw-fs/shared/tasks/{task-id}/spec.md
  - Result: ~/hiclaw-fs/shared/tasks/{task-id}/result.md
  - **On REVISION_NEEDED**: return to {task-id} | reassign to @{worker}

## Change Log

- {ISO datetime}: Project initiated
- {ISO datetime}: Plan confirmed by human
```

**Task status markers:**
- `[ ]` — pending (not yet started)
- `[~]` — in-progress (task assigned, Worker is working)
- `[x]` — completed
- `[!]` — blocked (Worker reported a blocker, needs attention)
- `[→]` — revision in progress (task triggered a revision)

**task-id** follows the same format as regular tasks: `task-YYYYMMDD-HHMMSS`

**On REVISION_NEEDED directive (optional):**

For tasks that may require rework (e.g., reviews, QA, approvals), specify what happens when the task reports `REVISION_NEEDED`:

| Directive | Meaning |
|-----------|---------|
| `return to {task-id}` | Create a revision task assigned to the original task's assignee |
| `reassign to @{worker}` | Create a revision task assigned to a specific worker |

Example:
```markdown
### Phase 2: Review

- [ ] task-002 — Code Review (assigned: @bob, depends on: task-001)
  - **On REVISION_NEEDED**: return to task-001
```

This means: if Bob's review finds issues, Manager will create a revision task for Alice (task-001's assignee).

## result.md Standard Format

All Workers should use this format when writing task results:

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

**Outcome values:**

| Status | When to use |
|--------|-------------|
| `SUCCESS` | Task fully completed, no issues |
| `SUCCESS_WITH_NOTES` | Completed but with suggestions for future improvement |
| `REVISION_NEEDED` | Issues found that require rework of earlier tasks |
| `BLOCKED` | Cannot complete due to missing dependency or external blocker |

---


## Context

- Project plan: ~/hiclaw-fs/shared/projects/<project-id>/plan.md
- <any relevant prior task results or links>

## Notes

<any additional constraints, quality bar, examples>

## Task Directory Convention

All your work for this task must stay in `~/hiclaw-fs/shared/tasks/<task-id>/`:
- Create `plan.md` **before starting** (your step-by-step execution plan)
- Store all intermediate artifacts here (code drafts, notes, tool outputs)
- Write `result.md` when done
- Push everything with: `mc mirror ~/hiclaw-fs/shared/tasks/<task-id>/ hiclaw/hiclaw-storage/shared/tasks/<task-id>/ --overwrite --exclude "spec.md" --exclude "base/"` (spec.md and base/ are Manager-owned, do not overwrite them)
EOF
```

### 2b. Sync to MinIO

```bash
mc cp ~/hiclaw-fs/shared/tasks/${TASK_ID}/meta.json hiclaw/hiclaw-storage/shared/tasks/${TASK_ID}/meta.json
mc cp ~/hiclaw-fs/shared/tasks/${TASK_ID}/spec.md hiclaw/hiclaw-storage/shared/tasks/${TASK_ID}/spec.md
```

### 2c. Update plan.md

Change the task marker from `[ ]` to `[~]` and add the task-id link if not already there. Sync plan.md to MinIO.

### 2d. @mention Worker in Project Room

Send a message in the **project room** @mentioning the Worker:

```
@{worker}:{domain} 你有一个新任务 [{task-id}]：{task title}

{2-3 句摘要：任务目的和关键交付物}

完整规格：hiclaw/hiclaw-storage/shared/tasks/{task-id}/spec.md

请先使用 file-sync 技能同步任务文件，然后阅读任务规格。开始前先在任务目录创建 plan.md 记录执行计划，所有中间产物也请放在该目录下。完成后在此 @mention 我汇报结果。
```

---

## Step 3: Handle Worker Completion Report

When a Worker @mentions you with a task completion in the project room:

### 3a. Parse task outcome

**First, read the task's `result.md` file** and extract the outcome:

```bash
RESULT_FILE="~/hiclaw-fs/shared/tasks/${TASK_ID}/result.md"

# Look for the Outcome section
if grep -q "Status:.*REVISION_NEEDED" "$RESULT_FILE" 2>/dev/null; then
  OUTCOME="REVISION_NEEDED"
elif grep -q "Status:.*BLOCKED" "$RESULT_FILE" 2>/dev/null; then
  OUTCOME="BLOCKED"
elif grep -q "Status:.*SUCCESS_WITH_NOTES" "$RESULT_FILE" 2>/dev/null; then
  OUTCOME="SUCCESS_WITH_NOTES"
else
  OUTCOME="SUCCESS"
fi
```

**Standard outcome values:**

| Outcome | Meaning | Action |
|---------|---------|--------|
| `SUCCESS` | Task completed successfully | Proceed to next task |
| `SUCCESS_WITH_NOTES` | Completed with notes/suggestions | Proceed, but note the suggestions |
| `REVISION_NEEDED` | Work needs revision/fixes | Trigger revision workflow |
| `BLOCKED` | Cannot proceed due to blocker | Handle blocker |

### 3b. If outcome is REVISION_NEEDED - Trigger Revision

When a task reports `REVISION_NEEDED`:

1. **Find the revision target** - Look in plan.md for `On REVISION_NEEDED:` directive:

```bash
# Parse plan.md to find the revision target for this task
# Example line in plan.md:
#   - [x] task-002 — Review (assigned: @bob)
#     **On REVISION_NEEDED**: return to task-001
```

2. **Identify who should do the revision**:
   - If `On REVISION_NEEDED: return to {task-id}` → Find the assignee of that task
   - If `On REVISION_NEEDED: reassign to {worker}` → Use specified worker

3. **Create a revision task**:

```bash
REVISION_TASK_ID="task-$(date +%Y%m%d-%H%M%S)"
ORIGINAL_TASK_ID="<task-reporting-revision>"
TARGET_TASK_ID="<task-to-revise>"
REVISION_AUTHOR="<worker-who-will-revise>"

mkdir -p ~/hiclaw-fs/shared/tasks/${REVISION_TASK_ID}

cat > ~/hiclaw-fs/shared/tasks/${REVISION_TASK_ID}/meta.json << EOF
{
  "task_id": "${REVISION_TASK_ID}",
  "project_id": "<project-id>",
  "task_title": "Revision based on ${ORIGINAL_TASK_ID}",
  "assigned_to": "${REVISION_AUTHOR}",
  "room_id": "<project-room-id>",
  "status": "assigned",
  "depends_on": ["${ORIGINAL_TASK_ID}"],
  "is_revision_for": "${TARGET_TASK_ID}",
  "triggered_by": "${ORIGINAL_TASK_ID}",
  "assigned_at": "$(date -Iseconds)"
}
EOF
```

4. **Create spec.md** referencing what needs revision:

```bash
cat > ~/hiclaw-fs/shared/tasks/${REVISION_TASK_ID}/spec.md << EOF
# Task: Revision Required

**Task ID**: ${REVISION_TASK_ID}
**Project**: <project-title>
**Assigned to**: ${REVISION_AUTHOR}
**Type**: Revision

## Context

Task ${ORIGINAL_TASK_ID} has identified issues that require revision of earlier work.

**Review/Feedback source**: ~/hiclaw-fs/shared/tasks/${ORIGINAL_TASK_ID}/result.md

## What Needs Revision

<Extract the "Notes" or "Issues" section from the result.md of the triggering task>

## Original Task Reference

Original task: ${TARGET_TASK_ID}
Spec: ~/hiclaw-fs/shared/tasks/${TARGET_TASK_ID}/spec.md

## Deliverables

Address all issues identified in the review/feedback, then:
1. Update the deliverables from the original task
2. Write result.md for this revision task
3. @mention Manager when complete
EOF
```

5. **Update plan.md** to add the revision task:

```markdown
### Phase N: {Phase Name}

- [x] task-xxx — Original task
- [x] task-yyy — Review task (reported REVISION_NEEDED)
- [ ] task-zzz — Revision (assigned: @worker, revision for: task-xxx)
```

6. **@mention the worker** in project room:

```
@{worker}:{domain} 任务 {ORIGINAL_TASK_ID} 反馈需要修改。

**任务**: {REVISION_TASK_ID} — 根据反馈进行修改

**反馈来源**: hiclaw/hiclaw-storage/shared/tasks/${ORIGINAL_TASK_ID}/result.md

请先使用 file-sync 技能同步文件，阅读修改要求，完成后 @mention 我汇报。
```

7. **Do NOT proceed to next phase** until revision is complete.

### 3c. If outcome is BLOCKED

Handle as described in Step 4 (Handle Blocked Tasks).

### 3d. If outcome is SUCCESS or SUCCESS_WITH_NOTES

1. Update `shared/tasks/{task-id}/meta.json`: `status → completed`, fill `completed_at`
2. Sync to MinIO
3. Update `plan.md`: change `[~]` to `[x]` for the completed task
4. Add entry to plan.md Change Log
5. If `SUCCESS_WITH_NOTES`, record the notes for reference
6. Proceed to find next tasks (Step 3e)

### 3e. Find next tasks

Read plan.md and find:
- Any `[ ]` tasks whose dependencies are now all `[x]`
- Any `[ ]` tasks assigned to the same Worker (if sequential phases)

For each newly unblocked task, go to Step 2 to assign it.

### 3f. If Worker has another task in plan.md

Assign the next task to the same Worker immediately (Step 2). The Worker is available and context-fresh.

### 3g. If all tasks are complete

**This step is mandatory — always execute it, including in YOLO mode.**

1. Update meta.json: `status → completed`
2. Update plan.md Status to "completed"
3. Sync to MinIO
4. Post completion summary in project room, @mention human admin:

```
@{admin}:{domain} 项目「{title}」已完成！

所有任务已全部交付：
{list of completed tasks with one-line summary}

项目文档：~/hiclaw-fs/shared/projects/{project-id}/plan.md
```

5. Update `memory/YYYY-MM-DD.md` with project outcome

---

## Step 4: Handle Blocked Tasks

When a Worker @mentions you reporting a blocker (`[!]` marker):

1. Update plan.md: change `[~]` to `[!]` for the blocked task
2. Assess if the blocker can be resolved (missing dependency, unclear requirement, needs a different Worker's input)
3. If you can resolve it (e.g., clarify requirements, reassign): do so and re-assign
4. If it needs human input: escalate in DM with human admin

---

## Step 5: Plan Changes

### Minor changes (no human gate required)
- Reordering tasks within a phase
- Adjusting task scope slightly based on Worker feedback
- Adding sub-tasks to clarify deliverables

Document in plan.md Change Log and sync.

### Major changes (require human confirmation)
- Adding or removing Workers from the project
- Changing overall deliverables or project goal
- Reassigning >2 tasks between Workers
- Splitting or merging phases that alter the timeline significantly
- Creating a new Worker role for the project (explain skill gap first; see Step 7)

For major changes:
1. Draft the proposed change in DM with human admin
2. Explain the rationale and impact
3. Wait for human confirmation before implementing
4. After confirmation, update plan.md, notify project room of the change

---

## Step 6: Onboard a New Mid-Project Worker

When a new Worker joins a project after it has started:

### 6a. Add Worker to project room

```bash
curl -X POST "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ID}/invite" \
  -H "Authorization: Bearer ${MANAGER_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"user_id": "@<new-worker>:<matrix_domain>"}'
```

Also add to manager's `groupAllowFrom`:
```bash
jq --arg w "@<new-worker>:<domain>" '.channels.matrix.groupAllowFrom += [$w]' \
  ~/hiclaw-fs/agents/manager/openclaw.json > /tmp/cfg.json && mv /tmp/cfg.json ~/hiclaw-fs/agents/manager/openclaw.json
mc cp ~/hiclaw-fs/agents/manager/openclaw.json hiclaw/hiclaw-storage/agents/manager/openclaw.json
```

### 6b. Send onboarding message in project room

@mention the new Worker in the project room with a full context briefing:

```
@{new-worker}:{domain} 欢迎加入项目「{title}」！

**项目背景**：{2-3 sentences describing what the project is and why}

**当前进展**：
{summary of completed tasks and current status}

**你的角色**：{description of what this Worker will contribute}

**项目计划**（最新版本）：hiclaw/hiclaw-storage/shared/projects/{project-id}/plan.md

请先使用 file-sync 技能同步文件，阅读 plan.md 了解全貌。我稍后会分配你的第一个任务。
```

Then notify the human admin in DM that the new Worker has been onboarded.

---

## Step 7: New Worker Headcount Request

When the project requires a Worker role that doesn't exist yet:

Before requesting human admin to create a new Worker, justify the need:

1. **Explain the skill gap**: what capability is needed that existing Workers don't have
2. **Explain the impact**: what tasks are blocked or at risk without this Worker
3. **Propose the Worker profile**: name, role, skills, MCP access needed

Present this to human admin in DM:

```
项目「{title}」需要一个新的 Worker：

**角色**：{role name}
**原因**：{current workers can't handle X because Y}
**承担任务**：{which tasks will be assigned to this worker}
**建议配置**：
  - 名称：{suggested-worker-name}
  - 技能：{required skills}
  - MCP 访问：{required MCP servers}

是否批准创建？
```

After human approval, use the worker-management skill to create the Worker.

---

## Heartbeat — Project Monitoring

During heartbeat, for each active project:

```bash
for meta in ~/hiclaw-fs/shared/projects/*/meta.json; do
  status=$(jq -r '.status' "$meta")
  [ "$status" != "active" ] && continue
  project_id=$(jq -r '.project_id' "$meta")
  room_id=$(jq -r '.project_room_id' "$meta")
  plan="~/hiclaw-fs/shared/projects/${project_id}/plan.md"
  # Check for [~] tasks (in-progress)
  # For each in-progress task, check if the assigned Worker has sent an @mention recently
  # If no activity in the last heartbeat cycle: @mention the Worker asking for update
done
```

For each stalled Worker, post in the project room:
```
@{worker}:{domain} 你正在执行的任务 {task-id} 有进展吗？有遇到阻塞请告知。
```

