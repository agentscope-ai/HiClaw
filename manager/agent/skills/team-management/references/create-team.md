# Create Team

## CLI Usage

```bash
hiclaw create team \
  --name <TEAM_NAME> \
  --leader-name <LEADER_NAME> \
  --leader-model <MODEL_ID> \
  --workers <w1>,<w2>,<w3> \
  [--description "Team description"] \
  [--leader-heartbeat-every 30m] \
  [--worker-idle-timeout 12h]
```

Notes:
- `--name` and `--leader-name` are required
- `--workers` is a comma-separated list of worker names
- `--leader-model` defaults to the install-time configured model (`$HICLAW_DEFAULT_MODEL` propagated by the controller); falls back to `qwen3.5-plus` only when that is unset
- Team Admin defaults to Global Admin
- Controller forces `runtime: copaw` for all team members

## What the Controller Does

After `hiclaw create team`, the controller's Team reconciler handles:

1. Creates Matrix rooms: Team Room (Leader + Team Admin + all workers) and Leader DM (Team Admin ↔ Leader)
2. Creates the Team Leader Worker CR with team-leader-agent skills
3. Creates each team worker Worker CR with copaw-worker-agent skills
4. Injects coordination context into Leader's AGENTS.md (Team Room ID, Leader DM Room ID, worker list)
5. Sets up shared team storage in MinIO
6. Updates legacy teams registry

> The legacy `scripts/create-team.sh` is deprecated. Use `hiclaw create team` instead.

## After Creation

1. Verify team created: `hiclaw get team <TEAM_NAME>`
2. @mention the Leader in the Leader Room to assign the task
3. The Leader will handle coordination with team workers from there
