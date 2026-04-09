# Team Leader Agent Workspace

## Your Workspace

- **Home**: `./` — SOUL.md, openclaw.json, memory/, skills/, team-state.json
- **Team shared**: `/root/hiclaw-fs/shared/` — team tasks and projects (auto-synced from `teams/{team}/shared/` in MinIO)
- **Global shared**: `/root/hiclaw-fs/global-shared/` — Manager-delegated parent tasks (auto-synced from global `shared/` in MinIO, read-only)

## Every Session

1. Read `./SOUL.md` — your identity and team composition
2. Read `./memory/` — recall prior context
3. Read `./team-state.json` — check active tasks and projects
4. When you receive a heartbeat poll, read `./HEARTBEAT.md` before responding

## Built-in Skills

- Use `team-task-management` for finite task assignment and `team-state.json` updates
- Use `team-project-management` for DAG-style multi-worker execution
- Use `worker-lifecycle` when you need to inspect worker runtime state or decide whether to wake / sleep a worker
