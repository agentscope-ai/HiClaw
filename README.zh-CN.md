# HiClaw

[English](./README.md) | [中文](./README.zh-CN.md)

**开源 Agent 团队系统，基于 IM 协议实现多 Agent 协作，支持人工全程监督介入。**

HiClaw 让你部署一支 AI Agent 团队，通过即时通讯（Matrix 协议）进行协作，借助集中式文件系统协调任务，管理员可随时观察和干预所有 Agent 的行为。

## 核心特性

- **Agent 团队**：Manager Agent 协调多个 Worker Agent 完成复杂任务
- **人工监督**：所有 Agent 通信均发生在 Matrix 房间中，人类可随时观察并介入
- **多渠道管理**：管理员可通过 Discord、飞书、Telegram 等 OpenClaw 支持的渠道联系 Manager；Manager 能识别管理员身份，并将日常通知路由到其首选渠道
- **编程 CLI 委托**：当编程 CLI 工具（Claude Code、Gemini CLI）可用时，Worker 可将编程任务委托给它——Manager 在任务工作区运行 CLI 并实时回传结果，实现超越标准 LLM 调用的代码生成能力
- **AI 网关**：通过 Higress 统一管理 LLM 和 MCP Server 访问，支持按 Worker 独立管理凭证
- **无状态 Worker**：Worker 从集中存储加载所有配置，可随时销毁和重建
- **MCP 集成**：通过 MCP Server 访问外部工具（GitHub 等），凭证集中管理
- **完全开源**：基于 Higress、Tuwunel、MinIO、OpenClaw 和 Element Web 构建

## 快速开始

完整步骤请参阅 **[docs/quickstart.md](docs/quickstart.md)**。

### 前置条件

- **Docker Desktop**（Windows/macOS）或 **Docker Engine**（Linux）已安装并运行
- **PowerShell 7+**（仅 Windows）
- LLM API Key（如通义千问、OpenAI 等）
- （可选）GitHub Personal Access Token，用于 GitHub 协作功能

### 安装

#### macOS / Linux

```bash
# 一键安装（交互式）
bash <(curl -fsSL https://higress.ai/hiclaw/install.sh)

# 或克隆后安装
git clone https://github.com/higress-group/hiclaw.git && cd hiclaw
HICLAW_LLM_API_KEY="sk-xxx" make install
```

#### Windows（PowerShell 7+）

```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://higress.ai/hiclaw/install.ps1'))
```

安装脚本会自动：
1. **检测时区**（支持 Linux 和 macOS），选择最优镜像源
2. **交互式配置**（LLM 提供商、API Key、端口等）——也可通过环境变量预设
3. **等待 Manager 就绪**后退出
4. **发送欢迎消息**，Manager 会用你可能使用的语言问候你

#### 安装选项

**macOS / Linux:**

```bash
# 非交互模式（使用全部默认值）
HICLAW_NON_INTERACTIVE=1 HICLAW_LLM_API_KEY="sk-xxx" make install

# 自定义端口
HICLAW_PORT_GATEWAY=8080 HICLAW_PORT_CONSOLE=8001 HICLAW_LLM_API_KEY="sk-xxx" make install

# 指定外部数据目录
HICLAW_DATA_DIR=~/hiclaw-data HICLAW_LLM_API_KEY="sk-xxx" make install

# 预设所有配置
HICLAW_LLM_PROVIDER=qwen \
HICLAW_DEFAULT_MODEL=qwen3.5-plus \
HICLAW_LLM_API_KEY="sk-xxx" \
HICLAW_ADMIN_USER=admin \
HICLAW_ADMIN_PASSWORD=yourpassword \
HICLAW_TIMEZONE=Asia/Shanghai \
make install
```

**Windows（PowerShell）:**

```powershell
# 非交互模式（使用全部默认值）
$env:HICLAW_NON_INTERACTIVE = "1"
$env:HICLAW_LLM_API_KEY = "sk-xxx"
.\hiclaw-install.ps1

# 预设所有配置
$env:HICLAW_LLM_PROVIDER = "qwen"
$env:HICLAW_DEFAULT_MODEL = "qwen3.5-plus"
$env:HICLAW_LLM_API_KEY = "sk-xxx"
$env:HICLAW_ADMIN_USER = "admin"
$env:HICLAW_ADMIN_PASSWORD = "yourpassword"
.\hiclaw-install.ps1
```

#### 升级或重装

在已有安装上再次运行安装脚本时，会提示选择操作：

```
Choose an action:
  1) In-place upgrade (keep data, workspace, env file)
  2) Clean reinstall (remove all data, start fresh)
  3) Cancel
```

- **原地升级**：保留所有数据，仅重建 Manager 容器。如镜像有更新，可选择同步重建 Worker 容器。
- **全量重装**：删除所有内容（Docker 卷、工作区目录、环境文件、Worker 容器），需手动确认工作区路径。

### 安装完成后

1. 打开 Element Web：`http://matrix-client-local.hiclaw.io:<端口>`（默认端口：18080）
2. 使用管理员账号登录
3. Manager 会主动问候并介绍自身能力

也可通过 CLI 发送任务：

```bash
make replay TASK="创建一个名为 alice 的 Worker，负责前端开发，直接创建。"
```

## 架构

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

## 多 Agent 架构：HiClaw vs OpenClaw 原生

HiClaw 基于 [OpenClaw](https://github.com/nicepkg/openclaw) 构建，将其从单进程多 Agent 框架扩展为完整的托管 Agent 团队平台。Manager Agent 借助 Higress AI 网关，通过自然语言对话自动化完成多 Agent 的全生命周期管理——从 Worker 创建、凭证分发，到任务派发、进度监控和技能演进。

### 1. 部署与拓扑

| | OpenClaw 原生 | HiClaw |
|---|---|---|
| **部署方式** | 单进程，所有 Agent 共享一个网关 | 分布式容器，每个 Agent 独立，支持跨机器 |
| **拓扑结构** | 扁平对等，基于渠道路由 | 层级制 Manager + Workers |
| **扩展与隔离** | 垂直扩展；共享进程，一个崩溃影响全部 | 水平扩展；容器级故障隔离 |

### 2. 通信与人工可见性

| | OpenClaw 原生 | HiClaw |
|---|---|---|
| **通信渠道** | 内部消息总线 | Matrix 房间（IM 协议） |
| **人工可见性** | 可选 | **内置**——人类在每个房间中 |
| **Agent 间通信** | 不透明的内部路由 | 所有交互可见、可搜索、可中断 |

每个房间包含人类 + Manager + Worker。人类可随时介入——指导 Worker 改进任务执行（反馈到技能优化），或指导 Manager 改进 Worker 管理策略（完善管理技能）。

### 3. LLM 与 MCP 凭证管理

| | OpenClaw 原生 | HiClaw |
|---|---|---|
| **LLM 访问** | 每个 Agent 持有自己的 API Key | 统一 AI 网关，按 Agent 分配消费者令牌 |
| **工具凭证** | 每个 Agent 持有真实凭证 | 集中在网关——Agent 永远看不到真实凭证 |
| **权限控制** | 按 Agent 配置 | Manager 按 Worker 授予/撤销 MCP Server 访问权限 |
| **凭证更新** | 手动编辑 + 重启 | Manager 更新 MinIO 中的配置，Worker 自动热重载 |

Worker 只持有自己的消费者令牌。即使 Worker 被攻破，也无法获取上游 API 凭证。

### 4. 生命周期与技能演进自动化

| | OpenClaw 原生 | HiClaw |
|---|---|---|
| **Agent 创建** | 手动配置 + 重启 | 对话式：_"创建一个名为 alice 的前端 Worker"_ |
| **监控** | 无跨 Agent 监控 | Manager 在房间中发送心跳（人类可见） |
| **配置更新** | 编辑文件 + 重启 | 热重载，秒级生效 |
| **自我改进** | 无 | Manager 审查表现，持续演进团队技能 |

Manager 自主处理 Worker 的完整生命周期：账号注册、SOUL.md 生成、凭证分发、技能分配、任务派发和心跳监控。两个内置扩展机制驱动持续改进：

- **Worker 经验管理**：按 Worker 记录绩效档案和技能评分，用于智能任务分配。
- **技能演进管理**：跨任务模式识别、新技能草稿生成、人工审核和模拟任务验证。

## 文档

| 文档 | 说明 |
|------|------|
| [docs/quickstart.md](docs/quickstart.md) | 端到端快速入门指南，含验证检查点 |
| [docs/architecture.md](docs/architecture.md) | 系统架构与组件概览 |
| [docs/manager-guide.md](docs/manager-guide.md) | Manager 配置与使用 |
| [docs/worker-guide.md](docs/worker-guide.md) | Worker 部署与故障排查 |
| [docs/development.md](docs/development.md) | 贡献指南与本地开发 |

## 构建与测试

```bash
# 构建所有镜像
make build

# 构建 + 运行全部集成测试（10 个测试用例，自动启用 YOLO 模式）
make test

# 只运行指定测试
make test TEST_FILTER="01 02 03"

# 不重新构建镜像，直接运行测试
make test SKIP_BUILD=1

# 快速冒烟测试（仅 test-01）
make test-quick
```

## 安装 / 卸载 / 任务回放

```bash
# 本地安装 Manager（构建镜像 + 交互式配置）
HICLAW_LLM_API_KEY="sk-xxx" make install

# 不重新构建镜像直接安装
HICLAW_LLM_API_KEY="sk-xxx" SKIP_BUILD=1 make install

# 通过 CLI 向 Manager 发送任务
make replay TASK="创建一个名为 alice 的前端开发 Worker"

# 查看最新回放对话日志
make replay-log

# 对已安装的 Manager 运行测试（不重建、不新建容器）
make test-installed

# 卸载所有内容（Manager + Workers + 卷 + 环境文件）
make uninstall
```

## 推送与发布

```bash
# 推送多架构镜像（amd64 + arm64）到镜像仓库
make push VERSION=0.1.0 REGISTRY=ghcr.io REPO=higress-group/hiclaw

# 清理容器和镜像
make clean

# 查看所有可用目标
make help
```

## 项目结构

```
hiclaw/
├── manager/           # Manager Agent 容器（all-in-one：Higress + Tuwunel + MinIO + Element Web + OpenClaw）
├── worker/            # Worker Agent 容器（轻量级：OpenClaw + mc + mcporter）
├── install/           # 一键安装脚本
├── scripts/           # 工具脚本（replay-task.sh）
├── hack/              # 维护脚本（mirror-images.sh）
├── tests/             # 自动化集成测试（10 个测试用例）
├── .github/workflows/ # CI/CD 流水线
├── docs/              # 用户文档
└── design/            # 内部设计文档
```

详细代码导航请参阅 [AGENTS.md](AGENTS.md)。

## 许可证

Apache License 2.0
