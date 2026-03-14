<h1 align="center">
    <img src="https://img.alicdn.com/imgextra/i1/O1CN014DQemZ257thQOBg8L_!!6000000007480-55-tps-1489-391.svg" alt="HiClaw"  width="290" height="72.5">
  <br>
</h1>

[English](./README.md) | [中文](./README.zh-CN.md)

<p align="center">
  <a href="https://deepwiki.com/higress-group/hiclaw"><img src="https://img.shields.io/badge/DeepWiki-Ask_AI-navy.svg?logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACwAAAAyCAYAAAAnWDnqAAAAAXNSR0IArs4c6QAAA05JREFUaEPtmUtyEzEQhtWTQyQLHNak2AB7ZnyXZMEjXMGeK/AIi+QuHrMnbChYY7MIh8g01fJoopFb0uhhEqqcbWTp06/uv1saEDv4O3n3dV60RfP947Mm9/SQc0ICFQgzfc4CYZoTPAswgSJCCUJUnAAoRHOAUOcATwbmVLWdGoH//PB8mnKqScAhsD0kYP3j/Yt5LPQe2KvcXmGvRHcDnpxfL2zOYJ1mFwrryWTz0advv1Ut4CJgf5uhDuDj5eUcAUoahrdY/56ebRWeraTjMt/00Sh3UDtjgHtQNHwcRGOC98BJEAEymycmYcWwOprTgcB6VZ5JK5TAJ+fXGLBm3FDAmn6oPPjR4rKCAoJCal2eAiQp2x0vxTPB3ALO2CRkwmDy5WohzBDwSEFKRwPbknEggCPB/imwrycgxX2NzoMCHhPkDwqYMr9tRcP5qNrMZHkVnOjRMWwLCcr8ohBVb1OMjxLwGCvjTikrsBOiA6fNyCrm8V1rP93iVPpwaE+gO0SsWmPiXB+jikdf6SizrT5qKasx5j8ABbHpFTx+vFXp9EnYQmLx02h1QTTrl6eDqxLnGjporxl3NL3agEvXdT0WmEost648sQOYAeJS9Q7bfUVoMGnjo4AZdUMQku50McDcMWcBPvr0SzbTAFDfvJqwLzgxwATnCgnp4wDl6Aa+Ax283gghmj+vj7feE2KBBRMW3FzOpLOADl0Isb5587h/U4gGvkt5v60Z1VLG8BhYjbzRwyQZemwAd6cCR5/XFWLYZRIMpX39AR0tjaGGiGzLVyhse5C9RKC6ai42ppWPKiBagOvaYk8lO7DajerabOZP46Lby5wKjw1HCRx7p9sVMOWGzb/vA1hwiWc6jm3MvQDTogQkiqIhJV0nBQBTU+3okKCFDy9WwferkHjtxib7t3xIUQtHxnIwtx4mpg26/HfwVNVDb4oI9RHmx5WGelRVlrtiw43zboCLaxv46AZeB3IlTkwouebTr1y2NjSpHz68WNFjHvupy3q8TFn3Hos2IAk4Ju5dCo8B3wP7VPr/FGaKiG+T+v+TQqIrOqMTL1VdWV1DdmcbO8KXBz6esmYWYKPwDL5b5FA1a0hwapHiom0r/cKaoqr+27/XcrS5UwSMbQAAAABJRU5ErkJggg==" alt="DeepWiki"></a>
  <a href="https://discord.com/invite/NVjNA4BAVw"><img src="https://img.shields.io/badge/Discord-Join_Us-blueviolet.svg?logo=discord" alt="Discord"></a>
  <a href="https://qr.dingtalk.com/action/joingroup?code=v1,k1,q3lHf2AY4o0W2aBsoyJE0kgYnGcBFqpBuwDTjJ36iu8=&_dt_no_comment=1&origin=11"><img src="https://img.shields.io/badge/DingTalk-Join_Us-orange.svg" alt="DingTalk"></a>
</p>

**HiClaw is an open-source Collaborative Multi-Agent OS for transparent, human-in-the-loop task coordination via Matrix rooms, ensuring that the entire process is visible to humans and allows for intervention at any time.
Designed with a Manager-Workers architecture, it allows humans to coordinate multiple Worker Agents through a Manager Agent to complete complex tasks. This accelerates the realization of OPOC (One-Person-One-Company) and the deployment of enterprise digital employees.**

HiClaw is not positioned as a competitor to other "xxClaw" products; it is fundamentally an Agent Collaboration System.

Key Features:

- 🦞**Customizable "Claws"**: Each Claw supports user customization. It can be an OpenClaw, CoPaw, NanoClaw, ZeroClaw, or a custom-built enterprise agent. The system currently comes pre-installed with OpenClaw.
  
- 🧬**Manager Claw Role**: Introduces a dedicated Manager Claw role. This eliminates the need for humans to manually manage every working Worker Claw, significantly reducing management overhead.
  
- ☎️**Native Matrix Communication**: Utilizes the Element IM client and Tuwunel IM server (both based on the Matrix real-time communication protocol). Unlike native protocols, this approach bypasses the complex integration and approval processes required by enterprise IMs like DingTalk or Feishu. This allows users to quickly experience the seamless interaction ("the thrill") of model services within an IM environment, while still supporting native OpenClaw IM integration.
  
- 📦**Shared File System (MinIO)**: Integrates MinIO as a shared file system for information exchange between agents. It also facilitates human-to-human collaboration, with shared memory stored directly on this file system.
  
- 🔐**Secure Entry via Higress AI Gateway**: Incorporates the Higress AI Gateway to centralize entry points and manage credentials. This significantly reduces security risks and alleviates user concerns regarding the security of native "Lobster" (OpenClaw) deployments.

## News

- **2026-03-10**: HiClaw 1.0.4 released with CoPaw Worker support — 80% less memory, local host mode for browser automation. Read more on our [blog](blog/hiclaw-1.0.4-release.md).
- **2026-03-04**: We officially open source HiClaw, an Agent Teams System. Read more on our [blog](https://github.com/higress-group/hiclaw/blob/main/blog/hiclaw-announcement.md).


## Why HiClaw

- **Enterprise-Grade Security**: Worker Agents never hold real API Keys or GitHub PATs; they operate using only a consumer token (similar to an "ID badge"). Even if a Worker Agent is compromised, attackers cannot obtain any real credentials.

- **Multi-Agent Group Chat Network**: The Manager Agent intelligently decomposes tasks and coordinates multiple Worker Agents to execute them in parallel, significantly enhancing the capability to handle complex workflows.

- **Matrix Protocol Driven**: Built on the open Matrix IM protocol, all agent communications are transparent and auditable. The system natively supports distributed deployment and federated communication.

- **Full Human Supervision**: Humans can enter any Matrix room at any time to observe agent conversations, allowing for real-time intervention or correction of agent behavior to ensure safety and control.

- **Truly Out-of-the-Box IM Experience**: Comes with a built-in Matrix server, eliminating the need to apply for DingTalk or Feishu bots or wait for internal approvals. Users can start chatting immediately by opening Element Web in a browser or using mobile Matrix clients (such as Element or FluffyChat) on iOS, Android, and Web to command agents anytime, anywhere.

- **Manager-Worker Architecture**: Features a clear two-tier Manager-Worker architecture with distinct responsibilities. This design makes it easy to extend and customize Worker Agents for different scenarios, supporting the management of CoPaw, NanoClaw, ZeroClaw, or custom-built enterprise agents.

- **One command to start**: A single `curl | bash` sets everything up — Higress AI Gateway, Matrix server, file storage, web client, and the Manager Agent itself.

- **Skills ecosystem**: Workers can pull from [skills.sh](https://skills.sh) (80,000+ community skills) on demand. Safe to use because Workers can't access real credentials anyway.

## Quick Start

**Prerequisites**: Docker Desktop (Windows/macOS) or Docker Engine (Linux). If deploying on virtual machines such as ECS or cloud desktops, please use a Linux operating system. For requirements involving a graphical interface, Ubuntu is recommended.Please note that the official image package currently does not support Windows systems on virtual machines, as Windows in a VM environment is not a Linux Container.

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) (Windows / macOS)
- [Docker Engine](https://docs.docker.com/engine/install/) (Linux) or [Podman Desktop](https://podman-desktop.io/) (alternative)

**Resource requirements**: Minimum 2 CPU cores and 4 GB RAM. If you want to deploy multiple Workers for a more powerful Agent Teams experience, **4 CPU cores and 8 GB RAM are recommended** — OpenClaw's memory usage is relatively high. In Docker Desktop, go to Settings → Resources to adjust.

Step 1: Open your terminal.

F**or macOS, enter the following installation command**
```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

**For Windows (requires PowerShell 7+), enter the corresponding command below**

```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://higress.ai/hiclaw/install.ps1'))
```
Here, we will input the installation command for macOS.

Step 2: Select a language. Here, we choose Chinese.

Step 3: Select the installation mode. Here, we choose Alibaba Cloud Bailian Quick Install.

Step 4: Select the Large Language Model (LLM) provider. We select Bailian. You can also connect to other model services supporting the OpenAPI protocol. Please note that the Anthropic protocol is not yet supported.

Step 5: Select the model interface. The Bailian Coding Plan interface differs from the general Bailian interface; here, we select the Coding Plan interface.[Bailian Coding Plan](https://modelstudio.console.alibabacloud.com/ap-southeast-1/?source_channel=Bk5s5ordYR&tab=coding-plan#/efm/index).

Step 6: Select the model series. If you chose the Bailian Coding Plan in Step 5, the default model is qwen3.5-plus. Once the Matrix room is established, you can send commands to the Manager to switch to other models as needed.

Step 7: Begin testing API connectivity. If the test fails, please check your model API configuration (e.g., ensure the key is pasted completely without extra spaces). If necessary, consult your model provider.

Step 8: Select the network access mode. Here, we choose Local Use Only. If you wish to allow external access (e.g., to create a Matrix room with colleagues), select Allow External Access. After making your selection, press Enter. The system will use default values for the port number, gateway host port, Higress console host port, Matrix domain, Element Web direct access port, and file system domain.

Step 9: For configurations regarding GitHub Integration, Skills Registry, Data Persistence, Docker Volumes, and Manager Workspace, simply press Enter to accept the default configurations.

Step 10: Select the Manager Worker runtime, OpenClaw and Copaw are currently supported, with future support for NanoClaw, ZeroClaw, and others

Step 11: Wait for the installation to complete.Upon completion, a login password will be automatically generated.
- To access and use the system via mobile devices, you will need an US-region Apple ID (or equivalent region setting) to download FluffyChat or Element Mobile. (These specific IM clients are used because they support the Matrix protocol).
- After downloading, connect to your Matrix server address to manage your Agent team anytime, anywhere.

Step 112: In your web browser, navigate to http://127.0.0.1:18088/#/login. Log in to Element using your username and password. You are now ready to start using "Claw"! Tell the Manager to create Workers and assign tasks.

⚠️ Note: HiClaw has Higress AI Gateway built-in, which is responsible for managing model API Keys and securing ingress traffic. The switching and addition of model API Keys, as well as the management of routing, domains, and certificates, can all be handled within the Higress Console.


## Upgrade

To update to a new version, simply execute the following command in your terminal to perform an in-place upgrade to the latest version by default.
- In-place Upgrade: Preserves all existing data and configurations.
- Fresh Re-installation: Will delete all data.

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

To upgrade to a specific version:

```bash
HICLAW_VERSION=v1.0.5 bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```


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

- **CoPaw** ✅ **[Released in 1.0.4](blog/hiclaw-1.0.4-release.md)** — Lightweight agent runtime by AgentScope. Docker mode uses ~150MB (vs ~500MB for OpenClaw), plus local host mode for browser automation and local file access.
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
- [DingTalk Group](https://qr.dingtalk.com/action/joingroup?code=v1,k1,q3lHf2AY4o0W2aBsoyJE0kgYnGcBFqpBuwDTjJ36iu8=&_dt_no_comment=1&origin=11)
- WeChat Group — scan to join:

<p align="center">
  <img src="https://img.alicdn.com/imgextra/i4/O1CN011Gb82b1TNyCQlA5eh_!!6000000002371-2-tps-790-752.png" width="200" alt="WeChat Group" />
</p>

## License

Apache License 2.0
