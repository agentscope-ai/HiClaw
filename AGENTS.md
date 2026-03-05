# HiClaw Codebase Navigation Guide

This file helps AI Agents (and human developers) quickly understand the project structure and find relevant code.

## What is HiClaw

HiClaw is an open-source Agent Teams system that uses IM (Matrix protocol) for multi-Agent collaboration with human-in-the-loop oversight. It consists of a Manager Agent (coordinator) and Worker Agents (task executors), connected via an AI Gateway (Higress), Matrix Homeserver (Tuwunel), and HTTP File System (MinIO).

## Project Structure

```
hiclaw/
├── manager/          # Manager Agent container (all-in-one: Higress + Tuwunel + MinIO + Element Web + OpenClaw)
├── worker/           # Worker Agent container (lightweight: OpenClaw + mc + mcporter)
├── install/          # One-click installation scripts
├── scripts/          # Utility scripts (replay-task.sh for sending tasks to Manager via Matrix)
├── hack/             # Maintenance scripts (mirror-images.sh for syncing base images to registry)
├── tests/            # Automated integration test suite (10 test cases)
├── .github/workflows/# CI/CD: build images, run tests, release
├── docs/             # User-facing documentation
├── design/           # Internal design documents and API specs
└── logs/             # Replay conversation logs (gitignored)
```

## Key Entry Points

### To understand the architecture
- Read [docs/architecture.md](docs/architecture.md) for system overview and component diagram
- Read [design/design.md](design/design.md) for full product design (Chinese)
- Read [design/poc-design.md](design/poc-design.md) for detailed implementation specs

### To build and run
- [Makefile](Makefile) -- unified build/test/push/install/replay interface (`make help` for all targets)
- [docs/quickstart.md](docs/quickstart.md) -- end-to-end guide from zero to working team
- [install/hiclaw-install.sh](install/hiclaw-install.sh) -- the installation script
- [scripts/replay-task.sh](scripts/replay-task.sh) -- send tasks to Manager via Matrix CLI

### To modify the Manager container
- [manager/Dockerfile](manager/Dockerfile) -- multi-stage build definition
- [manager/supervisord.conf](manager/supervisord.conf) -- process orchestration
- [manager/scripts/init/](manager/scripts/init/) -- container startup scripts (supervisord)
- [manager/scripts/lib/](manager/scripts/lib/) -- shared libraries (base.sh, container-api.sh)
- [manager/configs/](manager/configs/) -- init-time configuration templates

### To modify the Worker container
- [worker/Dockerfile](worker/Dockerfile) -- build definition (Node.js 22 from build stage)
- [worker/scripts/worker-entrypoint.sh](worker/scripts/worker-entrypoint.sh) -- startup logic

### To manage Worker containers via socket
- [manager/scripts/lib/container-api.sh](manager/scripts/lib/container-api.sh) -- Docker/Podman REST API helpers for direct Worker creation

### To modify Agent behavior
- [manager/agent/SOUL.md](manager/agent/SOUL.md) -- Manager personality and rules
- [manager/agent/HEARTBEAT.md](manager/agent/HEARTBEAT.md) -- periodic check routine
- [manager/agent/skills/](manager/agent/skills/) -- Manager's skills (9 skill directories, each with SKILL.md and optional scripts/references/)
- [manager/agent/worker-skills/](manager/agent/worker-skills/) -- Skill definitions pushed to Workers on creation
- [worker/agent/skills/github-operations/SKILL.md](worker/agent/skills/github-operations/SKILL.md) -- Worker GitHub skill

### To modify CI/CD
- [.github/workflows/](/.github/workflows/) -- GitHub Actions workflows
- [tests/](tests/) -- integration test suite

### To modify Higress routing and initialization
- [manager/scripts/init/setup-higress.sh](manager/scripts/init/setup-higress.sh) -- route, consumer, MCP server setup
- [design/higress-console-api.yaml](design/higress-console-api.yaml) -- Higress Console API spec (OpenAPI 3.0)

## Technology Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| AI Gateway | Higress (all-in-one) | LLM proxy, MCP Server hosting, consumer auth, route management |
| Matrix Server | Tuwunel (conduwuit fork) | IM communication between Agents and Human |
| Matrix Client | Element Web | Browser-based IM interface |
| File System | MinIO + mc mirror | Centralized HTTP file storage with local sync |
| Agent Framework | OpenClaw (fork) | Agent runtime with Matrix plugin, skills, heartbeat |
| MCP CLI | mcporter | Worker calls MCP Server tools via CLI |

## Changelog Policy

Any change that affects the content of a built image — i.e. modifications under `manager/`, `worker/`, or `openclaw-base/` — **must** be recorded in [`changelog/current.md`](changelog/current.md) before committing.

Format: one bullet per logical change, with a linked commit hash, e.g.:
```
- feat(manager): add task-management skill extracted from AGENTS.md ([a1b2c3d](https://github.com/higress-group/hiclaw/commit/a1b2c3d...))
- fix(manager): fix upgrade-builtins idempotency (duplicate marker insertion) ([e4f5g6h](https://github.com/higress-group/hiclaw/commit/e4f5g6h...))
```

On release, the workflow automatically renames `current.md` → `vX.Y.Z.md` and creates a fresh `current.md`.

## Key Design Patterns

1. **All communication in Matrix Rooms**: Human + Manager + Worker are all in the same Room. Human sees everything, can intervene anytime.
2. **Centralized file system**: All Agent configs and state stored in MinIO. Workers are stateless -- destroy and recreate freely.
3. **Unified credential management**: Worker uses one Consumer key-auth token for both LLM and MCP Server access. Manager controls permissions.
4. **Skills as documentation**: Each SKILL.md is a self-contained reference that tells the Agent how to use an API or tool.

## Environment Variables

See [manager/scripts/init/start-manager-agent.sh](manager/scripts/init/start-manager-agent.sh) for the full list of `HICLAW_*` environment variables used by the Manager container.

## Verified Technical Details

All technical assumptions have been verified in POC. See [design/poc-tech-verification.md](design/poc-tech-verification.md) for detailed verification results. Key findings that affect implementation:

- Tuwunel uses `CONDUWUIT_` env prefix (not `TUWUNEL_`)
- Higress Console uses Session Cookie auth (not Basic Auth)
- MCP Server created via `PUT` (not `POST`)
- Auth plugin takes ~40s to activate after first configuration
- OpenClaw Skills auto-load from `workspace/skills/<name>/SKILL.md`
