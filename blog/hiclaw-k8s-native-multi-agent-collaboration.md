# HiClaw: A Kubernetes-Native Orchestration System for Multi-Agent Collaboration

> Published: April 14, 2026

---

## Running Multiple Agents Is Easy. Getting Them to Work Together Is the Hard Part.

If you've used AI coding agents, you know the drill: one agent, one task, one context window. Works great, until it doesn't.

The moment your project needs a frontend dev, a backend engineer, and a DevOps person all working in parallel, you're back to manual coordination. Copy-pasting context between agents. Tracking who's doing what in a spreadsheet. Hoping they don't step on each other's code.

That's the problem HiClaw solves. It's not another agent runtime. It's an orchestration system that lets multiple agents collaborate like a real engineering team, built on the same declarative principles that made Kubernetes the standard for container orchestration.

---

## From Container Orchestration to Agent Orchestration

If you know the Kubernetes ecosystem, the evolution of AI agents should feel familiar:

| Container Ecosystem | Agent Ecosystem | Problem Solved |
|---|---|---|
| Docker (container runtime) | OpenClaw / Claude Code (agent runtime) | How to run an isolated work unit |
| Docker Compose (single-host orchestration) | NemoClaw (single-agent sandbox) | How to manage runtime lifecycle and config |
| **Kubernetes (cluster orchestration)** | **HiClaw (multi-agent collaboration)** | How to make multiple work units function as a coordinated system |

Kubernetes didn't replace Docker. It orchestrated containers on top of it. HiClaw does the same thing for agent runtimes: it orchestrates collaboration on top of them.

But there's a distinction here that matters more than the analogy itself: **orchestration and collaboration are not the same thing**.

- **Orchestration** manages agent lifecycle, resource allocation, and security isolation. It answers "how to run multiple agents"
- **Collaboration** defines organizational structure, communication permissions, task delegation, and shared state. It answers "how multiple agents work together"

Most multi-agent systems today stop at orchestration. HiClaw takes it further.

---

## Declarative Agent Teams: CRDs for AI

If you've written a Kubernetes manifest, HiClaw's resource model will feel right at home. Four CRD-style resource types, all under `apiVersion: hiclaw.io/v1beta1`:

### Worker: The Pod of Agent Orchestration

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Worker
metadata:
  name: alice
spec:
  model: claude-sonnet-4-6
  runtime: openclaw
  skills: [github-operations]
  mcpServers: [github]
  soul: |
    You are a frontend engineer specializing in React...
```

Each Worker maps to: a Docker container (or K8s Pod) + a Matrix communication account + a MinIO storage space + a Gateway Consumer Token. Stateless, disposable, rebuildable. Just like a Pod.

### Team: The Deployment of Agent Orchestration

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Team
metadata:
  name: frontend-team
spec:
  leader:
    name: frontend-lead
    model: claude-sonnet-4-6
    heartbeat:
      enabled: true
      every: 10m
  workers:
    - name: alice
      model: claude-sonnet-4-6
      skills: [github-operations]
      mcpServers: [github]
    - name: bob
      model: qwen3.5-plus
      runtime: copaw
  peerMentions: true
```

When you `hiclaw apply` a Team, the Controller automatically provisions this communication topology:

- **Leader Room**: Manager + Admin + Leader, the delegation channel
- **Team Room**: Leader + Admin + Workers, the collaboration space (Manager is not in here)
- **Worker Rooms**: Leader + Admin + individual Worker, private channels

The key design decision: **Manager never enters the Team Room**. This creates a delegation boundary. Manager talks to the Leader, Leader coordinates the team. No bottleneck, no matter how big the organization gets.

### Human: RBAC for People

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Human
metadata:
  name: zhangsan
spec:
  displayName: "Zhang San"
  permissionLevel: 2
  accessibleTeams: [frontend-team]
```

Three permission levels (Admin / Team / Worker) control who can talk to whom, enforced at the Matrix protocol level via `groupAllowFrom` configuration.

---

## The Three-Tier Organization

HiClaw's architecture maps directly to real enterprise team structures:

```
Admin (Human)
  │
  ├── Manager (AI coordinator, optional)
  │     ├── Team Leader A (special Worker, manages team tasks)
  │     │     ├── Worker A1
  │     │     └── Worker A2
  │     ├── Team Leader B
  │     │     └── Worker B1
  │     └── Worker C (standalone, no team)
  │
  └── Human Users (real people, permission-based access)
        ├── Level 1: Admin-equivalent
        ├── Level 2: Team-scoped
        └── Level 3: Worker-only
```

A few design choices worth calling out:

- **Team Leaders are just Workers.** Same container, same runtime, different SOUL and skills. Think of how K8s control plane nodes and worker nodes both run the same kubelet.
- **Manager doesn't penetrate Teams.** It only talks to Leaders, never directly to team Workers. As the organization scales, Manager doesn't become a bottleneck.
- **Communication permissions are declarative.** The `groupAllowFrom` matrix is generated by the Controller based on Team/Human resource definitions. No manual wiring needed.

---

## Controller Architecture: Reconcile All the Things

The HiClaw Controller follows the standard Kubernetes controller pattern:

```
YAML Resources
    ↓ hiclaw apply
kine (etcd-compatible, SQLite backend) / native K8s etcd
    ↓ Informer Watch
Controller Runtime
    ↓ Reconcile Loop
┌──────────────────────────────────────────────┐
│  Provisioner (Infrastructure)                │
│  - Matrix account registration & Room setup  │
│  - MinIO user & bucket configuration         │
│  - Higress Gateway Consumer & Route setup    │
├──────────────────────────────────────────────┤
│  Deployer (Configuration)                    │
│  - Package resolution (file/http/nacos)      │
│  - openclaw.json generation                  │
│  - SOUL.md / AGENTS.md / Skills push         │
│  - Container start / Pod creation            │
├──────────────────────────────────────────────┤
│  Worker Backend Abstraction                  │
│  - Docker Backend (embedded mode)            │
│  - K8s Backend (incluster mode)              │
│  - Cloud Backend (managed mode)              │
└──────────────────────────────────────────────┘
```

Two deployment modes, one Reconciler:

| Mode | State Store | Worker Runtime | Use Case |
|------|-------------|----------------|----------|
| Embedded | kine + SQLite | Docker containers | Developer local, small teams |
| Incluster | K8s native etcd | K8s Pods | Enterprise, cloud deployment |

The Worker Backend abstraction works like Kubernetes CRI: the orchestration layer doesn't care whether Workers run as Docker containers or K8s Pods.

---

## Transparent Communication via Matrix Protocol

Most multi-agent systems use internal RPC or message queues for agent communication. The problem? It's a black box. You can't see what agents are saying to each other, and intervening means building custom tooling.

HiClaw uses the [Matrix protocol](https://matrix.org/), a decentralized open IM standard, as its communication layer:

- **Transparent**: All agent-to-agent communication happens in Matrix Rooms. Humans see everything in real-time.
- **Human-in-the-Loop by default**: Humans use the same IM client (Element Web, FluffyChat, or any Matrix client). Just @mention an agent to jump in.
- **Naturally auditable**: Messages are persisted automatically. Complete audit trail out of the box.
- **No vendor lock-in**: Matrix is a decentralized open protocol. Self-host, federate, or run standalone.

Here's what collaboration actually looks like:

```
[Team Room]
Leader: @alice Implement password validation, minimum 8 characters
Alice:  On it...

[Admin sees this in the same room, decides to adjust]
Admin:  @alice Hold on, change the rule to minimum 12 chars with uppercase + special characters
Alice:  Got it, updating the validation rules
Leader: Noted, I'll update the task spec
```

No hidden agent-to-agent calls. Every decision is out in the open, and you can step in anytime.

---

## Secure LLM & MCP Access with Higress (CNCF Sandbox)

In a multi-agent system, credential management is a big deal. If every Worker holds real API keys, one compromised agent and you're exposed.

HiClaw's security layer is handled by [Higress](https://github.com/alibaba/higress), a **CNCF Sandbox project**. It's an Envoy-based cloud-native AI Gateway with native support for LLM proxying, MCP Server hosting, and fine-grained consumer authentication.

### Core Principle: Credentials Never Reach the Agent

```
Worker (holds only a Consumer Token: GatewayKey)
    → Higress AI Gateway
        ├── key-auth WASM plugin validates Consumer Token
        ├── Checks if Consumer is in the target Route's allowedConsumers
        ├── Injects real credentials (API Key / GitHub PAT / OAuth Token)
        └── Proxies request to upstream
            ├── LLM API (OpenAI / Anthropic / Qwen / ...)
            ├── MCP Server (GitHub / Jira / custom / ...)
            └── Other external services
```

Real credentials only exist inside the Gateway. Agents hold a revocable Consumer Token and nothing else. Even if an agent gets compromised, the attacker walks away with nothing reusable.

### LLM Access Security

When a Worker is created, the Controller automatically:

1. Generates a 32-byte random GatewayKey as the Worker's identity credential
2. Registers a Gateway Consumer (`worker-{name}`) with key-auth BEARER binding
3. Adds the Consumer to all AI Route `allowedConsumers` lists

The Worker's API endpoint points to the Gateway address, not the real LLM provider. The Worker has no idea the real API key even exists.

### MCP Server Security

MCP (Model Context Protocol) Servers give agents tool-calling capabilities like GitHub operations, database queries, and so on. In a multi-agent setup, several Workers might need access to the same GitHub repo, but you don't want each of them holding a GitHub PAT.

HiClaw solves this through Higress-hosted MCP Servers:

```
Worker calls MCP tool:
    POST https://aigw-local.hiclaw.io/mcp-servers/github/mcp
    Authorization: Bearer {GatewayKey}
        ↓
    Higress Gateway:
        1. Validates Consumer Token
        2. Checks if Consumer is authorized for "github" MCP Server
        3. Injects real GitHub PAT
        4. Proxies to MCP Server implementation
```

### Fine-Grained Permissions with Dynamic Revocation

| Control Dimension | Mechanism | Example |
|---|---|---|
| Per-Worker LLM access | AI Route allowedConsumers | Worker A can use GPT-4, Worker B only gets GPT-3.5 |
| Per-Worker MCP access | MCP Server allowedConsumers | Worker A can access GitHub, Worker B can't |
| Dynamic permission changes | Modify allowedConsumers list | Manager can grant/revoke MCP access in real-time |
| Instant revocation | Remove from allowedConsumers | No credential rotation needed, takes effect in 1-2 seconds (WASM hot-sync) |

This permission model follows the same logic as K8s ServiceAccount + RBAC. Consumer Token is the ServiceAccount Token, `allowedConsumers` is the RBAC Policy.

### Why Higress

As a CNCF Sandbox project, Higress brings a few key things to the table:

- **AI-Native Gateway**: Native LLM proxying (multi-provider routing, token rate limiting, fallback) and MCP Server hosting, not bolted on through generic API gateway plugins
- **WASM Plugin System**: Security plugins run as WASM, hot-updatable without restart, permission changes take effect in seconds
- **Envoy Core**: Inherits Envoy's performance and observability, natively integrates with the CNCF ecosystem (Prometheus, OpenTelemetry)

---

## Kubernetes Concept Mapping

A cheat sheet for K8s veterans:

| Kubernetes | HiClaw | Notes |
|---|---|---|
| Pod | Worker | Smallest schedulable unit, stateless, disposable |
| Deployment | Team | Manages desired state of a group of Workers |
| Service | Matrix Room | Communication abstraction between Workers |
| ServiceAccount + RBAC | Consumer Token + allowedConsumers | Identity auth + fine-grained access control |
| CRD | Worker/Team/Human/Manager | Declarative resource definitions |
| Controller + Reconcile Loop | hiclaw-controller | Continuously converges actual state to desired state |
| Ingress / Gateway API | Higress Route (CNCF Sandbox) | LLM/MCP access entry + credential injection |
| NetworkPolicy | allowedConsumers + MCP Server authorization | Agent-level API access control |
| CRI | Worker Backend abstraction | Pluggable underlying runtimes |
| kubectl apply | hiclaw apply | Declarative resource management CLI |

If you can write a Kubernetes manifest, you can orchestrate an AI agent team.

---

## Comparison with NVIDIA NemoClaw

NemoClaw is NVIDIA's open-source reference stack for running agents in secure OpenShell sandboxes. It does its job well, but it's solving a different problem at a different layer.

| Dimension | NemoClaw | HiClaw |
|---|---|---|
| Core focus | Single-agent secure sandbox | Multi-agent collaboration orchestration |
| Agent relationships | Fully isolated, no communication | Declarative communication matrix, structured collaboration |
| LLM security | OpenShell intercepts inference, agent never sees credentials | Higress (CNCF) Gateway proxy, Consumer Token auth |
| MCP Server security | No centralized management | Higress-hosted MCP Servers, per-Worker fine-grained authorization |
| Dynamic permissions | Requires sandbox rebuild | Modify allowedConsumers, takes effect in seconds |
| Shared state | Each sandbox is independent | MinIO shared filesystem + task state machine |
| Team structure | None | Team CRD, declarative definition |
| Human-in-the-Loop | CLI interaction only | Matrix Room real-time observation and intervention |
| Configuration model | Blueprint YAML (single agent) | K8s CRD-style (Worker/Team/Human/Manager) |

### Complementary, Not Competing

NemoClaw and HiClaw solve different layers of the agent stack:

```
┌──────────────────────────────────────────────┐
│  HiClaw (Collaboration Orchestration Layer)  │
│  Organization / Communication / Delegation   │
├──────────────────────────────────────────────┤
│  NemoClaw (Secure Runtime Layer)             │
│  Sandbox Isolation / Inference Routing       │
├──────────────────────────────────────────────┤
│  OpenClaw / CoPaw / Hermes (Agent Runtime)   │
│  LLM Interaction / Tool Calling / Skills     │
└──────────────────────────────────────────────┘
```

HiClaw's Worker Backend abstraction makes it possible to plug in NemoClaw as the underlying runtime, combining NemoClaw's sandbox security with HiClaw's collaboration orchestration. Same idea as Kubernetes using CRI to plug in different container runtimes (containerd, CRI-O): the orchestration layer doesn't care how the runtime is implemented.

---

## Getting Started

You'll need Docker Desktop (Windows/macOS) or Docker Engine (Linux). Minimum 2 CPU + 4 GB RAM.

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

Open http://127.0.0.1:18088, log in to Element Web, and start chatting with your Manager Agent. That's it. AI gateway, Matrix server, file storage, web client, all running on your machine.

---

## What's Next

- **ZeroClaw**: Rust-based ultra-lightweight runtime, 3.4MB binary, <10ms cold start
- **NanoClaw**: Minimal agent runtime, under 4000 lines of code
- **Team Management Center**: Visual dashboard for watching and controlling agent teams
- **Incluster Helm Chart**: Production-grade K8s deployment (implemented, shipping in v1.1.0)
- **NemoClaw Runtime Integration**: Sandbox security meets collaboration orchestration

---

## Links

- GitHub: https://github.com/alibaba/hiclaw
- Discord: https://discord.gg/NVjNA4BAVw
- Higress (CNCF Sandbox): https://github.com/alibaba/higress
- License: Apache 2.0
