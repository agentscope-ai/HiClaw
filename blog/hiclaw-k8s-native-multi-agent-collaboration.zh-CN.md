# HiClaw: 基于 Kubernetes 原生的多 Agent 协作编排系统

> 发布日期: 2026 年 4 月 14 日

---

## 跑多个 Agent 不难，难的是让它们一起干活

用过 AI 编程 Agent 的人都知道这个套路：一个 Agent，一个任务，一个上下文窗口。挺好使的，直到你发现不够用。

项目一旦需要前端、后端、DevOps 三个角色同时并行，你就又回到了手动协调的老路上：在 Agent 之间复制粘贴上下文，用表格跟踪谁在做什么，然后祈祷它们别互相踩代码。

HiClaw 就是来解决这个问题的。它不是又一个 Agent 运行时，而是一套让多个 Agent 像真实工程团队一样协作的编排系统。底层的设计原则和 Kubernetes 一脉相承：声明式、Controller Reconcile、CRD 扩展。

---

## 从容器编排到 Agent 编排

如果你熟悉 Kubernetes 生态，AI Agent 的演进路径会让你觉得似曾相识：

| 容器生态 | Agent 生态 | 解决的问题 |
|---|---|---|
| Docker（容器运行时） | OpenClaw / Claude Code（Agent 运行时） | 怎么跑一个隔离的工作单元 |
| Docker Compose（单机编排） | NemoClaw（单 Agent 沙箱管理） | 怎么管理运行时的生命周期和配置 |
| **Kubernetes（集群编排）** | **HiClaw（多 Agent 协作编排）** | 怎么让多个工作单元组成一个协调的系统 |

Kubernetes 没有替代 Docker，而是在它之上编排容器。HiClaw 也一样，不替代 Agent 运行时，而是在它之上编排协作。

不过这里有个关键区分，比类比本身更重要：**编排和协作是两回事**。

- **编排（Orchestration）**：管 Agent 的生命周期、资源分配、安全隔离。解决的是"怎么跑多个 Agent"
- **协作（Collaboration）**：定义 Agent 之间的组织关系、通信权限、任务委派、状态共享。解决的是"多个 Agent 怎么一起干活"

现在大多数多 Agent 系统做到了编排就停了。HiClaw 往前多走了一步。

---

## 声明式 Agent 团队：给 AI 写 CRD

写过 Kubernetes manifest 的人看到 HiClaw 的资源模型会很亲切。四种 CRD 风格的资源类型，统一用 `apiVersion: hiclaw.io/v1beta1`：

### Worker：Agent 世界里的 Pod

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
    你是一个专注于 React 的前端工程师...
```

每个 Worker 背后是：一个 Docker 容器（或 K8s Pod）+ 一个 Matrix 通信账号 + 一个 MinIO 存储空间 + 一个 Gateway Consumer Token。无状态、可销毁、可重建，跟 Pod 一个思路。

### Team：Agent 世界里的 Deployment

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

`hiclaw apply` 一个 Team 之后，Controller 会自动编排出这样的通信拓扑：

- **Leader Room**：Manager + Admin + Leader，委派通道
- **Team Room**：Leader + Admin + Workers，协作空间（注意 Manager 不在里面）
- **Worker Room**：Leader + Admin + 单个 Worker，私聊通道

这里的关键设计是：**Manager 永远不进 Team Room**。这就形成了一个委派边界。Manager 跟 Leader 说，Leader 去协调团队。组织规模再大，Manager 也不会成为瓶颈。

### Human：给人用的 RBAC

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Human
metadata:
  name: zhangsan
spec:
  displayName: "张三"
  permissionLevel: 2
  accessibleTeams: [frontend-team]
```

三个权限级别（Admin / Team / Worker）控制谁能跟谁说话，在 Matrix 协议层面通过 `groupAllowFrom` 强制执行。

---

## 三层组织架构

HiClaw 的架构直接映射真实的企业团队结构：

```
Admin（人类管理员）
  │
  ├── Manager（AI 协调者，可选部署）
  │     ├── Team Leader A（特殊 Worker，管团队内任务调度）
  │     │     ├── Worker A1
  │     │     └── Worker A2
  │     ├── Team Leader B
  │     │     └── Worker B1
  │     └── Worker C（独立 Worker，不属于任何 Team）
  │
  └── Human Users（真人用户，按权限级别接入）
        ├── Level 1: 等同 Admin
        ├── Level 2: Team 范围
        └── Level 3: 仅限指定 Worker
```

几个设计上的考量值得说一下：

- **Team Leader 本质就是 Worker**。同样的容器、同样的运行时，只是 SOUL 和 Skills 不同。有点像 K8s 里 control plane node 和 worker node 跑的都是同一个 kubelet。
- **Manager 不穿透 Team**。它只跟 Leader 说话，不直接联系团队内的 Worker。这样组织规模扩大的时候，Manager 不会变成瓶颈。
- **通信权限是声明式的**。`groupAllowFrom` 矩阵由 Controller 根据 Team/Human 资源定义自动生成，不用手动配。

---

## Controller 架构：Reconcile 一切

HiClaw Controller 走的是标准 Kubernetes Controller 路线：

```
YAML 资源声明
    ↓ hiclaw apply
kine（etcd 兼容层，SQLite 后端）/ 原生 K8s etcd
    ↓ Informer Watch
Controller Runtime
    ↓ Reconcile Loop
┌──────────────────────────────────────────────┐
│  Provisioner（基础设施配置）                    │
│  - Matrix 账号注册 & Room 创建                │
│  - MinIO 用户 & Bucket 配置                   │
│  - Higress Gateway Consumer & Route 配置      │
├──────────────────────────────────────────────┤
│  Deployer（配置部署）                          │
│  - Package 解析（file/http/nacos）            │
│  - openclaw.json 生成                         │
│  - SOUL.md / AGENTS.md / Skills 推送          │
│  - 容器启动 / Pod 创建                         │
├──────────────────────────────────────────────┤
│  Worker Backend 抽象层                         │
│  - Docker Backend（embedded 模式）             │
│  - K8s Backend（incluster 模式）               │
│  - Cloud Backend（云上托管模式）                │
└──────────────────────────────────────────────┘
```

两种部署模式，共用同一套 Reconciler：

| 模式 | 状态存储 | Worker 运行 | 适用场景 |
|---|---|---|---|
| Embedded | kine + SQLite | Docker 容器 | 开发者本地、小团队 |
| Incluster | K8s 原生 etcd | K8s Pod | 企业级、云上部署 |

Worker Backend 抽象层的思路跟 Kubernetes CRI 一样：编排层不关心 Worker 到底是 Docker 容器还是 K8s Pod。

---

## 基于 Matrix 协议的透明通信

大多数多 Agent 系统用内部 RPC 或消息队列做 Agent 间通信。问题是：这是个黑盒。你看不到 Agent 之间在聊什么，想介入还得自己写工具。

HiClaw 用的是 [Matrix 协议](https://matrix.org/)，一个去中心化的开放 IM 标准：

- **透明**：所有 Agent 间通信都在 Matrix Room 里，人类实时可见
- **Human-in-the-Loop 是默认的**：人类用同一个 IM 客户端（Element Web、FluffyChat 或任何 Matrix 客户端），@mention 就能介入
- **天然可审计**：消息自动持久化，完整审计轨迹开箱即用
- **没有供应商锁定**：Matrix 是去中心化开放协议，自托管、联邦、独立运行都行

实际协作长这样：

```
[Team Room]
Leader: @alice 实现密码强度校验，规则是至少 8 位
Alice:  收到，开始实现...

[Admin 在同一个 Room 里看到了，觉得规则要调]
Admin:  @alice 等一下，密码规则改成至少 12 位，必须包含大小写和特殊字符
Alice:  收到，已更新校验规则
Leader: 好的，我更新一下任务规格
```

没有隐藏的 Agent-to-Agent 调用。每个决策都摆在明面上，随时可以介入。

---

## 基于 Higress（CNCF Sandbox）的 LLM/MCP 安全访问

多 Agent 系统里，凭证管理是个大问题。如果每个 Worker 都拿着真实 API Key，一个 Agent 被攻破就全完了。

HiClaw 的安全层交给了 [Higress](https://github.com/alibaba/higress)。Higress 是 **CNCF Sandbox 项目**，基于 Envoy 的云原生 AI Gateway，原生支持 LLM 代理、MCP Server 托管和细粒度消费者鉴权。

### 核心原则：凭证永远不下发到 Agent

```
Worker（只有一个 Consumer Token: GatewayKey）
    → Higress AI Gateway
        ├── key-auth WASM 插件验证 Consumer Token
        ├── 检查该 Consumer 是否在目标 Route 的 allowedConsumers 里
        ├── 注入真实凭证（API Key / GitHub PAT / OAuth Token）
        └── 代理请求到上游服务
            ├── LLM API（OpenAI / Anthropic / 通义千问 等）
            ├── MCP Server（GitHub / Jira / 自定义 等）
            └── 其他外部服务
```

真实凭证只在 Gateway 内部。Agent 手里只有一个随时可以吊销的 Consumer Token。就算 Agent 被攻破，攻击者也拿不到任何可复用的凭证。

### LLM 访问安全

Worker 创建的时候，Controller 自动搞定这些事：

1. 生成 32 字节随机 GatewayKey，作为 Worker 的身份凭证
2. 在 Higress 注册 Gateway Consumer（`worker-{name}`），绑定 key-auth BEARER 凭证
3. 把这个 Consumer 加到所有 AI Route 的 `allowedConsumers` 列表里

Worker 的 API endpoint 指向 Gateway 地址，不是真实的 LLM Provider。Worker 压根不知道真实 API Key 长什么样。

### MCP Server 安全访问

MCP（Model Context Protocol）Server 给 Agent 提供工具调用能力，比如 GitHub 操作、数据库查询之类的。多 Agent 场景下，好几个 Worker 可能都要访问同一个 GitHub 仓库，但你不会想让每个 Worker 都拿着 GitHub PAT。

HiClaw 通过 Higress 托管 MCP Server 来解决这个问题：

```
Worker 调用 MCP 工具:
    POST https://aigw-local.hiclaw.io/mcp-servers/github/mcp
    Authorization: Bearer {GatewayKey}
        ↓
    Higress Gateway:
        1. 验证 Consumer Token
        2. 检查该 Consumer 是否有权访问 "github" MCP Server
        3. 注入真实 GitHub PAT
        4. 代理请求到 MCP Server 实现
```

### 细粒度权限控制，支持动态吊销

| 控制维度 | 实现方式 | 举个例子 |
|---|---|---|
| Worker 级 LLM 访问 | AI Route 的 allowedConsumers | Worker A 能用 GPT-4，Worker B 只能用 GPT-3.5 |
| Worker 级 MCP 访问 | MCP Server 的 allowedConsumers | Worker A 能访问 GitHub，Worker B 不行 |
| 动态权限变更 | 改 allowedConsumers 列表 | Manager 可以实时授予或吊销 Worker 的 MCP 访问权 |
| 即时吊销 | 从 allowedConsumers 里移除 | 不用轮换凭证，1-2 秒生效（WASM 插件热同步） |

这套权限模型跟 K8s 的 ServiceAccount + RBAC 是一个思路。Consumer Token 对应 ServiceAccount Token，`allowedConsumers` 对应 RBAC Policy。

### 为什么是 Higress

Higress 作为 CNCF Sandbox 项目，给 HiClaw 带来了几个关键能力：

- **AI-Native Gateway**：原生支持 LLM 代理（多 Provider 路由、Token 限流、Fallback）和 MCP Server 托管，不是拿通用 API Gateway 硬凑的
- **WASM 插件体系**：安全插件跑在 WASM 里，热更新不用重启，权限变更秒级生效
- **Envoy 内核**：继承了 Envoy 的高性能和可观测性，跟 CNCF 生态（Prometheus、OpenTelemetry）天然打通

---

## Kubernetes 概念映射

给 K8s 老手准备的速查表：

| Kubernetes | HiClaw | 说明 |
|---|---|---|
| Pod | Worker | 最小调度单元，无状态，可销毁重建 |
| Deployment | Team | 管理一组 Worker 的期望状态 |
| Service | Matrix Room | Worker 间的通信抽象 |
| ServiceAccount + RBAC | Consumer Token + allowedConsumers | 身份认证 + 细粒度权限控制 |
| CRD | Worker/Team/Human/Manager | 声明式资源定义 |
| Controller + Reconcile Loop | hiclaw-controller | 持续把实际状态收敛到期望状态 |
| Ingress / Gateway API | Higress Route（CNCF Sandbox） | LLM/MCP 访问入口 + 凭证注入 |
| NetworkPolicy | allowedConsumers + MCP Server 授权 | Agent 级别的 API 访问控制 |
| CRI | Worker Backend 抽象层 | 可插拔的底层运行时 |
| kubectl apply | hiclaw apply | 声明式资源管理 CLI |

会写 Kubernetes manifest，就能编排 AI Agent 团队。

---

## 跟 NVIDIA NemoClaw 的对比

NemoClaw 是 NVIDIA 的开源参考栈，用来在安全的 OpenShell 沙箱里跑 Agent。它在自己的领域做得很好，但解决的是一个不同层面的问题。

| 维度 | NemoClaw | HiClaw |
|---|---|---|
| 核心定位 | 单 Agent 安全沙箱 | 多 Agent 协作编排 |
| Agent 间关系 | 完全隔离，没有通信 | 声明式通信权限矩阵，结构化协作 |
| LLM 安全 | OpenShell 拦截推理请求，Agent 看不到凭证 | Higress（CNCF）Gateway 代理，Consumer Token 鉴权 |
| MCP Server 安全 | 没有集中管理 | Higress 托管 MCP Server，per-Worker 细粒度授权 |
| 动态权限 | 要重建 Sandbox | 改 allowedConsumers 就行，秒级生效 |
| 共享状态 | 每个 Sandbox 各管各的 | MinIO 共享文件系统 + 任务状态机 |
| 团队结构 | 没有 | Team CRD，声明式定义 |
| Human-in-the-Loop | 只有 CLI 交互 | Matrix Room 实时旁观和介入 |
| 配置模型 | Blueprint YAML（单 Agent） | K8s CRD 风格（Worker/Team/Human/Manager） |

### 互补，不是竞争

NemoClaw 和 HiClaw 解决的是 Agent 技术栈里不同层的问题：

```
┌──────────────────────────────────────────────┐
│  HiClaw（协作编排层）                          │
│  组织结构 / 通信权限 / 任务委派                  │
├──────────────────────────────────────────────┤
│  NemoClaw（安全运行时层）                       │
│  沙箱隔离 / 推理路由                            │
├──────────────────────────────────────────────┤
│  OpenClaw / CoPaw / Hermes（Agent 运行时）     │
│  LLM 交互 / 工具调用 / 技能执行                 │
└──────────────────────────────────────────────┘
```

HiClaw 的 Worker Backend 抽象层让它可以把 NemoClaw 接进来当底层运行时，把 NemoClaw 的沙箱安全和 HiClaw 的协作编排结合起来。跟 Kubernetes 通过 CRI 对接不同容器运行时（containerd、CRI-O）是一个道理：编排层不管运行时怎么实现。

---

## 快速开始

需要 Docker Desktop（Windows/macOS）或 Docker Engine（Linux），最低 2 CPU + 4 GB RAM。

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

打开 http://127.0.0.1:18088，登录 Element Web，跟你的 Manager Agent 聊起来就行。AI Gateway、Matrix 服务器、文件存储、Web 客户端，全在你自己机器上跑着。

---

## 接下来

- **ZeroClaw**：Rust 写的超轻量运行时，3.4MB 二进制，冷启动 <10ms
- **NanoClaw**：极简 Agent 运行时，不到 4000 行代码
- **Team 管理中心**：可视化 Dashboard，实时看到和控制 Agent 团队
- **Incluster Helm Chart**：生产级 K8s 部署方案（已实现，将在 v1.1.0 正式发布）
- **NemoClaw 运行时集成**：沙箱安全 + 协作编排，两手都要抓

---

## 链接

- GitHub: https://github.com/alibaba/hiclaw
- Discord: https://discord.gg/NVjNA4BAVw
- Higress（CNCF Sandbox）: https://github.com/alibaba/higress
- License: Apache 2.0
