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

### Local full build (from source)

The image dependency chain is: `openclaw-base` → `manager` / `worker`. CoPaw worker is independent.

By default, `OPENCLAW_BASE_IMAGE` points to the remote registry (`higress-registry.cn-hangzhou.cr.aliyuncs.com/higress/openclaw-base`). When building locally from a modified `openclaw-base`, you **must** override it to the local image name so that manager/worker actually use your local base:

```bash
# Step 1: Build openclaw-base
make build-openclaw-base

# Step 2: Build manager, worker, copaw-worker using the LOCAL base
make build-manager build-worker build-copaw-worker \
    OPENCLAW_BASE_IMAGE=hiclaw/openclaw-base \
    OPENCLAW_BASE_VERSION=latest
```

**Common pitfall**: Running `make build-manager build-worker OPENCLAW_BASE_VERSION=latest` without `OPENCLAW_BASE_IMAGE=hiclaw/openclaw-base` will pull the remote registry's `:latest` tag instead of using the locally-built image. Always set both variables together for local builds.

**Proxy support**: If behind an HTTP proxy, pass proxy build args. This covers APT, PIP, NPM and all other network access — no mirror args needed:
```bash
PROXY_ARGS="--build-arg HTTP_PROXY=http://host.containers.internal:1087 \
    --build-arg HTTPS_PROXY=http://host.containers.internal:1087 \
    --build-arg http_proxy=http://host.containers.internal:1087 \
    --build-arg https_proxy=http://host.containers.internal:1087"

make build-embedded build-manager build-worker build-copaw-worker DOCKER_BUILD_ARGS="${PROXY_ARGS}"
```
Note: use `host.containers.internal` for Podman on macOS, `host.docker.internal` for Docker Desktop.

**China build acceleration (without proxy)**: All Dockerfiles default to official sources. For builds in China without proxy, pass mirror args:
```bash
# APT mirror (for Ubuntu/Debian-based images: openclaw-base, copaw, manager-copaw, embedded)
make build-embedded DOCKER_BUILD_ARGS="--build-arg APT_MIRROR=mirrors.aliyun.com"

# PIP mirror (for Python-based images: copaw, manager-copaw)
make build-copaw-worker DOCKER_BUILD_ARGS="--build-arg APT_MIRROR=mirrors.aliyun.com --build-arg PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/"

# NPM mirror (for Node.js-based images: openclaw-base)
make build-openclaw-base DOCKER_BUILD_ARGS="--build-arg APT_MIRROR=mirrors.aliyun.com --build-arg NPM_REGISTRY=https://registry.npmmirror.com/"
```

### To modify the Manager container
- [manager/Dockerfile](manager/Dockerfile) -- multi-stage build definition
- [manager/supervisord.conf](manager/supervisord.conf) -- process orchestration
- [manager/scripts/init/](manager/scripts/init/) -- container startup scripts (supervisord)
- [manager/scripts/lib/](manager/scripts/lib/) -- shared libraries (base.sh, container-api.sh)
- [manager/configs/](manager/configs/) -- init-time configuration templates

### To modify the Worker container (OpenClaw)
- [worker/Dockerfile](worker/Dockerfile) -- build definition (Node.js 22 from build stage)
- [worker/scripts/worker-entrypoint.sh](worker/scripts/worker-entrypoint.sh) -- startup logic

### To modify the Worker container (CoPaw)
- [copaw/Dockerfile](copaw/Dockerfile) -- build definition (Python 3.11)
- [copaw/scripts/copaw-worker-entrypoint.sh](copaw/scripts/copaw-worker-entrypoint.sh) -- startup logic
- [copaw/src/copaw_worker/](copaw/src/copaw_worker/) -- CoPaw worker Python package

### To manage Worker containers via socket
- [manager/scripts/lib/container-api.sh](manager/scripts/lib/container-api.sh) -- Docker/Podman REST API helpers for direct Worker creation

### To modify Agent behavior
- [manager/agent/SOUL.md](manager/agent/SOUL.md) -- Manager personality and rules
- [manager/agent/HEARTBEAT.md](manager/agent/HEARTBEAT.md) -- periodic check routine
- [manager/agent/skills/](manager/agent/skills/) -- Manager's skills (9 skill directories, each with SKILL.md and optional scripts/references/)
- [manager/agent/worker-skills/](manager/agent/worker-skills/) -- Skill definitions pushed to Workers on creation
- [manager/agent/worker-skills/github-operations/SKILL.md](manager/agent/worker-skills/github-operations/SKILL.md) -- Worker GitHub skill (on-demand, pushed by Manager)

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
| Agent Framework | CoPaw (Python) | Alternative Python-based agent runtime for Workers |
| MCP CLI | mcporter | Worker calls MCP Server tools via CLI |

## Changelog Policy

Any change that affects the content of a built image — i.e. modifications under `manager/`, `worker/`, `copaw/`, or `openclaw-base/` — **must** be recorded in [`changelog/current.md`](changelog/current.md) before committing.

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

## Agent-Facing Content: Writing Convention

Files under `manager/agent/` are **read by the Agent at runtime**, not by human developers. All content in these paths must be written from the Agent's own perspective using second-person voice ("you"):

- **AGENTS.md, SOUL.md, HEARTBEAT.md** — address the Agent directly: "You are the Manager...", "Your responsibilities include..."
- **SKILL.md** — instruct the Agent as the reader: "Use this script to...", "You can call...", "Run `mcporter --config ~/mcporter-servers.json list` to see..."
- **TOOLS.md** — describe tools available to the Agent: "You have access to...", "Use `mc cp` to push..."
- **Script `log` output and comments** — write from the system's perspective, but keep the Agent as the implied operator. Avoid third-person references like "the Manager does X" — instead say "Step 4: Updating your mcporter-servers.json..."

**Do NOT** use third-person descriptions like "Manager can call..." or "This skill provides..." in agent-facing files. The Agent is the reader — talk to it directly.

This convention applies to all files that end up in the Agent's workspace or are loaded as skills/prompts at runtime:
- `manager/agent/**` (Manager Agent config, skills, tools)
- `manager/agent/worker-agent/**` (OpenClaw Worker Agent config, builtin skills)
- `manager/agent/copaw-worker-agent/**` (CoPaw Worker Agent config, builtin skills)
- `manager/agent/worker-skills/**` (on-demand skill definitions pushed to Workers)

## Environment Variables

See [manager/scripts/init/start-manager-agent.sh](manager/scripts/init/start-manager-agent.sh) for the full list of `HICLAW_*` environment variables used by the Manager container.

## Verified Technical Details

All technical assumptions have been verified in POC. See [design/poc-tech-verification.md](design/poc-tech-verification.md) for detailed verification results. Key findings that affect implementation:

- Tuwunel uses `CONDUWUIT_` env prefix (not `TUWUNEL_`)
- Higress Console uses Session Cookie auth (not Basic Auth)
- MCP Server created via `PUT` (not `POST`)
- Auth plugin takes ~40s to activate after first configuration
- OpenClaw Skills auto-load from `workspace/skills/<name>/SKILL.md`
