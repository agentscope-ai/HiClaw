# 导入 Worker 指南

将预配置的 Worker 导入 HiClaw —— 支持迁移独立运行的 OpenClaw 实例，也支持导入社区 Worker 模板。

## 概述

Worker 导入系统由两部分组成：

1. **导入脚本** (`hiclaw-import.sh` / `hiclaw-import.ps1`) —— 在 HiClaw 宿主机上运行，接收 Worker 包（ZIP），完成所有注册和配置，然后通知 Manager 启动容器
2. **迁移 Skill** (`migrate/skill/`) —— 在独立运行的 OpenClaw 实例上运行，分析其环境并生成兼容的 Worker 包

Worker 包是一个 ZIP 文件，包含配置文件，可选包含 Dockerfile 用于构建自定义镜像。当不包含 Dockerfile 时，使用标准 HiClaw Worker 镜像。

## Worker 包格式

```
worker-package.zip
├── manifest.json           # 包元数据（必需）
├── Dockerfile              # 自定义镜像构建（可选）
├── config/
│   ├── SOUL.md             # Worker 身份和角色
│   ├── AGENTS.md           # 自定义 Agent 配置
│   ├── MEMORY.md           # 长期记忆
│   └── memory/             # 记忆文件
├── skills/                 # 自定义技能
│   └── <skill-name>/
│       └── SKILL.md
├── crons/
│   └── jobs.json           # 定时任务
└── tool-analysis.json      # 工具依赖报告（仅供参考）
```

### manifest.json

```json
{
  "version": "1.0",
  "source": {
    "openclaw_version": "2026.3.x",
    "hostname": "my-server",
    "os": "Ubuntu 22.04",
    "created_at": "2026-03-18T10:00:00Z"
  },
  "worker": {
    "suggested_name": "my-worker",
    "base_image": "hiclaw/worker-agent:latest",
    "apt_packages": ["ffmpeg", "imagemagick"],
    "pip_packages": [],
    "npm_packages": []
  },
  "proxy": {
    "suggested": false,
    "reason": ""
  }
}
```

## 场景一：迁移独立运行的 OpenClaw

如果你有一个在服务器上独立运行的 OpenClaw 实例，想将其纳入 HiClaw 管理成为一个 Worker，按以下步骤操作。

### 第 1 步：在源 OpenClaw 上安装迁移 Skill

将 `migrate/skill/` 目录复制到 OpenClaw 的 skills 目录：

```bash
cp -r migrate/skill/ ~/.openclaw/workspace/skills/hiclaw-migrate/
```

或者让你的 OpenClaw 安装它：

```
安装 hiclaw-migrate skill，路径在 /path/to/hiclaw/migrate/skill/
```

### 第 2 步：生成迁移包

让你的 OpenClaw 分析当前环境并生成迁移包：

```
分析我当前的配置和环境，生成 HiClaw 迁移包。
```

OpenClaw 会阅读迁移 Skill 的说明，理解 HiClaw 的 Worker 架构，然后：

1. 运行 `analyze.sh` 扫描工具依赖（Skill 脚本、Shell 历史、Cron 任务、AGENTS.md 代码块）
2. 智能适配你的 AGENTS.md —— 保留你的自定义角色和行为定义，移除与 HiClaw 内置 Worker 配置冲突的部分（通信协议、文件同步、任务执行规范等）
3. 适配 SOUL.md 为 HiClaw 的 Worker 身份格式
4. 生成基于 HiClaw Worker 基础镜像的 Dockerfile，包含所需的系统工具
5. 将所有内容打包为 ZIP 并输出文件路径

这一步需要 OpenClaw AI 参与 —— 脚本本身无法智能地适配你的配置。OpenClaw 会阅读 SKILL.md 来理解 HiClaw 的规范，然后对配置内容做出保留、修改或移除的判断。

### 第 3 步：审查包内容（建议）

导入前建议检查生成的文件：

```bash
unzip -l /tmp/hiclaw-migration/migration-my-worker-*.zip
```

查看 `tool-analysis.json` 确认检测到的依赖是否正确。如有需要可以编辑 Dockerfile 增减软件包。

### 第 4 步：传输并导入

将 ZIP 传输到 HiClaw Manager 宿主机，然后运行：

```bash
bash hiclaw-import.sh --zip migration-my-worker-20260318-100000.zip
```

脚本会依次执行：
1. 从 Dockerfile 构建自定义 Worker 镜像
2. 注册 Matrix 账号并创建通信 Room
3. 创建 MinIO 用户并配置权限策略
4. 配置 Higress Gateway Consumer 和路由授权
5. 生成 openclaw.json 并推送所有配置到 MinIO
6. 更新 Manager 的 workers-registry.json
7. 发送消息通知 Manager 启动 Worker 容器

### 第 5 步：验证

脚本完成后，在 Element Web 中查看 Worker 状态。Manager 会启动容器，Worker 应在一分钟内上线。

### 迁移内容对照表

| 内容 | 是否迁移 | 说明 |
|------|----------|------|
| SOUL.md / AGENTS.md | 是 | 适配为 HiClaw 格式 |
| 自定义 Skills | 是 | 放入 `custom-skills/` 目录 |
| Cron 定时任务 | 是 | 转换为 HiClaw 调度任务 |
| 记忆文件 | 是 | MEMORY.md 和每日笔记 |
| 系统工具依赖 | 是 | 通过自定义 Dockerfile 安装 |
| API 密钥 / 认证配置 | 否 | HiClaw 使用自己的 AI Gateway 凭据 |
| 设备身份 | 否 | 注册时生成新身份 |
| 会话记录 | 否 | HiClaw 中会话每日重置 |
| Discord/Slack 渠道配置 | 否 | HiClaw 使用 Matrix |

## 场景二：导入 Worker 模板

Worker 模板是预构建的包，定义了 Worker 的角色、技能和工具依赖。可以在团队内共享或发布到社区。

### 从本地 ZIP 导入

```bash
bash hiclaw-import.sh --zip devops-worker-template.zip --name devops-alice
```

### 从 URL 导入

```bash
bash hiclaw-import.sh --zip https://example.com/templates/devops-worker.zip --name devops-alice
```

### 不含 Dockerfile 的模板

如果模板 ZIP 中不包含 Dockerfile，将使用标准 HiClaw Worker 镜像（`hiclaw/worker-agent`）。适用于只需要内置工具（git、curl、jq、Node.js、Python 等）的 Worker。

```bash
# 无需构建自定义镜像
bash hiclaw-import.sh --zip simple-worker-template.zip --name bob
```

### 创建 Worker 模板

要创建可分享的 Worker 模板：

1. 创建 `manifest.json`：

```json
{
  "version": "1.0",
  "source": {
    "hostname": "template",
    "os": "N/A",
    "created_at": "2026-03-18T00:00:00Z"
  },
  "worker": {
    "suggested_name": "devops-worker",
    "base_image": "hiclaw/worker-agent:latest",
    "apt_packages": [],
    "pip_packages": [],
    "npm_packages": []
  }
}
```

2. 创建 `config/SOUL.md` 定义 Worker 角色：

```markdown
# DevOps Worker

## AI Identity

**You are an AI Agent, not a human.**

## Role
- Name: devops-worker
- 专长: CI/CD 流水线管理、基础设施监控、部署自动化
- 技能: GitHub 操作、Shell 脚本、Docker、Kubernetes

## Behavior
- 主动监控 CI/CD 流水线
- 发现故障立即告警
- 自动化日常部署任务
```

3. 可选添加 `config/AGENTS.md`（自定义指令）、`skills/`（自定义技能）和 `Dockerfile`（额外工具）。

4. 打包：

```bash
cd my-template-dir/
zip -r devops-worker-template.zip manifest.json config/ skills/ Dockerfile
```

## 命令参考

### hiclaw-import.sh（Bash — macOS/Linux）

```bash
bash hiclaw-import.sh --zip <路径或URL> [选项]
```

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `--zip <路径\|URL>` | Worker 包 ZIP（本地路径或 URL） | 必需 |
| `--name <名称>` | Worker 名称 | 从 manifest 读取 |
| `--proxy <URL>` | Worker 运行时 HTTP 代理 | 无 |
| `--no-proxy <域名>` | 额外免代理域名 | 无 |
| `--env-file <路径>` | HiClaw 环境文件路径 | `~/hiclaw-manager.env` |
| `--base-image <镜像>` | 覆盖 Dockerfile 构建的基础镜像 | 从 manifest 读取 |
| `--skip-build` | 跳过 Docker 镜像构建 | 关闭 |
| `--yes` | 跳过交互确认 | 关闭 |

### hiclaw-import.ps1（PowerShell — Windows）

```powershell
.\hiclaw-import.ps1 -Zip <路径或URL> [-Name <名称>] [-Proxy <URL>] [-NoProxy <域名>] [-EnvFile <路径>] [-BaseImage <镜像>] [-SkipBuild] [-Yes]
```

参数与 Bash 版本一致。

## HTTP 代理配置

对于在中国或企业防火墙后运行的 Worker，使用 `--proxy` 配置运行时 HTTP 代理：

```bash
bash hiclaw-import.sh --zip worker.zip --proxy http://192.168.1.100:7890
```

代理通过环境变量（`HTTP_PROXY`、`HTTPS_PROXY`）设置在 Worker 容器中。以下域名自动排除代理（`NO_PROXY`）：

- `*.hiclaw.io`（所有 HiClaw 内部域名）
- `127.0.0.1`、`localhost`
- Manager 的 Matrix、AI Gateway 和 MinIO 域名

使用 `--no-proxy` 添加额外域名：

```bash
bash hiclaw-import.sh --zip worker.zip \
    --proxy http://192.168.1.100:7890 \
    --no-proxy "*.internal.company.com,10.0.0.0/8"
```

注意：代理仅用于 Worker 运行时。镜像建期间，代理通过 Docker build args 传入，在最终镜像中会被清除。

## 故障排查

### 导入脚本在 "检查 Manager 容器" 步骤失败

HiClaw Manager 容器必须处于运行状态：

```bash
docker start hiclaw-manager
```

### 镜像构建失败

检查 ZIP 包中的 Dockerfile。常见问题：
- 软件包名称在不同 Ubuntu 版本间可能不同
- pip/npm 包可能已更名或下架

可以编辑解压后的 Dockerfile 重试，或使用 `--skip-build` 配合预构建的镜像。

### Worker 启动但无响应

1. 查看 Worker 容器日志：`docker logs hiclaw-worker-<name>`
2. 在 Element Web 中确认 Worker 出现在其专属 Room 中
3. 确认 Manager 的 `workers-registry.json` 中有正确的条目
4. 尝试在 Worker 的 Room 中发送 `@<worker-name>:<matrix-domain> hello`
