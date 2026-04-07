---
name: team-project-management
description: Use when a task requires multiple workers to collaborate, when tasks have dependencies (parallel/serial), or when you need DAG-based task orchestration within your team.
---

# Team Project Management

Manage multi-worker collaborative projects with DAG-based task orchestration.

## Key Scripts

```bash
# Create a new team project (use --source, --parent-task-id, --requester as needed)
bash ./skills/team-project-management/scripts/create-team-project.sh \
  --id tp-YYYYMMDD-HHMMSS --title "Title" --workers alice,bob \
  --source manager --parent-task-id task-xxx

# Validate DAG (check for cycles)
bash ./skills/team-project-management/scripts/resolve-dag.sh \
  --plan /path/to/plan.md --action validate

# Get ready-to-assign tasks (all dependencies satisfied)
bash ./skills/team-project-management/scripts/resolve-dag.sh \
  --plan /path/to/plan.md --action ready

# Get full DAG status
bash ./skills/team-project-management/scripts/resolve-dag.sh \
  --plan /path/to/plan.md --action status
```

## Gotchas

- **plan.md is the single source of truth** — always sync to MinIO after changes
- **Always validate before activating** — `resolve-dag.sh --action validate` catches cycles
- **Always resolve after completion** — `resolve-dag.sh --action ready` finds newly unblocked tasks
- **Route completion by source** — Manager tasks → result.md in `shared/tasks/`, Team Admin tasks → stay in `teams/{team}/`

## References

Read the relevant doc **before** executing. Do not load all of them.

| Situation | Read |
|---|---|
| Create a new team project | `references/create-project.md` |
| Need plan.md / DAG format | `references/plan-format.md` |
| Execute DAG, handle completion, find next tasks | `references/dag-execution.md` |
| Task assignment, completion, revision flow | `references/task-lifecycle.md` |
