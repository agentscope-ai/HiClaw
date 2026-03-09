<h1 align="center">
    <img src="https://img.alicdn.com/imgextra/i3/O1CN01JLLvVU21EWyG90gbi_!!6000000006953-2-tps-2539-575.png" alt="HiClaw"  width="290" height="72.5">
  <br>
</h1>

[English](./README.md) | [中文](./README.zh-CN.md)

<p align="center">
  <a href="https://deepwiki.com/higress-group/hiclaw"><img src="https://img.shields.io/badge/DeepWiki-Ask_AI-navy.svg?logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACwAAAAyCAYAAAAnWDnqAAAAAXNSR0IArs4c6QAAA05JREFUaEPtmUtyEzEQhtWTQyQLHNak2AB7ZnyXZMEjXMGeK/AIi+QuHrMnbChYY7MIh8g01fJoopFb0uhhEqqcbWTp06/uv1saEDv4O3n3dV60RfP947Mm9/SQc0ICFQgzfc4CYZoTPAswgSJCCUJUnAAoRHOAUOcATwbmVLWdGoH//PB8mnKqScAhsD0kYP3j/Yt5LPQe2KvcXmGvRHcDnpxfL2zOYJ1mFwrryWTz0advv1Ut4CJgf5uhDuDj5eUcAUoahrdY/56ebRWeraTjMt/00Sh3UDtjgHtQNHwcRGOC98BJEAEymycmYcWwOprTgcB6VZ5JK5TAJ+fXGLBm3FDAmn6oPPjR4rKCAoJCal2eAiQp2x0vxTPB3ALO2CRkwmDy5WohzBDwSEFKRwPbknEggCPB/imwrycgxX2NzoMCHhPkDwqYMr9tRcP5qNrMZHkVnOjRMWwLCcr8ohBVb1OMjxLwGCvjTikrsBOiA6fNyCrm8V1rP93iVPpwaE+gO0SsWmPiXB+jikdf6SizrT5qKasx5j8ABbHpFTx+vFXp9EnYQmLx02h1QTTrl6eDqxLnGjporxl3NL3agEvXdT0WmEost648sQOYAeJS9Q7bfUVoMGnjo4AZdUMQku50McDcMWcBPvr0SzbTAFDfvJqwLzgxwATnCgnp4wDl6Aa+Ax283gghmj+vj7feE2KBBRMW3FzOpLOADl0Isb5587h/U4gGvkt5v60Z1VLG8BhYjbzRwyQZemwAd6cCR5/XFWLYZRIMpX39AR0tjaGGiGzLVyhse5C9RKC6ai42ppWPKiBagOvaYk8lO7DajerabOZP46Lby5wKjw1HCRx7p9sVMOWGzb/vA1hwiWc6jm3MvQDTogQkiqIhJV0nBQBTU+3okKCFDy9WwferkHjtxib7t3xIUQtHxnIwtx4mpg26/HfwVNVDb4oI9RHmx5WGelRVlrtiw43zboCLaxv46AZeB3IlTkwouebTr1y2NjSpHz68WNFjHvupy3q8TFn3Hos2IAk4Ju5dCo8B3wP7VPr/FGaKiG+T+v+TQqIrOqMTL1VdWV1DdmcbO8KXBz6esmYWYKPwDL5b5FA1a0hwapHiom0r/cKaoqr+27/XcrS5UwSMbQAAAABJRU5ErkJggg==" alt="DeepWiki"></a>
  <a href="https://discord.gg/n6mV8xEYUF"><img src="https://img.shields.io/badge/Discord-Join_Us-blueviolet.svg?logo=discord" alt="Discord"></a>
  <a href="https://qr.dingtalk.com/action/joingroup?code=v1,k1,0etR5l8fxeb/6/mzE5hRE1uy4tkiwxvPV9+TdBv7sEM=&_dt_no_comment=1&origin=11"><img src="https://img.shields.io/badge/DingTalk-Join_Us-orange.svg" alt="DingTalk"></a>
</p>

**Deploy a team of AI Agents in 5 minutes. Manager coordinates Workers, all visible in your IM.**

HiClaw is an open-source Agent Teams system built on [OpenClaw](https://github.com/nicepkg/openclaw). A Manager Agent acts as your AI chief of staff — it creates Workers, assigns tasks, monitors progress, and reports back. You stay in control, making decisions instead of babysitting agents.

```
You → Manager → Worker Alice (frontend)
             → Worker Bob   (backend)
             → Worker ...
```

All communication happens in Matrix Rooms. You see everything, and can intervene anytime — just like messaging a team in a group chat.

## News

- **2026-03-04**: We officially open source HiClaw，an Agent Teams System. Read more on our [blog](https://github.com/higress-group/hiclaw/blob/main/blog/hiclaw-announcement.md).


## Why HiClaw

**Security by design**: Workers never hold real API keys or GitHub PATs. They only carry a consumer token (like a badge). Even a compromised Worker can't leak your credentials.

**Truly open IM**: Built-in Matrix server means no Slack/Feishu bot approval process. Open Element Web in your browser, or use any Matrix client (Element, FluffyChat) on mobile — iOS, Android, Web.

**One command to start**: A single `curl | bash` sets everything up — Higress AI Gateway, Matrix server, file storage, web client, and the Manager Agent itself.

**Skills ecosystem**: Workers can pull from [skills.sh](https://skills.sh) (80,000+ community skills) on demand. Safe to use because Workers can't access real credentials anyway.

## Quick Start

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

That's it. The script asks for your LLM API key, then sets everything up. When it's done:

```
=== HiClaw Manager Started! ===
  Open: http://127.0.0.1:18088
  Login: admin / [generated password]
  Tell the Manager: "Create a Worker named alice for frontend dev"
```

**Windows (PowerShell 7+):**

```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://higress.ai/hiclaw/install.ps1'))
```

**Prerequisites**: Docker Desktop (Windows/macOS) or Docker Engine (Linux). That's all.

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) (Windows / macOS)
- [Docker Engine](https://docs.docker.com/engine/install/) (Linux) or [Podman Desktop](https://podman-desktop.io/) (alternative)

**Resource requirements**: Minimum 2 CPU cores and 4 GB RAM. If you want to deploy multiple Workers for a more powerful Agent Teams experience, **4 CPU cores and 8 GB RAM are recommended** — OpenClaw's memory usage is relatively high. In Docker Desktop, go to Settings → Resources to adjust.

### Upgrade

Run the same install script in-place to upgrade. Your data and config are preserved. Upgrades to the latest version by default:

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

To upgrade to a specific version:

```bash
HICLAW_VERSION=0.2.0 bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

### After install

![Installation complete](https://img.alicdn.com/imgextra/i2/O1CN01uXyp0Q1Z0y039PC6F_!!6000000003133-2-tps-832-300.png)

1. Open `http://127.0.0.1:18088` in your browser
2. Login with the credentials shown during install
3. Tell the Manager to create a Worker and assign it a task

For mobile: download Element or FluffyChat, connect to your Matrix server address, and manage your agents from your phone.

## How It Works

### Manager as your AI chief of staff

The Manager handles the full Worker lifecycle through natural language:

```
You: Create a Worker named alice for frontend development

Manager: Done. Worker alice is ready.
         Room: Worker: Alice
         Tell alice what to build.

You: @alice implement a login page with React

Alice: On it... [a few minutes later]
       Done. PR submitted: https://github.com/xxx/pull/1
```

<p align="center">
  <img src="https://img.alicdn.com/imgextra/i4/O1CN01wHWaJQ29KV3j5vryD_!!6000000008049-0-tps-589-1280.jpg" width="240" />
  &nbsp;&nbsp;&nbsp;&nbsp;
  <img src="https://img.alicdn.com/imgextra/i2/O1CN01q9L67J245mFT0fPXH_!!6000000007340-0-tps-589-1280.jpg" width="240" />
</p>
<p align="center">
  <sub>① Manager creates a Worker and assigns tasks</sub>
  &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
  <sub>② You can also direct Workers directly in the room</sub>
</p>

The Manager also runs periodic heartbeats — if a Worker gets stuck, it alerts you automatically.

### Security model

```
Worker (consumer token only)
    → Higress AI Gateway (holds real API keys, GitHub PAT)
        → LLM API / GitHub API / MCP Servers
```

Workers only see their consumer token. The gateway handles all real credentials. Manager knows what Workers are doing, but never touches the actual keys either.

### Human in the loop

Every Matrix Room has you, the Manager, and the relevant Workers. You can jump in at any point:

```
You: @bob wait, change the password rule to minimum 8 chars
Bob: Got it, updated.
Alice: Frontend validation updated too.
```

No black boxes. No hidden agent-to-agent calls.

## HiClaw vs OpenClaw Native

| | OpenClaw Native | HiClaw |
|---|---|---|
| Deployment | Single process | Distributed containers |
| Agent creation | Manual config + restart | Conversational |
| Credentials | Each agent holds real keys | Workers only hold consumer tokens |
| Human visibility | Optional | Built-in (Matrix Rooms) |
| Mobile access | Depends on channel setup | Any Matrix client, zero config |
| Monitoring | None | Manager heartbeat, visible in Room |

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

| Component | Role |
|-----------|------|
| Higress AI Gateway | LLM proxy, MCP Server hosting, credential management |
| Tuwunel (Matrix) | IM server for all Agent + Human communication |
| Element Web | Browser client, zero setup |
| MinIO | Centralized file storage, Workers are stateless |
| OpenClaw | Agent runtime with Matrix plugin and skills |

## Troubleshooting

If the Manager container fails to start, check the agent log for details:

```bash
docker exec -it hiclaw-manager cat /var/log/hiclaw/manager-agent.log
```

See [docs/zh-cn/faq.md](docs/zh-cn/faq.md) for common issues (startup timeout, LAN access, etc.).

Feel free to [open an issue](https://github.com/higress-group/hiclaw/issues) or ask in [Discord](https://discord.gg/n6mV8xEYUF) / DingTalk group.

## Roadmap

### Lightweight Worker Runtimes

Currently, Workers run on OpenClaw which has relatively high memory usage. We plan to support alternative lightweight runtimes:

- **CoPaw** — Lightweight agent runtime by AgentScope, supports local models (llama.cpp/MLX/Ollama) and multi-channel, lower memory footprint than OpenClaw
- **ZeroClaw** — Rust-based ultra-lightweight runtime, 3.4MB binary, <10ms cold start, designed for edge and resource-constrained environments
- **NanoClaw** — Minimal OpenClaw alternative, <4000 LOC, container-based isolation, built on Anthropic Agents SDK

Goal: Reduce per-Worker memory footprint from ~500MB to <100MB, enabling more Workers on the same hardware.

### Team Management Center

A built-in dashboard for observing and controlling your Agent Teams:

- **Real-time observation**: Watch each agent's thinking process, tool calls, and decision-making
- **Active interruption**: Pause or stop any agent mid-task when you spot issues
- **Task timeline**: Visual history of who did what and when
- **Resource monitoring**: CPU/memory usage per Worker

Goal: Make Agent Teams as observable and controllable as human teams — no black boxes.

### Universal MCP Service Support

Currently, Workers access GitHub via Higress MCP Gateway + mcporter, using only a Higress-issued token — real GitHub PATs never leave the gateway. This secure pattern works for any MCP server:

- **Pre-built MCP connectors**: GitHub, Slack, Notion, Linear, and more
- **Custom MCP integration**: Bring your own MCP server, let Higress handle auth
- **Per-Worker access control**: Manager grants/revokes MCP access per Worker

Goal: Any tool that speaks MCP can be safely exposed to Workers without credential leakage.

---

## Documentation

| | |
|---|---|
| [docs/quickstart.md](docs/quickstart.md) | Step-by-step guide with verification checkpoints |
| [docs/architecture.md](docs/architecture.md) | System architecture deep dive |
| [docs/manager-guide.md](docs/manager-guide.md) | Manager configuration |
| [docs/worker-guide.md](docs/worker-guide.md) | Worker deployment and troubleshooting |
| [docs/development.md](docs/development.md) | Contributing and local dev |

Chinese docs: [docs/zh-cn/](docs/zh-cn/) — including [FAQ](docs/zh-cn/faq.md)

## Build & Test

```bash
make build          # Build all images
make test           # Build + run all integration tests
make test SKIP_BUILD=1  # Run tests without rebuilding
make test-quick     # Smoke test only (test-01)
```

## Other Commands

```bash
# Send a task to Manager via CLI
make replay TASK="Create a Worker named alice for frontend development"

# Uninstall everything
make uninstall

# Push multi-arch images
make push VERSION=0.1.0 REGISTRY=ghcr.io REPO=higress-group/hiclaw

make help  # All available targets
```

## Community

- [Discord](https://discord.gg/NVjNA4BAVw)
- [DingTalk Group](https://qr.dingtalk.com/action/joingroup?code=v1,k1,0etR5l8fxeb/6/mzE5hRE1uy4tkiwxvPV9+TdBv7sEM=&_dt_no_comment=1&origin=11)
- WeChat Group — scan to join:

<p align="center">
  <img src="https://img.alicdn.com/imgextra/i1/O1CN01tTIeig1tQQKXGHvHV_!!6000000005896-2-tps-738-730.png" width="200" alt="WeChat Group" />
</p>

## License

Apache License 2.0
