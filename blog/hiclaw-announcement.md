# HiClaw：OpenClaw 超进化，更安全更易用，5 分钟打造出一人公司

> 发布日期：2026 年 2 月 27 日

---

## 你是否也曾这样？

作为 OpenClaw 的深度用户，我深刻体会到它的强大——一个 Agent 就能帮你写代码、查邮件、操作 GitHub。但当你开始做更复杂的项目时，问题就来了：

**安全问题让人睡不着**：每个 Agent 都要配置自己的 API Key，GitHub PAT、LLM Key 散落各处。2026 年 1 月的 CVE-2026-25253 漏洞让我意识到，这种 "self-hackable" 架构在便利的同时也带来了风险。

**一个 Agent 承担太多角色**：让它做前端，又做后端，还要写文档。`skills/` 目录越来越乱，`MEMORY.md` 里混杂各种记忆，每次加载都要塞一大堆无关上下文。

**想指挥多个 Agent 协作，但没有好工具**：手动配置、手动分配任务、手动同步进度……你想专注于业务决策，而不是当 AI 的"保姆"。

**移动端体验一言难尽**：想在手机上指挥 Agent 干活，却发现飞书、钉钉的机器人接入流程要几天甚至几周。

如果你有同感，那 **HiClaw** 就是为而生的。

---

## HiClaw 是什么？

**HiClaw = OpenClaw 超进化**

核心创新是引入 **Manager Agent** 角色——你的 "AI 管家"。它不直接干活，而是帮你管理一批 Worker Agent。

```
┌─────────────────────────────────────────────────────┐
│                   你的本地环境                       │
│  ┌───────────────────────────────────────────────┐ │
│  │           Manager Agent (AI 管家)             │ │
│  │                    ↓ 管理                     │ │
│  │    Worker Alice    Worker Bob    Worker ...   │ │
│  │    (前端开发)       (后端开发)                  │ │
│  └───────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
         ↑
    你（真人管理员）
    只需做决策，不用当保姆
```

---

## 技术架构：OpenClaw 的"器官移植"

OpenClaw 的设计就像一个完整的生物体：有**大脑**（LLM）、**中枢神经系统**（pi-mono）、**眼睛和嘴**（各种 Channel）。但原生设计中，大脑和感知器官都是"外接"的——你需要自己去配置 LLM Provider、去对接各种消息渠道。

HiClaw 做了一次"器官移植"手术，把这些外接组件变成**内置器官**：

```
┌────────────────────────────────────────────────────────────────────┐
│                         HiClaw All-in-One                          │
│  ┌──────────────────────────────────────────────────────────────┐ │
│  │                     OpenClaw (pi-mono)                       │ │
│  │                      中枢神经系统                             │ │
│  └──────────────────────────────────────────────────────────────┘ │
│           ↑                              ↑                        │
│  ┌────────────────┐              ┌────────────────┐               │
│  │  Higress AI    │              │   Tuwunel      │               │
│  │  Gateway       │              │   Matrix       │               │
│  │  (大脑接入)     │              │   Server       │               │
│  │                │              │   (感知器官)    │               │
│  │  灵活切换       │              │                │               │
│  │  LLM供应商      │              │  Element Web   │               │
│  │  和模型         │              │  FluffyChat    │               │
│  └────────────────┘              │  (自带客户端)   │               │
│                                  └────────────────┘               │
└────────────────────────────────────────────────────────────────────┘
```

### LLM 接入：Higress AI Gateway

**大脑不再外接，而是通过 AI Gateway 灵活管理**：

- **一个入口，多种模型**：在 Higress 控制台即可切换阿里云通义、OpenAI、Claude 等不同供应商
- **凭证集中管理**：API Key 只需要配置一次，所有 Agent 共享
- **按需授权**：每个 Worker 只获得调用权限，永远接触不到真实的 API Key

### 通信接入：内置 Matrix Server

**感知器官也变成内置的**：

- **Tuwunel Matrix Server**：开箱即用的消息服务器，无需任何配置
- **自带 Element Web 客户端**：浏览器打开就能对话
- **移动端友好**：支持 FluffyChat、Element Mobile 等全平台客户端
- **零对接成本**：不需要申请飞书/钉钉机器人，不需要等待审批

> 💡 换个比喻：OpenClaw 原生就像一台组装电脑，你需要自己买显卡（LLM）、显示器（Channel）然后装驱动。HiClaw 则是一台开箱即用的笔记本，所有外设都集成好了，开机就能干活。

---

## Multi-Agent 系统：你的 AI 管家贾维斯

在组件封装的基础上，HiClaw 还实现了一套**开箱即用的 Multi-Agent 系统**——Manager Agent 管理 Worker Agent，就像钢铁侠的管家 **贾维斯** 一样。

### 按需启用，两种模式

这套系统是**按需启用**的，你可以灵活选择：

**模式一：直接对话 Manager**
- 简单任务直接告诉 Manager，它自己处理
- 适合快速问答、简单操作

**模式二：Manager 分派 Worker**
- 复杂任务由 Manager 拆解，分配给专业 Worker
- 每个 Worker 有独立的 Skills 和 Memory
- 技能和记忆**完全隔离**，不会互相污染

### Human in the Loop：全程透明，随时干预

和 OpenClaw 原生的 Sub Agent 系统相比，HiClaw 的 Multi-Agent 系统不仅更易用，而且**更透明**：

```
┌─────────────────────────────────────────────────────────────┐
│                  Matrix 项目群聊房间                        │
│                                                             │
│  你: 实现一个登录页面                                        │
│                                                             │
│  Manager: 收到，我来分派...                                  │
│           → @alice 前端页面                                  │
│           → @bob 后端 API                                    │
│                                                             │
│  Alice: 正在实现登录组件...                                  │
│  Bob: API 接口已定义好...                                    │
│                                                             │
│  你: @bob 等下，密码规则改成至少8位                          │  ← 随时干预
│                                                             │
│  Bob: 好的，已修改...                                        │
│  Alice: 收到，前端校验也更新了                               │
│                                                             │
│  Manager: 任务完成，请 Review                                │
└─────────────────────────────────────────────────────────────┘
```

**核心优势**：
- **全程可见**：所有 Agent 的协作过程都在 Matrix 群聊里
- **随时介入**：发现问题可以直接 @某个 Agent 修正
- **自然交互**：就像在微信群里和一群同事协作

---

## 协作架构：Supervisor + Swarm 的融合

从 Manager-Worker 的角度看，这是一个 **Supervisor 架构**：Manager 作为中心节点协调所有 Worker。但因为基于 Matrix 群聊房间协作，它同时也具备了 **Swarm（蜂群）架构** 的特点。

### 共享上下文，无需重复沟通

在 Swarm 模式下，每个 Agent 都能看到群聊房间里的完整上下文：

- Alice 说"我在做登录页面"
- Bob 自动知道前端在做什么，API 设计时可以配合
- 不需要 Manager 做额外的信息同步

### 防惊群设计：按需唤醒

一个关键问题：如果群里每条消息都触发所有 Agent 去调用 LLM，那成本和延迟都会爆炸。HiClaw 做了**防惊群设计**：

```
规则：Agent 只有被 @ 的时候才会触发 LLM 调用
```

- 群聊消息主要是有意义的沟通信息
- Agent 不会因为无关消息被唤醒
- 成本可控，响应及时

### 中间产物不污染上下文

另一个关键设计：**工作中间产物不发到群聊**。

Agent 之间的大量协作（文件交换、代码片段、临时数据）通过底层的 **MinIO 共享文件系统** 完成：

```
┌─────────────────────────────────────────────────────────────┐
│                    Matrix 群聊房间                          │
│         只保留有意义的沟通和决策记录                         │
│                   （上下文精简）                             │
└─────────────────────────────────────────────────────────────┘
                            ↑ 有意义的信息
                            │
┌─────────────────────────────────────────────────────────────┐
│                   MinIO 共享文件系统                        │
│         代码、文档、临时文件等大量中间产物                    │
│                  （不污染群聊上下文）                        │
└─────────────────────────────────────────────────────────────┘
```

这样群聊里的上下文始终保持在合理规模，不会因为大量文件交换而迅速膨胀。

---

## 开箱即用：5 分钟完成部署

HiClaw 集成了多个开源组件（Higress、Tuwunel、MinIO、Element Web...），但不用担心部署复杂度。

### 一键安装脚本

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

bash 脚本的特点：

- **跨平台兼容**：Mac / Linux 一键安装
- **智能检测**：根据时区自动选择最近的镜像仓库
- **Docker 封装**：所有组件跑在容器里，屏蔽操作系统差异
- **最少配置**：只需要一个 LLM API Key，其他都是可选的

**Windows 用户**可以使用 PowerShell 脚本：

```powershell
Invoke-Expression (Invoke-WebRequest -Uri "https://higress.ai/hiclaw/install.ps1" -UseBasicParsing).Content
```

### 安装完成后你拥有

| 组件 | 端口 | 用途 |
|------|------|------|
| Higress Gateway | 18080 | AI Gateway + 反向代理 |
| Higress Console | 18001 | 模型配置、路由管理 |
| Element Web | 18080 | 对话客户端（浏览器） |
| MinIO | 9000/9001 | 共享文件系统 |

**真正的开箱即用——不是"开箱后还要配半天"的那种。**

---

## 更安全：Manager 能管理，但不能泄密

### 问题：OpenClaw 的凭证风险

原生 OpenClaw 架构下，每个 Agent 都需要持有真实的 API Key：

```json
// 每个 agent 的配置文件里都要写
{
  "providers": {
    "anthropic": { "apiKey": "sk-ant-xxx" },  // 真实的 Key
    "github": { "token": "ghp_xxx" }          // 真实的 PAT
  }
}
```

一旦 Agent 被攻击或意外输出，这些凭证就可能泄露。

### HiClaw 的解决方案

**Worker 永远不持有真实凭证**

```
┌──────────────┐      ┌──────────────────┐      ┌─────────────┐
│   Worker     │─────►│  Higress AI      │─────►│  LLM API    │
│   (只持有    │      │  Gateway         │      │  GitHub API │
│   Consumer   │      │  (凭证集中管理)   │      │  ...        │
│   Token)     │      │                  │      │             │
└──────────────┘      └──────────────────┘      └─────────────┘
```

- Worker 只持有一个 Consumer Token（类似于"工牌"）
- 真实的 API Key、GitHub PAT 等凭证存储在 AI Gateway
- Worker 调用外部服务时，通过 Gateway 代理
- **即使 Worker 被攻击，攻击者也拿不到真实凭证**

**Manager 的安全设计**：

- Manager 知道 Worker 要做什么任务，但不知道 API Key、GitHub PAT
- Manager 的职责是"管理和协调"，不直接执行文件读写、代码编写
- 即使 Manager 被攻击，攻击者只能看到任务列表，无法获取凭证

| 维度 | OpenClaw 原生 | HiClaw |
|------|--------------|--------|
| 凭证持有 | 每个 Agent 自己持有 | Worker 只持有 Consumer Token |
| 泄漏途径 | Agent 可直接输出凭证 | Manager 无法访问真实凭证 |
| 攻击面 | 每个 Agent 都是入口 | 只有 Manager 需要防护 |

---

## 更易用：5 分钟打造一人公司

### Manager 帮你做这些事

| 能力 | 说明 |
|------|------|
| **Worker 生命周期管理** | "帮我创建一个前端 Worker" → 自动完成配置、技能分配 |
| **自动分派任务** | 你说目标，Manager 拆解并分配给合适的 Worker |
| **Heartbeat 自动监工** | 定期检查 Worker 状态，发现卡住自动提醒你 |
| **项目群自动拉起** | 为项目创建 Matrix Room，邀请相关人员 |

### 真实案例：一人开发一个 Todo App

**以前（用原生 OpenClaw）**：

```
1. 手动配置前端 Agent 的 skills
2. 手动配置后端 Agent 的 skills
3. 手动分配任务："Alice 你做前端"
4. 手动检查进度："Alice 你做完了吗？"
5. 手动同步："Alice 做完了，Bob 可以开始了"
6. ...一直盯着，生怕烂尾
```

**现在（用 HiClaw）**：

```
你: "我要做一个 Todo App，前端用 React，后端用 Node.js"

Manager: 好的，我来安排
  1. 创建 Worker Alice（前端专家）
  2. 创建 Worker Bob（后端专家）
  3. 创建项目群（你 + Manager + Alice + Bob）
  4. 拆解任务：前端页面 → API 设计 → 数据库 → 联调
  5. 分配给 Alice 和 Bob
  6. [Heartbeat] 定期检查进度，卡住提醒你

[30 分钟后]

Manager: @你 Alice 完成了前端页面，Bob 完成了 API，项目已完成
         请在项目群里 Review 结果

你: [打开手机 Matrix 客户端] 看到进度，满意 ✅
```

**你只需要做决策，不需要当保姆。**

### 移动端体验

HiClaw 内置 Matrix 服务器，支持多种客户端：

- **一键安装后直接用**：无需配置飞书/钉钉机器人
- **手机上随时指挥**：下载 Matrix 客户端，推荐 **FluffyChat**（轻量、国区可下载）
- **消息实时推送**：不会折叠到"服务号"
- **所有对话可见**：你、Manager、Worker 在同一个 Room，全程透明

> 💡 **移动端推荐**：国内用户推荐使用 **FluffyChat**，Element 在国区 iOS 暂未开放下载。FluffyChat 同样支持 iOS/Android/Web 全平台，体验流畅。

---

## 5 分钟快速开始

### 第一步：安装（1 分钟）

```bash
# 一键安装
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)

# 只需要提供一个 LLM API Key
HICLAW_LLM_API_KEY=sk-xxx bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

安装完成后，你会看到：

```
=== HiClaw Manager Started! ===

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  ★ Open the following URL in your browser to start:                           ★
                                                                                
    http://matrix-client-local.hiclaw.io:18080/#/login
                                                                                
  Login with:                                                                   
    Username: admin
    Password: [自动生成的密码]
                                                                                
  After login, start chatting with the Manager!                                 
    Tell it: "Create a Worker named alice for frontend dev"                     
    The Manager will handle everything automatically.                           
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

> 💡 **无需配置 hosts**：我们已经为你配好了 DNS 解析，`*-local.hiclaw.io` 会自动解析到 `127.0.0.1`，开箱即用！

### 第二步：打开浏览器，登录开始对话（1 分钟）

1. 打开浏览器访问安装时显示的 URL（如 `http://matrix-client-local.hiclaw.io:18080`）
2. 输入安装时显示的用户名和密码登录
3. 你会看到一个 "Manager" 的对话

### 第三步：创建你的第一个 Worker（1 分钟）

```
你: 帮我创建一个 Worker，名字叫 alice，负责前端开发

Manager: 好的，正在创建...
         Worker alice 已创建，Room: !xxx:matrix-local.hiclaw.io
         你可以在 "Worker: Alice" Room 里直接给 alice 分配任务
```

### 第四步：分配任务（1 分钟）

```
你: @alice 请帮我实现一个简单的登录页面，使用 React

Alice: 好的，我正在处理...
       [5分钟后] 完成了！代码已提交到 GitHub，PR 链接: https://github.com/xxx/pull/1
```

### 第五步：在手机上查看进度（1 分钟）

1. 下载 **FluffyChat**（iOS/Android/全平台）
2. 登录时选择"其他服务器"，填入你的 Matrix 服务器地址
3. 随时查看 Worker 的进度，随时干预

---

## 一人公司实战：3 个 Worker 干完一个团队的活

假设你是一个独立开发者，想做一个 SaaS 产品。你可以这样配置：

```
你: 帮我创建 3 个 Worker：
    - alice: 前端开发
    - bob: 后端开发
    - charlie: 测试

Manager: 好的，已创建。

你: 这个 Sprint 的目标是：实现用户注册和登录功能

Manager: 收到，我来拆解任务...
         [拆解完成]
         - 任务 1: 设计注册/登录 API（分配给 bob）
         - 任务 2: 实现登录页面（分配给 alice，依赖任务 1）
         - 任务 3: 实现注册页面（分配给 alice，依赖任务 2）
         - 任务 4: 编写测试用例（分配给 charlie，依赖任务 1-3）

[2 小时后]

Manager: @你 Sprint 完成！
         - bob: API 设计完成
         - alice: 登录页面、注册页面完成
         - charlie: 测试用例编写完成，全部通过
         请在项目群里 Review
```

**你一个人，带着 3 个 AI 员工，干完了一个团队的活。**

---

## 开源地址

- **GitHub**: https://github.com/higress-group/hiclaw
- **文档**: https://github.com/higress-group/hiclaw/tree/main/docs
- **社区**: 加入我们的 Discord / 钉钉群 / 微信群

---

## 写在最后

HiClaw 是对 OpenClaw 的一次"超进化"——不是推翻，而是增强。

我们保留了 OpenClaw 的核心理念（自然语言对话、Skills 生态、MCP 工具），同时解决了安全和易用性上的痛点。

如果你是：
- **独立开发者**：一个人想干一个团队的活
- **OpenClaw 深度用户**：想要更安全、更易用的体验
- **一人公司创始人**：需要 AI 员工帮你分担工作

HiClaw 就是为你准备的。

**5 分钟，打造你的一人公司。现在就开始：**

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

---

*HiClaw 是开源项目，基于 Apache 2.0 协议。如果你觉得有用，欢迎 Star ⭐ 和贡献代码！*
