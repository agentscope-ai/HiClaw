# HiClaw

[English](./README.md) | [中文](./README.zh-CN.md)

**Open-source Agent Teams system with IM-based multi-Agent collaboration and human-in-the-loop oversight.**

HiClaw lets you deploy a team of AI Agents that communicate via instant messaging (Matrix protocol), coordinate tasks through a centralized file system, and are fully observable and controllable by human administrators.

## Key Features

- **Agent Teams**: Manager Agent coordinates multiple Worker Agents to complete complex tasks
- **Human in the Loop**: All Agent communication happens in Matrix Rooms where humans can observe and intervene at any time
- **Multi-Channel Admin**: Admin can contact the Manager from Discord, Feishu, Telegram, and other OpenClaw-supported channels; the Manager recognizes them as the admin and can route daily notifications to their preferred (primary) channel
- **Coding CLI Delegation**: When a coding CLI tool (Claude Code, Gemini CLI) is available, Workers can delegate coding tasks to it — the Manager runs the CLI in the task workspace and streams results back, enabling richer code generation beyond the standard LLM call
- **AI Gateway**: Unified LLM and MCP Server access through Higress, with per-Worker credential management
- **Stateless Workers**: Workers load all config from centralized storage -- destroy and recreate freely
- **MCP Integration**: External tools (GitHub, etc.) accessed via MCP Servers with centralized credential management
- **Open Source**: Built on Higress, Tuwunel, MinIO, OpenClaw, and Element Web

## Quick Start

See **[docs/quickstart.md](docs/quickstart.md)** for a step-by-step guide from zero to a working Agent team.

### Prerequisites

- **Docker Desktop** (Windows/macOS) or **Docker Engine** (Linux) installed and running
- **PowerShell 7+** (Windows only)
- An LLM API key (e.g., Qwen, OpenAI)
- (Optional) A GitHub Personal Access Token for GitHub collaboration features

### Installation

#### macOS / Linux

```bash
# Quick install (interactive)
bash <(curl -fsSL https://higress.ai/hiclaw/install.sh)

# Or clone and install
git clone https://github.com/higress-group/hiclaw.git && cd hiclaw
HICLAW_LLM_API_KEY="sk-xxx" make install
```

#### Windows (PowerShell 7+)

```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://higress.ai/hiclaw/install.ps1'))
```

The installation script will:
1. **Detect your timezone** (supports Linux and macOS) for optimal registry selection
2. **Prompt for configuration** (LLM provider, API key, ports, etc.) - all can be pre-set via environment variables
3. **Wait for Manager to be ready** before exiting
4. **Send a welcome message** to the Manager, which will greet you in your likely local language

#### Installation Options

**macOS / Linux:**

```bash
# Non-interactive mode (use all defaults)
HICLAW_NON_INTERACTIVE=1 HICLAW_LLM_API_KEY="sk-xxx" make install

# Custom ports
HICLAW_PORT_GATEWAY=8080 HICLAW_PORT_CONSOLE=8001 HICLAW_LLM_API_KEY="sk-xxx" make install

# External data directory
HICLAW_DATA_DIR=~/hiclaw-data HICLAW_LLM_API_KEY="sk-xxx" make install

# Pre-configure all settings
HICLAW_LLM_PROVIDER=qwen \
HICLAW_DEFAULT_MODEL=qwen3.5-plus \
HICLAW_LLM_API_KEY="sk-xxx" \
HICLAW_ADMIN_USER=admin \
HICLAW_ADMIN_PASSWORD=yourpassword \
HICLAW_TIMEZONE=Asia/Shanghai \
make install
```

**Windows (PowerShell):**

```powershell
# Non-interactive mode (use all defaults)
$env:HICLAW_NON_INTERACTIVE = "1"
$env:HICLAW_LLM_API_KEY = "sk-xxx"
.\hiclaw-install.ps1

# Pre-configure all settings
$env:HICLAW_LLM_PROVIDER = "qwen"
$env:HICLAW_DEFAULT_MODEL = "qwen3.5-plus"
$env:HICLAW_LLM_API_KEY = "sk-xxx"
$env:HICLAW_ADMIN_USER = "admin"
$env:HICLAW_ADMIN_PASSWORD = "yourpassword"
.\hiclaw-install.ps1
```

#### Upgrade or Reinstall

When running the install script on an existing installation, you'll be prompted:

```
Choose an action:
  1) In-place upgrade (keep data, workspace, env file)
  2) Clean reinstall (remove all data, start fresh)
  3) Cancel
```

- **In-place upgrade**: Keeps all data, just recreates the Manager container. Optionally rebuild Worker containers if the image has changed.
- **Clean reinstall**: Removes everything (Docker volume, workspace directory, env file, Worker containers). Requires manual confirmation of the workspace path.

### Post-Installation

After installation completes:

1. Open Element Web: `http://matrix-client-local.hiclaw.io:<port>` (default port: 18080)
2. Login with your admin credentials
3. The Manager will greet you and introduce its capabilities

Or send tasks via CLI:

```bash
make replay TASK="Create a Worker named alice for frontend development. Create it directly."
```

## Architecture

```
┌─────────────────────────────────────────────┐
│         hiclaw-manager-agent                │
│  Higress │ Tuwunel │ MinIO │ Element Web    │
│  Manager Agent (OpenClaw)                   │
└──────────────────┬──────────────────────────┘
                   │ Matrix + HTTP Files
┌──────────────────┴──────┐  ┌────────────────┐
│  hiclaw-worker-agent    │  │  hiclaw-worker │
│  Worker Alice (OpenClaw)│  │  Worker Bob    │
└─────────────────────────┘  └────────────────┘
```

## Multi-Agent Architecture: HiClaw vs OpenClaw Native

HiClaw is built on [OpenClaw](https://github.com/nicepkg/openclaw) and extends it from a single-process multi-agent framework into a fully managed Agent Teams platform. The Manager Agent leverages the Higress AI Gateway to automate the entire multi-agent lifecycle -- from Worker creation and credential provisioning to task dispatch, progress monitoring, and skill evolution -- all through natural language conversation.

### 1. Deployment & Topology

| | OpenClaw Native | HiClaw |
|---|---|---|
| **Deployment** | Single process, all agents in one Gateway | Distributed containers, one per agent, cross-machine |
| **Topology** | Flat peers, channel-based routing | Hierarchical Manager + Workers |
| **Scaling & isolation** | Vertical; shared process, one crash affects all | Horizontal; container-level fault isolation |

### 2. Communication & Human Visibility

| | OpenClaw Native | HiClaw |
|---|---|---|
| **Channel** | Internal message bus | Matrix Rooms (IM protocol) |
| **Human visibility** | Optional | **Built-in** -- human is in every Room |
| **Agent-to-agent** | Opaque internal routing | All exchanges visible, searchable, interruptible |

Every Room contains Human + Manager + Worker. The human can intervene at any time -- guiding a Worker to improve task execution (feeding back into skill optimization), or coaching the Manager on better Worker management strategies (refining its management skills).

### 3. LLM & MCP Credential Management

| | OpenClaw Native | HiClaw |
|---|---|---|
| **LLM access** | Each agent holds its own API key | Unified AI Gateway with per-agent consumer tokens |
| **Tool credentials** | Each agent holds real credentials | Centralized at gateway -- agents never see real credentials |
| **Permission control** | Per-agent config | Manager grants/revokes MCP Server access per Worker |
| **Credential update** | Manual edit + restart | Manager updates config in MinIO, Worker hot-reloads automatically |

Workers only hold their own consumer tokens. Even a compromised Worker cannot access upstream API credentials.

### 4. Lifecycle & Skill Evolution Automation

| | OpenClaw Native | HiClaw |
|---|---|---|
| **Agent creation** | Manual config + restart | Conversational: _"Create a Worker named alice for frontend dev"_ |
| **Monitoring** | No cross-agent monitoring | Manager heartbeat in Rooms (human-visible) |
| **Config updates** | Edit files + restart | Hot-reload, seconds to take effect |
| **Self-improvement** | None | Manager reviews performance, evolves team skills |

The Manager handles the full Worker lifecycle autonomously: account registration, SOUL.md generation, credential provisioning, skill assignment, task dispatch, and heartbeat monitoring. Two built-in extension mechanisms drive continuous improvement:

- **Worker Experience Management**: per-Worker performance profiles with skill-level scoring, used for intelligent task assignment.
- **Skill Evolution Management**: pattern recognition across tasks, new skill drafting, human review, and simulated task validation.

## Documentation

| Document | Description |
|----------|-------------|
| [docs/quickstart.md](docs/quickstart.md) | End-to-end quickstart guide with verification checkpoints |
| [docs/architecture.md](docs/architecture.md) | System architecture and component overview |
| [docs/manager-guide.md](docs/manager-guide.md) | Manager setup and configuration |
| [docs/worker-guide.md](docs/worker-guide.md) | Worker deployment and troubleshooting |
| [docs/development.md](docs/development.md) | Contributing guide and local development |

## Build & Test

```bash
# Build all images
make build

# Build + run all integration tests (10 test cases, YOLO mode auto-enabled)
make test

# Run specific tests only
make test TEST_FILTER="01 02 03"

# Run tests without rebuilding images
make test SKIP_BUILD=1

# Quick smoke test (test-01 only)
make test-quick
```

## Install / Uninstall / Replay

```bash
# Install Manager locally (builds images + interactive setup)
HICLAW_LLM_API_KEY="sk-xxx" make install

# Install without rebuilding images
HICLAW_LLM_API_KEY="sk-xxx" SKIP_BUILD=1 make install

# Send a task to Manager via CLI
make replay TASK="Create a Worker named alice for frontend development"

# View latest replay conversation log
make replay-log

# Run tests against installed Manager (no rebuild, no new container)
make test-installed

# Uninstall everything (Manager + Workers + volume + env file)
make uninstall
```

## Push & Release

```bash
# Push multi-arch images (amd64 + arm64) to registry
make push VERSION=0.1.0 REGISTRY=ghcr.io REPO=higress-group/hiclaw

# Clean up containers and images
make clean

# Show all targets
make help
```

## Project Structure

```
hiclaw/
├── manager/           # Manager Agent container (all-in-one: Higress + Tuwunel + MinIO + Element Web + OpenClaw)
├── worker/            # Worker Agent container (lightweight: OpenClaw + mc + mcporter)
├── install/           # One-click installation scripts
├── scripts/           # Utility scripts (replay-task.sh)
├── hack/              # Maintenance scripts (mirror-images.sh)
├── tests/             # Automated integration tests (10 test cases)
├── .github/workflows/ # CI/CD pipelines
├── docs/              # User documentation
└── design/            # Internal design documents
```

See [AGENTS.md](AGENTS.md) for a detailed codebase navigation guide.

## License

Apache License 2.0
