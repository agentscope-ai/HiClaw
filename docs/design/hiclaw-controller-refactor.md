# HiClaw Controller 重构与 K8s 部署设计方案

## 1. 背景与目标

### 1.1 现状

当前 HiClaw 的核心组件全部运行在一个 Manager 容器内（embedded 模式）：

```
Manager Container (单体)
├── hiclaw-controller (kine + embedded kube-apiserver + reconciler)
├── Tuwunel (Matrix Server)
├── MinIO (对象存储)
├── Higress (AI Gateway)
├── Element Web (IM UI)
├── Manager Agent (OpenClaw/CoPaw)
└── docker-proxy (容器管理代理)
```

存在以下问题：

1. hiclaw-controller 嵌入在 Manager 容器内，无法独立升级和扩展
2. Reconciler 依赖 bash 脚本（create-worker.sh 等），脚本内部直接操作 Docker API、Matrix API、Higress API，逻辑分散且难以测试
3. docker-proxy 作为独立容器仅支持 Docker 后端，PR #451 的 orchestrator 方案需要统一到 controller 中
4. 不支持 incluster 模式（K8s 原生部署）
5. Manager Agent 承担了过多职责（集群初始化、配置升级、Worker 生命周期管理等），导致其成为必选组件
6. 缺乏 K8s 下的 debug 手段

### 1.2 目标

1. hiclaw-controller 剥离为独立容器，统一承担资源 reconcile、容器生命周期管理、集群编排职责
2. Manager Agent 变为可选部署，仅保留自然语言交互和跨 Team 任务派发能力
3. 支持 K8s 原生部署（incluster 模式），通过 Helm 安装/升级
4. 实现平滑升级机制，skill/配置热更新不重启 Worker
5. 提供 K8s 下的自助 debug 能力（DebugWorker CRD）

## 2. 整体架构

### 2.1 目标架构（K8s incluster 模式）

```
┌─────────────────────────────────────────────────────────────────────┐
│  K8s Cluster                                                        │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ Deployment: hiclaw-controller                                │   │
│  │  - CRD Reconciler (Worker/Team/Human/Manager/DebugWorker)    │   │
│  │  - Worker Backend 抽象层 (K8s/Docker/Cloud)                  │   │
│  │  - 集群初始化 & 编排引擎                                      │   │
│  │  - 配置版本管理 & 热更新                                      │   │
│  │  - HTTP API Server (:8090)                                   │   │
│  │  - hiclaw CLI (内置)                                         │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌──────────────────┐  ┌──────────────────┐  ┌─────────────────┐   │
│  │ Deploy: Tuwunel  │  │ Deploy: MinIO    │  │ Deploy: Higress │   │
│  │ (Matrix Server)  │  │ (对象存储/OSS)   │  │ (AI Gateway)    │   │
│  └──────────────────┘  └──────────────────┘  └─────────────────┘   │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ Deployment: manager-agent (可选)                              │   │
│  │  - 自然语言创建 Worker/Team (通过 hiclaw CLI)                 │   │
│  │  - 跨 Team 任务派发                                          │   │
│  │  - 无状态，配置从 OSS 拉取                                    │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐               │
│  │ Pod: Worker A │ │ Pod: Worker B │ │ Pod: Leader  │  ...         │
│  │ (无状态)      │ │ (无状态)      │ │ (无状态)     │               │
│  └──────────────┘ └──────────────┘ └──────────────┘               │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ Pod: DebugWorker (按需创建，Team 级别)                        │   │
│  │  - 导出 Matrix 消息 & LLM 日志                                │   │
│  │  - 内置 hiclaw 源码，可结合代码分析问题                        │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 目标架构（embedded 模式，向后兼容）

```
hiclaw-controller Container (独立容器，合并原 docker-proxy)
├── hiclaw-controller 主进程
│   - CRD Reconciler (同 incluster)
│   - Docker Backend (直接管理容器，替代 docker-proxy)
│   - 集群初始化 & 编排引擎
│   - 配置版本管理 & 热更新
│   - HTTP API Server (:8090)
│   - Embedded kube-apiserver + kine (SQLite)
│   - File Watcher (MinIO → kine 同步)
└── hiclaw CLI (内置)

Manager Container (精简，仅基础设施 + 可选 Agent)
├── Tuwunel / MinIO / Higress / Element Web (基础设施)
└── Manager Agent (可选，默认启动)

Worker Containers (不变)
└── Worker Agent (OpenClaw/CoPaw)
```

embedded 模式下 hiclaw-controller 作为独立容器运行（合并原 docker-proxy 的职责），通过 Docker Socket 直接管理 Worker 容器。逻辑与 incluster 完全一致，只是 Worker Backend 使用 Docker 而非 K8s，且额外运行 embedded kube-apiserver + kine 提供 K8s API 兼容层。

## 3. hiclaw-controller 重构

### 3.1 职责划分

hiclaw-controller 统一承担以下职责：

| 职责 | 当前实现 | 重构后 |
|------|---------|--------|
| CRD Reconcile | controller + bash 脚本 | 纯 Go 实现，不依赖脚本 |
| Worker 容器管理 | docker-proxy / orchestrator (PR #451) | 内置 WorkerBackend 抽象层 |
| Matrix 账号管理 | create-worker.sh 内的 curl 调用 | Go MatrixClient |
| Higress 配置管理 | bash 脚本 + HigressClient | Go HigressClient (已有，扩展) |
| 集群初始化 | manager/scripts/init/*.sh | Go Initializer |
| 配置版本升级 | upgrade-builtins.sh | Go ConfigVersionManager |
| Manager 生命周期 | 无（Manager 是必选的） | Manager CRD Reconciler |
| Debug Worker | 无 | DebugWorker CRD Reconciler |

### 3.2 Worker Backend 抽象层

统一 PR #451 的 orchestrator 设计到 controller 内部：

```go
// WorkerBackend 定义 Worker 生命周期管理的统一接口
type WorkerBackend interface {
    // Create 创建一个 Worker 实例（容器/Pod/云实例）
    Create(ctx context.Context, req CreateWorkerRequest) (*WorkerInstance, error)
    // Delete 删除 Worker 实例
    Delete(ctx context.Context, name string) error
    // Status 查询 Worker 实例状态
    Status(ctx context.Context, name string) (*WorkerInstanceStatus, error)
    // Exec 在 Worker 实例中执行命令
    Exec(ctx context.Context, name string, cmd []string) (string, error)
    // Logs 获取 Worker 日志
    Logs(ctx context.Context, name string, opts LogOptions) (io.ReadCloser, error)
    // NeedsCredentialInjection 是否需要注入凭证（云端部署需要）
    NeedsCredentialInjection() bool
}

type CreateWorkerRequest struct {
    Name        string
    Image       string
    Runtime     string            // openclaw | copaw
    Env         map[string]string // 环境变量
    Labels      map[string]string
    Ports       []PortMapping
    Resources   ResourceRequirements // CPU/Memory limits
    NetworkName string               // Docker network / K8s namespace
}

type WorkerInstance struct {
    Name      string
    ID        string // container ID / pod UID
    IP        string
    State     string
    CreatedAt time.Time
}
```

实现层：

| Backend | 适用场景 | 实现方式 |
|---------|---------|---------|
| DockerBackend | embedded 模式 | Docker SDK (替代 docker-proxy) |
| K8sBackend | incluster 模式 | client-go 创建 Pod/Deployment |
| ACKBackend | 阿里云 ACK 部署 | 复用 K8sBackend + 云特性（如 ECI 弹性实例） |

### 3.3 纯 Go Reconciler（去脚本化）

将所有 bash 脚本逻辑重写为 Go：

```go
// WorkerReconciler 重构后的核心流程
func (r *WorkerReconciler) handleCreate(ctx context.Context, w *Worker) error {
    // 1. Matrix 账号注册
    matrixUser, err := r.Matrix.RegisterUser(ctx, w.Name)

    // 2. Matrix Room 创建（3-party: Admin + Manager/Leader + Worker）
    roomID, err := r.Matrix.CreateRoom(ctx, CreateRoomRequest{
        Name:    fmt.Sprintf("worker-%s", w.Name),
        Invite:  []string{adminUserID, managerOrLeaderID, matrixUser.UserID},
    })

    // 3. Higress Consumer 创建 + MCP Server 授权
    consumerKey, err := r.Higress.EnsureConsumer(ctx, w.Name)
    for _, mcp := range w.Spec.McpServers {
        r.Higress.AuthorizeConsumer(ctx, w.Name, mcp)
    }

    // 4. AI Gateway Route 配置
    r.Higress.EnsureAIRoute(ctx, w.Name, w.Spec.Model)

    // 5. 配置生成 & 推送到 OSS
    config := r.generateWorkerConfig(w, matrixUser, consumerKey, roomID)
    r.OSS.PutObject(ctx, agentConfigPath(w.Name), config)

    // 6. Skills 推送到 OSS
    r.pushSkills(ctx, w.Name, w.Spec.Skills)

    // 7. 创建 Worker 实例（通过 Backend 抽象层）
    instance, err := r.Backend.Create(ctx, CreateWorkerRequest{
        Name:    workerContainerName(w.Name),
        Image:   r.resolveImage(w),
        Runtime: w.Spec.Runtime,
        Env:     r.buildWorkerEnv(w, consumerKey),
    })

    // 8. 更新 Status
    w.Status.Phase = "Running"
    w.Status.MatrixUserID = matrixUser.UserID
    w.Status.RoomID = roomID
    return r.Status().Update(ctx, w)
}
```

### 3.4 Go 服务客户端

替代 bash 脚本中的 curl/mc 调用：

```go
// MatrixClient - Tuwunel Matrix Server 操作
type MatrixClient struct {
    BaseURL           string
    RegistrationToken string
    AdminToken        string
}
func (c *MatrixClient) RegisterUser(ctx, name) (*MatrixUser, error)
func (c *MatrixClient) CreateRoom(ctx, req) (string, error)
func (c *MatrixClient) InviteUser(ctx, roomID, userID) error
func (c *MatrixClient) SetPowerLevel(ctx, roomID, userID, level) error
func (c *MatrixClient) SendMessage(ctx, roomID, body) error

// OSSClient - 基于 mc (MinIO Client) 的 S3 API 统一操作
// 底层通过 mc CLI 对接 MinIO 或阿里云 OSS（S3 兼容模式）
type OSSClient struct {
    MCBinary string // mc 二进制路径
    Alias    string // mc alias 名称（如 "hiclaw"）
    Prefix   string // 存储前缀（如 "hiclaw/hiclaw-storage"）
}
func (c *OSSClient) PutObject(ctx, key, data) error        // mc cp
func (c *OSSClient) GetObject(ctx, key) ([]byte, error)    // mc cat
func (c *OSSClient) ListObjects(ctx, prefix) ([]string, error) // mc ls
func (c *OSSClient) DeleteObject(ctx, key) error           // mc rm
func (c *OSSClient) CopyPrefix(ctx, src, dst) error        // mc cp --recursive
func (c *OSSClient) Mirror(ctx, srcPrefix, dstDir) error   // mc mirror
```

mc alias 在 controller 启动时配置，embedded 模式指向本地 MinIO，incluster 模式可指向阿里云 OSS（S3 兼容端点）。统一使用 S3 API，无需区分 MinIO 和 OSS 的差异。

### 3.5 集群初始化引擎

将 `manager/scripts/init/*.sh` 的逻辑统一到 controller：

```go
type Initializer struct {
    Matrix  *MatrixClient
    OSS     *OSSClient
    Higress *HigressClient
    Backend WorkerBackend
}

// Initialize 执行集群首次初始化
func (i *Initializer) Initialize(ctx context.Context, cfg ClusterConfig) error {
    // 1. 等待基础设施就绪（Matrix/MinIO/Higress）
    i.waitForInfrastructure(ctx)

    // 2. 注册 Admin Matrix 账号
    i.Matrix.RegisterUser(ctx, cfg.AdminUser)

    // 3. 配置 Higress 基础路由（Matrix/MinIO/Element Web）
    i.Higress.SetupBaseRoutes(ctx)

    // 4. 初始化 OSS 目录结构
    i.OSS.EnsureBucketStructure(ctx)

    // 5. 推送内置 Skills 到 OSS
    i.pushBuiltinSkills(ctx)

    // 6. 如果配置了 Manager CRD，创建 Manager Agent
    // （K8s 模式下通过 Manager CRD 触发，embedded 模式下默认创建）
}
```

### 3.6 配置版本管理

```go
type ConfigVersionManager struct {
    OSS     *OSSClient
    Backend WorkerBackend
}

// 配置版本存储在 OSS: hiclaw-storage/system/versions.json
type VersionManifest struct {
    BuiltinSkillsVersion string            `json:"builtinSkillsVersion"`
    WorkerConfigVersion  string            `json:"workerConfigVersion"`
    PerWorkerVersions    map[string]string `json:"perWorkerVersions"`
}

// Upgrade 执行配置热更新（不重启 Worker）
func (m *ConfigVersionManager) UpgradeSkills(ctx context.Context, targetVersion string) error {
    // 1. 获取所有 Running 状态的 Worker
    // 2. 对比每个 Worker 的当前 skill 版本
    // 3. 推送新版 skill 到 Worker 的 OSS 空间
    // 4. 通知 Worker file-sync（通过 Matrix @mention 或 OSS 信号文件）
    // 5. 更新 versions.json
    // 注意：不重启 Worker，Worker 通过 file-sync 拉取新配置
}

// UpgradeRuntime 执行引擎升级（需要重启 Worker）
func (m *ConfigVersionManager) UpgradeRuntime(ctx context.Context, newImage string) error {
    // 1. 逐个 Worker 执行滚动更新
    // 2. 创建新实例 → 等待就绪 → 删除旧实例
    // 3. 或者直接更新 Worker CRD 的 image 字段触发 reconcile
}
```

### 3.7 项目结构（重构后）

```
hiclaw-controller/
├── cmd/
│   ├── controller/main.go          # hiclaw-controller 主进程
│   └── hiclaw/main.go              # hiclaw CLI
├── api/v1beta1/
│   ├── types.go                    # 扩展：新增 Manager, DebugWorker CRD
│   └── register.go
├── internal/
│   ├── controller/
│   │   ├── worker_controller.go    # 纯 Go 实现，不依赖脚本
│   │   ├── team_controller.go
│   │   ├── human_controller.go
│   │   ├── manager_controller.go   # 新增：Manager CRD reconciler
│   │   ├── debugworker_controller.go # 新增：DebugWorker CRD reconciler
│   │   ├── expose.go              # Higress 端口暴露
│   │   └── higress_client.go      # Higress API 客户端
│   ├── backend/                    # 新增：Worker 生命周期管理抽象层
│   │   ├── interface.go           # WorkerBackend 接口定义
│   │   ├── docker.go              # Docker 后端（替代 docker-proxy）
│   │   ├── kubernetes.go          # K8s 后端（incluster / ACK）
│   │   └── factory.go             # 根据环境自动选择后端
│   ├── matrix/                     # 新增：Matrix API 客户端
│   │   └── client.go
│   ├── oss/                        # 新增：OSS/MinIO 统一客户端
│   │   └── client.go
│   ├── orchestrator/               # 新增：集群编排引擎
│   │   ├── initializer.go         # 集群初始化
│   │   └── version_manager.go     # 配置版本管理 & 热更新
│   ├── apiserver/embedded.go       # 保留：embedded 模式
│   ├── store/kine.go               # 保留：embedded 模式
│   ├── watcher/file_watcher.go     # 保留：embedded 模式
│   ├── server/http.go              # HTTP API
│   └── mail/smtp.go
├── config/
│   ├── crd/                        # CRD 定义（扩展）
│   ├── helm/                       # 新增：Helm Chart
│   └── rbac/                       # 新增：RBAC 配置
└── Dockerfile
```

## 4. Manager Agent 改造

### 4.1 职责分离

Manager Agent 当前承担的所有职责及其去向：

| 当前职责 | 去向 | 说明 |
|---------|------|------|
| 集群初始化 | hiclaw-controller Initializer | 启动时自动执行 |
| Worker/Team/Human 创建 | hiclaw-controller Reconciler | CRD 驱动，不依赖 Manager |
| 内置 Skill 版本升级 | hiclaw-controller ConfigVersionManager | OSS 热更新 |
| Worker 容器生命周期 | hiclaw-controller WorkerBackend | 统一抽象层 |
| Higress Consumer/Route 管理 | hiclaw-controller HigressClient | Reconciler 内部调用 |
| Matrix 账号注册 | hiclaw-controller MatrixClient | Reconciler 内部调用 |
| 自然语言创建 Worker | Manager Agent (保留) | 通过 hiclaw CLI 下发 |
| 跨 Team 任务派发 | Manager Agent (保留) | Manager 核心价值 |
| Heartbeat 健康检查 | Manager Agent (保留) + Team Leader (新增) | Controller 暂不做存活检查（后续单独设计）；Team Leader 承担团队内 Worker 的存活检查/唤醒/睡眠 |
| 通知 Admin | Manager Agent (保留) | 需要自然语言能力 |

### 4.2 Manager Agent 保留的职责

重构后 Manager Agent 仅保留需要 LLM 能力的职责：

1. 自然语言交互：接收 Admin 的自然语言指令，转化为 hiclaw CLI 命令
2. 跨 Team 任务派发：理解任务语义，选择合适的 Team/Worker
3. 高级 Heartbeat：语义级别的任务进度检查和异常处理
4. Admin 通知：将系统事件转化为人类可读的通知

### 4.3 Manager Agent 的 Skill 改造

所有资源管理类 skill 的脚本统一改为调用 hiclaw CLI：

```bash
# 旧方式：create-worker.sh 内部直接操作 Matrix/Higress/Docker
bash ./skills/worker-management/scripts/create-worker.sh --name alice --model qwen3.5-plus

# 新方式：通过 hiclaw CLI 下发声明式配置
hiclaw apply worker --name alice --model qwen3.5-plus --skills github-operations
```

hiclaw CLI 的双模式支持：

| 模式 | 行为 |
|------|------|
| incluster | 直接通过 client-go 创建/更新 K8s CRD |
| embedded | 写入 MinIO hiclaw-config/ → file watcher → kine → reconcile |

这样 Manager Agent 的 skill 脚本无需关心底层环境差异。

### 4.4 Manager CRD

K8s 模式下通过 Manager CRD 声明式管理 Manager Agent 的部署：

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Manager
metadata:
  name: default
spec:
  model: qwen3.5-plus              # Manager 使用的 LLM 模型
  runtime: openclaw                 # openclaw | copaw
  image: hiclaw/manager-agent:v1.1.0
  replicas: 1                       # Manager 始终单副本
  soul: |                           # 可选：自定义 Manager 人设
    You are the HiClaw Manager...
  config:
    heartbeatInterval: 15m          # Heartbeat 间隔
    workerIdleTimeout: 720m         # Worker 空闲超时
    notifyChannel: admin-dm         # 通知渠道
status:
  phase: Running
  matrixUserID: "@hiclaw-manager:domain"
  version: "v1.1.0"
```

embedded 模式下，install 脚本默认创建 Manager，用户无需手动配置。

### 4.5 K8s 下 Manager Agent 容器的变化

K8s 部署时，Manager Agent 容器变为完全无状态：

```
Manager Pod (K8s)
├── 仅包含 Agent Runtime (OpenClaw/CoPaw)
├── 配置从 OSS 拉取（SOUL.md, AGENTS.md, skills/）
├── state.json 持久化到 OSS（不依赖本地磁盘）
└── 通过 hiclaw CLI 与 controller 交互
```

install 脚本安装时（embedded 模式），仍支持挂载工作目录到宿主机：

```
Manager Container (embedded)
├── Agent Runtime
├── 工作目录挂载到 ~/hiclaw-manager（宿主机）
└── 通过 hiclaw CLI 与内嵌 controller 交互
```

## 5. Team Leader 能力增强

### 5.1 当前 Team Leader 能力评估

当前 Team Leader 的能力：
- 接收 Manager 委派的任务，分解为子任务
- 通过 @mention 在 Team Room 分配任务给 Worker
- 通过 manage-team-state.sh 跟踪任务状态
- 向 Manager 汇报任务完成情况

不足之处：
- 无法动态创建/销毁 Team 内 Worker（需要 Manager 介入）
- 无法调整 Worker 配置（模型切换、skill 增减）
- 无 quota 限制机制
- 没有 Heartbeat 机制，无法自主检测 Worker 存活状态
- 无法自主管理 Worker 的睡眠/唤醒

### 5.2 Team Leader Heartbeat 机制

参考 CoPaw Manager Agent 的 Heartbeat 实现，为 Team Leader 新增 Heartbeat 能力：

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Team
metadata:
  name: alpha-team
spec:
  leader:
    name: alpha-lead
    model: claude-sonnet-4-6
    heartbeat:                           # 新增：Leader Heartbeat 配置
      enabled: true
      every: 30m                         # 检查间隔（默认 30 分钟）
      activeHours:                       # 可选：活跃时间窗口
        start: "08:00"
        end: "22:00"
    permissions:
      canScaleWorkers: true
      maxWorkers: 10
      canSwitchModel: true
      allowedModels:
        - qwen3.5-plus
        - claude-sonnet-4-6
      maxConcurrentTasks: 20
      workerIdleTimeout: 720m            # Worker 空闲超时（默认 12 小时）
  workers:
    - name: alpha-dev
      model: qwen3.5-plus
```

Team Leader 的 HEARTBEAT.md 检查清单：

```markdown
## Team Leader Heartbeat Checklist

### Step 1: Check Active Tasks
- Read team-state.json, check all active finite tasks
- For tasks with no progress in 30+ minutes, @mention assigned worker in Team Room

### Step 2: Worker Lifecycle Check
- Run `hiclaw worker status --team alpha-team` to get all team workers' status
- For each worker:
  - If has active tasks but container stopped → wake up (ensure-ready)
  - If no active tasks and no cron jobs → mark idle_since if not set
  - If idle_since > workerIdleTimeout → stop container (sleep)
  - If container running but no heartbeat response in 60min → flag for admin

### Step 3: Report to Manager (if applicable)
- If any worker is unresponsive or failed, @mention Manager in Leader Room
- If all tasks complete, report summary to Manager
```

### 5.3 Worker 生命周期管理（权限隔离）

Team Leader 通过 hiclaw CLI 管理本 Team 内 Worker 的生命周期，controller 端强制执行权限隔离：

```bash
# Team Leader 查看本 Team Worker 状态
hiclaw worker status --team alpha-team

# Team Leader 唤醒 Worker（仅限本 Team）
hiclaw worker wake --name alpha-dev --team alpha-team

# Team Leader 休眠 Worker（仅限本 Team）
hiclaw worker sleep --name alpha-dev --team alpha-team

# Team Leader 创建临时 Worker（受 maxWorkers 限制）
hiclaw apply worker --name alpha-temp-1 --model qwen3.5-plus \
  --team alpha-team --ephemeral

# Team Leader 切换 Worker 模型（受 allowedModels 限制）
hiclaw apply worker --name alpha-dev --model claude-sonnet-4-6
```

权限隔离设计：

```go
// hiclaw CLI 在执行 worker 操作时，注入调用者身份
// Team Leader 容器内的 hiclaw CLI 通过环境变量获取身份：
//   HICLAW_CALLER_IDENTITY=team-leader
//   HICLAW_CALLER_TEAM=alpha-team
//   HICLAW_CALLER_NAME=alpha-lead

// Controller 端权限检查
func (r *WorkerReconciler) checkCallerPermission(ctx context.Context, w *Worker, caller CallerIdentity) error {
    // Admin 和 Manager 可以操作任何 Worker
    if caller.Identity == "admin" || caller.Identity == "manager" {
        return nil
    }

    // Team Leader 只能操作本 Team 的 Worker
    if caller.Identity == "team-leader" {
        workerTeam := w.Annotations["hiclaw.io/team"]
        if workerTeam != caller.Team {
            return fmt.Errorf("team leader %s cannot manage worker %s: belongs to team %s, not %s",
                caller.Name, w.Name, workerTeam, caller.Team)
        }
        return nil
    }

    // Worker 不能操作其他 Worker
    return fmt.Errorf("caller %s has no permission to manage workers", caller.Name)
}
```

Worker 生命周期状态机：

```
                    ┌─────────┐
          create    │ Pending │
         ────────>  └────┬────┘
                         │ reconcile success
                         v
                    ┌─────────┐  idle timeout   ┌──────────┐
                    │ Running │ ──────────────> │ Sleeping │
                    └────┬────┘                 └────┬─────┘
                         │                           │ wake (task assigned / manual)
                         │                           v
                         │                      ┌─────────┐
                         │                      │ Running │
                         │                      └─────────┘
                         │ update spec
                         v
                    ┌──────────┐
                    │ Updating │ ──> Running
                    └──────────┘
```

Team Leader 的 lifecycle-worker skill 脚本（通过 hiclaw CLI 实现）：

| 操作 | CLI 命令 | Controller 行为 |
|------|---------|----------------|
| 查看状态 | `hiclaw worker status --team T` | 列出 Team 内所有 Worker 的 phase + containerState |
| 唤醒 | `hiclaw worker wake --name W --team T` | Backend.Start(W)，设置 phase=Running |
| 休眠 | `hiclaw worker sleep --name W --team T` | Backend.Stop(W)，设置 phase=Sleeping |
| 确保就绪 | `hiclaw worker ensure-ready --name W --team T` | 如果 Sleeping 则 Start，等待就绪后返回 |
| 空闲检查 | `hiclaw worker check-idle --team T` | 返回每个 Worker 的 idle_since 和剩余超时时间 |

### 5.4 Quota 执行机制

hiclaw-controller 在 WorkerReconciler 中执行 quota 检查：

```go
func (r *WorkerReconciler) checkTeamQuota(ctx context.Context, w *Worker) error {
    teamName := w.Annotations["hiclaw.io/team"]
    if teamName == "" {
        return nil // 非 Team Worker，不检查
    }

    // 获取 Team CRD
    var team Team
    r.Get(ctx, types.NamespacedName{Name: teamName, Namespace: w.Namespace}, &team)

    // 检查 Worker 数量
    maxWorkers := team.Spec.Leader.Permissions.MaxWorkers
    if maxWorkers > 0 {
        currentCount := countTeamWorkers(ctx, r.Client, teamName)
        if currentCount >= maxWorkers {
            return fmt.Errorf("team %s worker quota exceeded: %d/%d", teamName, currentCount, maxWorkers)
        }
    }

    // 检查模型白名单
    allowedModels := team.Spec.Leader.Permissions.AllowedModels
    if len(allowedModels) > 0 && !contains(allowedModels, w.Spec.Model) {
        return fmt.Errorf("model %s not allowed for team %s", w.Spec.Model, teamName)
    }

    return nil
}
```

## 6. 新增 CRD 定义

### 6.1 Manager CRD

```go
type Manager struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec              ManagerSpec   `json:"spec"`
    Status            ManagerStatus `json:"status,omitempty"`
}

type ManagerSpec struct {
    Model              string            `json:"model"`
    Runtime            string            `json:"runtime,omitempty"`
    Image              string            `json:"image,omitempty"`
    Soul               string            `json:"soul,omitempty"`
    Agents             string            `json:"agents,omitempty"`
    Config             ManagerConfig     `json:"config,omitempty"`
}

type ManagerConfig struct {
    HeartbeatInterval  string `json:"heartbeatInterval,omitempty"`  // default: 15m
    WorkerIdleTimeout  string `json:"workerIdleTimeout,omitempty"`  // default: 720m
    NotifyChannel      string `json:"notifyChannel,omitempty"`
}

type ManagerStatus struct {
    Phase        string `json:"phase,omitempty"`
    MatrixUserID string `json:"matrixUserID,omitempty"`
    RoomID       string `json:"roomID,omitempty"`
    Version      string `json:"version,omitempty"`
    Message      string `json:"message,omitempty"`
}
```

### 6.2 DebugWorker CRD

```yaml
apiVersion: hiclaw.io/v1beta1
kind: DebugWorker
metadata:
  name: debug-alpha-team
spec:
  target:                            # 调试目标
    type: team                       # team | worker
    name: alpha-team                 # Team 或 Worker 名称
  hiclawVersion: v1.1.0             # 内置的 hiclaw 代码版本（用于代码分析）
  retention: 72h                     # 自动清理时间（0 表示不自动清理）
  accessControl:
    allowedUsers:                    # 允许与 DebugWorker 对话的用户
      - admin                        # 默认只有 admin
status:
  phase: Running
  matrixUserID: "@debug-alpha-team:domain"
  roomID: "!debug-room:domain"
  mountedWorkspaces:                 # 实时挂载的工作目录
    - worker: alpha-lead
      ossPath: "hiclaw/hiclaw-storage/agents/alpha-lead/"
    - worker: alpha-dev
      ossPath: "hiclaw/hiclaw-storage/agents/alpha-dev/"
  message: "Ready for debugging"
```

```go
type DebugWorker struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec              DebugWorkerSpec   `json:"spec"`
    Status            DebugWorkerStatus `json:"status,omitempty"`
}

type DebugWorkerSpec struct {
    Target         DebugTarget        `json:"target"`
    HiclawVersion  string             `json:"hiclawVersion,omitempty"`
    Retention      string             `json:"retention,omitempty"`      // default: 72h, "0" = no auto-cleanup
    AccessControl  DebugAccessControl `json:"accessControl,omitempty"`
}

type DebugTarget struct {
    Type string `json:"type"` // team | worker
    Name string `json:"name"`
}

type DebugAccessControl struct {
    AllowedUsers []string `json:"allowedUsers,omitempty"`
}

type DebugWorkerStatus struct {
    Phase             string              `json:"phase,omitempty"`
    MatrixUserID      string              `json:"matrixUserID,omitempty"`
    RoomID            string              `json:"roomID,omitempty"`
    MountedWorkspaces []MountedWorkspace  `json:"mountedWorkspaces,omitempty"`
    Message           string              `json:"message,omitempty"`
}

type MountedWorkspace struct {
    Worker  string `json:"worker"`
    OSSPath string `json:"ossPath"`
}
```

### 6.3 DebugWorker 核心设计

DebugWorker 的核心能力是实时访问调试目标的所有成员工作目录，并通过内置的 debug skill 生成调试日志、结合源码分析问题。

工作目录实时挂载：

```
DebugWorker 容器内的目录结构：

/root/debug/
├── workspaces/                      # 实时同步的目标成员工作目录（通过 mc mirror）
│   ├── alpha-lead/                  # Team Leader 的完整工作目录
│   │   ├── SOUL.md
│   │   ├── AGENTS.md
│   │   ├── team-state.json
│   │   ├── skills/
│   │   ├── sessions/                # LLM 请求/响应日志
│   │   └── memory/
│   ├── alpha-dev/                   # Worker 的完整工作目录
│   │   ├── SOUL.md
│   │   ├── openclaw.json
│   │   ├── skills/
│   │   ├── sessions/
│   │   └── memory/
│   └── alpha-qa/
│       └── ...
├── matrix-export/                   # Matrix 消息导出（按需生成）
│   ├── team-room.json
│   ├── alpha-lead-room.json
│   └── alpha-dev-room.json
├── hiclaw-source/                   # hiclaw 指定版本的源码
│   ├── manager/
│   ├── hiclaw-controller/
│   └── ...
└── output/                          # debug skill 生成的分析报告
    └── debug-report-20260403.md
```

### 6.4 DebugWorker 内置 Debug Skill

DebugWorker 自带一个专门的 `debug-analysis` skill，用于生成调试日志并结合代码分析：

```markdown
---
name: debug-analysis
description: Use when you need to generate debug logs, export Matrix messages,
  analyze LLM session logs, or investigate issues by cross-referencing with hiclaw source code.
---

# Debug Analysis Skill

## Available Commands

### Export Matrix Messages
Export recent Matrix room messages for a specific worker or the team room.


bash ./skills/debug-analysis/scripts/export-matrix-messages.sh \
  --worker alpha-dev \
  --hours 24 \
  --output /root/debug/matrix-export/alpha-dev-room.json


### Generate Debug Log
Aggregate session logs, Matrix messages, and state files into a structured debug report.

bash ./skills/debug-analysis/scripts/generate-debug-log.sh \
  --worker alpha-dev \
  --hours 24 \
  --include-sessions \
  --include-matrix \
  --include-state \
  --output /root/debug/output/debug-report.md

### Analyze with Source Code
The hiclaw source code is available at `/root/debug/hiclaw-source/`.
When investigating issues, cross-reference:
- Agent behavior rules: `manager/agent/*/AGENTS.md`
- Skill implementations: `manager/agent/skills/*/`
- Controller reconcile logic: `hiclaw-controller/internal/controller/`
- Worker config generation: `hiclaw-controller/internal/executor/`

## Workspace Access
All target workers' workspaces are live-synced at `/root/debug/workspaces/<worker-name>/`.
You can directly read any file to understand current state:
- `sessions/` — LLM request/response logs (JSON)
- `team-state.json` / `state.json` — Task tracking state
- `memory/` — Agent memory files
- `openclaw.json` / `copaw.json` — Runtime configuration
```

### 6.5 DebugWorker Reconciler 逻辑

```go
func (r *DebugWorkerReconciler) handleCreate(ctx context.Context, dw *DebugWorker) error {
    // 1. 解析调试目标，获取所有成员的 OSS 路径和 Matrix 凭证
    members := r.resolveTargetMembers(ctx, dw.Spec.Target)
    // team → leader + all workers
    // worker → single worker

    // 2. 创建 DebugWorker 的 Matrix 账号
    matrixUser, _ := r.Matrix.RegisterUser(ctx, dw.Name)

    // 3. 创建 Debug Room，邀请 allowedUsers
    roomID, _ := r.Matrix.CreateRoom(ctx, CreateRoomRequest{
        Name:   fmt.Sprintf("debug-%s", dw.Name),
        Invite: append(dw.Spec.AccessControl.AllowedUsers, matrixUser.UserID),
    })

    // 4. 准备 mc mirror 配置：为每个目标成员配置实时同步
    mirrorConfigs := []MirrorConfig{}
    mountedWorkspaces := []MountedWorkspace{}
    for _, member := range members {
        mirrorConfigs = append(mirrorConfigs, MirrorConfig{
            Source: member.OSSAgentPath,  // e.g., hiclaw/hiclaw-storage/agents/alpha-dev/
            Dest:   fmt.Sprintf("/root/debug/workspaces/%s/", member.Name),
        })
        mountedWorkspaces = append(mountedWorkspaces, MountedWorkspace{
            Worker:  member.Name,
            OSSPath: member.OSSAgentPath,
        })
    }

    // 5. 创建 DebugWorker 容器
    //    - 内置 hiclaw 指定版本的源码
    //    - 内置 debug-analysis skill
    //    - 配置 mc mirror 实时同步目标工作目录
    //    - 注入目标成员的 Matrix 凭证（用于导出消息）
    instance, _ := r.Backend.Create(ctx, CreateWorkerRequest{
        Name:  debugContainerName(dw.Name),
        Image: fmt.Sprintf("hiclaw/debug-worker:%s", dw.Spec.HiclawVersion),
        Env: map[string]string{
            "DEBUG_TARGET_TYPE":     dw.Spec.Target.Type,
            "DEBUG_TARGET_NAME":     dw.Spec.Target.Name,
            "HICLAW_SOURCE_VERSION": dw.Spec.HiclawVersion,
            "MIRROR_CONFIGS":        encodeMirrorConfigs(mirrorConfigs),
            "TARGET_MATRIX_CREDS":   encodeMatrixCreds(members), // 用于导出消息
        },
    })

    // 6. 更新 Status
    dw.Status.Phase = "Running"
    dw.Status.MatrixUserID = matrixUser.UserID
    dw.Status.RoomID = roomID
    dw.Status.MountedWorkspaces = mountedWorkspaces

    // 7. 设置自动清理定时器
    if dw.Spec.Retention != "" && dw.Spec.Retention != "0" {
        r.scheduleCleanup(ctx, dw)
    }

    return nil
}
```

DebugWorker 容器启动后，内部运行 mc mirror 持续同步目标成员的 OSS 工作目录到本地，用户（admin/team admin）通过 Matrix 与 DebugWorker 对话，DebugWorker 利用 debug-analysis skill 读取实时数据、导出日志、结合源码分析问题。

### 6.6 Team 默认 DebugWorker

每个 Team 可配置默认自带一个 DebugWorker：

```yaml
apiVersion: hiclaw.io/v1beta1
kind: Team
metadata:
  name: alpha-team
spec:
  leader:
    name: alpha-lead
    model: claude-sonnet-4-6
  workers:
    - name: alpha-dev
      model: qwen3.5-plus
  debug:                             # 新增：Team 级 Debug 配置
    enabled: true                    # 默认创建 DebugWorker
    accessControl:
      allowedUsers: [admin]          # 默认只有 Team Admin 有权限
```

TeamReconciler 在创建 Team 时，如果 `spec.debug.enabled=true`，自动创建对应的 DebugWorker CRD。

## 7. K8s 部署与 Helm Chart

### 7.1 组件拆分

K8s 部署模式下，各组件拆分为独立的 Deployment/StatefulSet：

| 组件 | K8s 资源类型 | 副本数 | 持久化 | 说明 |
|------|-------------|--------|--------|------|
| hiclaw-controller | Deployment | 1 (leader election) | 无（无状态） | 核心控制面 |
| tuwunel | StatefulSet | 1 | PVC (SQLite) | Matrix Server |
| minio | StatefulSet | 1 | PVC (数据) | 对象存储（可替换为阿里云 OSS） |
| higress | Deployment | 1-N | ConfigMap | AI Gateway |
| element-web | Deployment | 1 | 无 | IM Web UI（可选） |
| manager-agent | Deployment | 0-1 | 无（OSS） | 可选部署 |
| worker-{name} | Pod (由 controller 管理) | 1 | 无（OSS） | 按需创建 |
| debug-{name} | Pod (由 controller 管理) | 0-1 | 无（OSS） | 按需创建 |

### 7.2 Helm Chart 结构

```
hiclaw/
├── Chart.yaml
├── values.yaml                      # 默认配置
├── values-aliyun.yaml               # 阿里云环境覆盖
├── templates/
│   ├── _helpers.tpl
│   ├── namespace.yaml
│   ├── NOTES.txt
│   │
│   ├── controller/
│   │   ├── deployment.yaml          # hiclaw-controller
│   │   ├── service.yaml             # HTTP API (:8090)
│   │   ├── serviceaccount.yaml
│   │   ├── clusterrole.yaml         # CRD 操作权限
│   │   └── clusterrolebinding.yaml
│   │
│   ├── crds/
│   │   ├── workers.hiclaw.io.yaml
│   │   ├── teams.hiclaw.io.yaml
│   │   ├── humans.hiclaw.io.yaml
│   │   ├── managers.hiclaw.io.yaml
│   │   └── debugworkers.hiclaw.io.yaml
│   │
│   ├── tuwunel/
│   │   ├── statefulset.yaml
│   │   ├── service.yaml
│   │   └── pvc.yaml
│   │
│   ├── minio/
│   │   ├── statefulset.yaml         # 可选，externalOSS 时不部署
│   │   ├── service.yaml
│   │   └── pvc.yaml
│   │
│   ├── higress/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   └── configmap.yaml
│   │
│   ├── element-web/
│   │   ├── deployment.yaml          # 可选
│   │   └── service.yaml
│   │
│   ├── ingress.yaml                 # 统一入口（可选）
│   └── configmap.yaml               # 全局配置
│
└── tests/
    └── test-connection.yaml
```

### 7.3 values.yaml 核心配置

```yaml
global:
  imageRegistry: higress-registry.cn-hangzhou.cr.aliyuncs.com
  imageTag: "v1.1.0"
  namespace: hiclaw
  domain: hiclaw.example.com         # 集群域名

llm:
  provider: alibaba-cloud            # alibaba-cloud | openai-compat
  model: qwen3.5-plus
  apiKey: ""                          # 必填，或引用 Secret
  apiKeySecret:
    name: hiclaw-llm-secret
    key: api-key

admin:
  username: admin
  password: ""                        # 首次安装必填
  email: ""

controller:
  image:
    repository: hiclaw/hiclaw-controller
    tag: ""                           # 默认使用 global.imageTag
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi
  workerBackend: kubernetes           # kubernetes | docker

tuwunel:
  enabled: true
  image:
    repository: hiclaw/tuwunel
  storage:
    size: 10Gi
    storageClass: ""                  # 使用默认 StorageClass

minio:
  enabled: true                       # false 时使用 externalOSS
  image:
    repository: minio/minio
  storage:
    size: 50Gi
    storageClass: ""

externalOSS:                          # minio.enabled=false 时使用
  endpoint: ""
  bucket: ""
  accessKey: ""
  secretKey: ""

higress:
  enabled: true
  image:
    repository: higress-registry.cn-hangzhou.cr.aliyuncs.com/higress/all-in-one

elementWeb:
  enabled: true
  image:
    repository: vectorim/element-web

manager:
  enabled: false                      # K8s 下默认不部署，需要时通过 Manager CRD 创建
  image:
    repository: hiclaw/manager-agent
  model: qwen3.5-plus
  runtime: openclaw

worker:
  defaultImage:
    openclaw: hiclaw/worker-agent
    copaw: hiclaw/copaw-worker
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: "2"
      memory: 2Gi

debug:
  image:
    repository: hiclaw/debug-worker
  defaultRetention: 72h
```

### 7.4 RBAC 配置

hiclaw-controller 需要的 K8s 权限：

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hiclaw-controller
rules:
  # CRD 操作
  - apiGroups: ["hiclaw.io"]
    resources: ["workers", "teams", "humans", "managers", "debugworkers"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["hiclaw.io"]
    resources: ["workers/status", "teams/status", "humans/status", "managers/status", "debugworkers/status"]
    verbs: ["get", "update", "patch"]
  # Pod 管理（K8sBackend 创建 Worker Pod）
  - apiGroups: [""]
    resources: ["pods", "pods/log", "pods/exec"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
  # Service 管理（Worker 端口暴露）
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
  # ConfigMap/Secret 读取
  - apiGroups: [""]
    resources: ["configmaps", "secrets"]
    verbs: ["get", "list", "watch"]
  # Leader Election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update"]
```

## 8. 平滑升级机制

### 8.1 升级分类

| 升级类型 | 影响范围 | 是否需要重启 | 频率 |
|---------|---------|-------------|------|
| Skill/配置更新 | Worker/Manager 的 skill 和配置文件 | 不需要 | 高频（常态化） |
| Controller 升级 | hiclaw-controller 自身 | 仅 controller Pod | 中频 |
| 基础设施升级 | Tuwunel/MinIO/Higress | 对应组件 | 低频 |
| Runtime 引擎升级 | OpenClaw/CoPaw 版本 | Worker/Manager Pod | 低频 |

### 8.2 Skill/配置热更新（零停机）

这是最常见的升级场景，必须做到零停机：

```
升级流程：
1. 新版 skill/配置推送到 OSS
   hiclaw-storage/system/skills/v1.2.0/
   ├── worker-skills/
   │   ├── file-sync/
   │   ├── task-progress/
   │   └── ...
   └── manager-skills/
       ├── worker-management/
       └── ...

2. hiclaw-controller 检测到新版本
   - 对比 system/versions.json 中的 builtinSkillsVersion
   - 逐个 Worker 推送新 skill 到其 OSS 空间
   - 写入信号文件 agents/{name}/.skill-update-signal

3. Worker 感知更新
   - Worker 的 file-sync 定时拉取（每 5 分钟）
   - 或者通过 Matrix @mention 触发立即 file-sync
   - Worker 加载新 skill，无需重启进程

4. 更新 versions.json
   - 记录每个 Worker 的当前 skill 版本
   - 全部更新完成后更新 builtinSkillsVersion
```

关键设计：
- OSS 作为配置分发通道，Worker 通过 file-sync 拉取，天然支持热更新
- 信号文件机制避免依赖 Matrix 消息的可靠性
- 逐个 Worker 更新，失败不影响其他 Worker
- versions.json 记录每个 Worker 的版本，支持部分更新和回滚

### 8.3 Controller 作为基础设施升级

hiclaw-controller 本身也是基础设施的一部分，支持通过 Helm 整体升级。升级时 controller 会将新版本的配置和内置技能推送到 OSS：

```bash
# Helm 升级（所有基础设施组件，含 controller）
helm upgrade hiclaw ./hiclaw --set global.imageTag=v1.2.0

# 仅升级 controller
helm upgrade hiclaw ./hiclaw \
  --set controller.image.tag=v1.2.0 \
  --reuse-values
```

Controller 升级支持两种模式（通过 Helm values 或 CLI 参数控制）：

| 模式 | 行为 | 适用场景 |
|------|------|---------|
| 仅推送配置 (默认) | 升级 controller 镜像 + 将新版配置/内置技能推送到 OSS 的 system/ 目录 | 常规升级，Worker 通过 file-sync 按需拉取 |
| 推送并更新全量 | 推送到 OSS 后，同时更新所有 Worker/Team 工作目录下的配置和技能 | 紧急修复或需要立即生效的变更 |

```bash
# 模式 1：仅推送配置到 OSS（默认）
# Controller 启动后自动将内置的最新配置/技能推送到 OSS system/ 目录
# Worker 在下次 file-sync 时拉取

# 模式 2：推送并更新全量
hiclaw config push --apply-to-all
# 将 OSS system/ 下的最新配置同步到每个 Worker/Team 的工作目录
# 并通过 Matrix @mention 触发各 Worker 立即 file-sync
```

Controller 升级期间的影响：
- 短暂的 reconcile 中断（Pod 重启期间，通常 < 30s）
- 已运行的 Worker/Manager 不受影响（它们独立运行）
- 新的 CRD 变更会在 controller 重启后自动 reconcile
- Leader Election 确保同一时刻只有一个 controller 实例

### 8.4 Runtime 引擎升级（需要重启）

Team 和 Worker 是 runtime 的最小升级单元。每个 Worker CRD 和 Team CRD 都有独立的 `image` 字段，支持独立控制升级：

```yaml
# 独立 Worker 的 image 字段
apiVersion: hiclaw.io/v1beta1
kind: Worker
metadata:
  name: alice
spec:
  model: qwen3.5-plus
  runtime: openclaw
  image: hiclaw/worker-agent:v1.1.0    # 独立控制此 Worker 的镜像版本

---
# Team 的 image 字段（控制 Leader + 所有 Workers）
apiVersion: hiclaw.io/v1beta1
kind: Team
metadata:
  name: alpha-team
spec:
  image: hiclaw/worker-agent:v1.2.0     # Team 级别默认镜像，Leader 和所有 Workers 共用
  leader:
    name: alpha-lead
    model: claude-sonnet-4-6
    image: ""                            # 空则使用 Team 级别 image
  workers:
    - name: alpha-dev
      model: qwen3.5-plus
      image: ""                          # 空则使用 Team 级别 image
    - name: alpha-qa
      model: qwen3.5-plus
      image: hiclaw/worker-agent:v1.1.0  # 可单独指定，覆盖 Team 级别
```

升级操作示例：

```bash
# 升级单个独立 Worker 的镜像
hiclaw apply worker --name alice --image hiclaw/worker-agent:v1.2.0

# 升级整个 Team（Leader + 所有 Workers）
hiclaw apply team --name alpha-team --image hiclaw/worker-agent:v1.2.0

# 升级 Team 中某个特定 Worker
hiclaw apply worker --name alpha-qa --team alpha-team --image hiclaw/worker-agent:v1.2.0
```

滚动升级流程：

```
1. WorkerReconciler 检测到 image 变化
   - 设置 phase = "Updating"
   - 通知 Worker 保存当前状态到 OSS（通过 Matrix @mention）
   - 等待 Worker 确认状态已保存（或超时 60s）

2. 执行滚动替换
   - 创建新版本 Worker 实例
   - 等待新实例就绪（health check 通过）
   - 删除旧实例
   - 设置 phase = "Running"

3. Team 批量升级
   - TeamReconciler 检测到 Team 级 image 变化
   - 逐个更新 Leader 和 Workers（先 Workers 后 Leader）
   - 可配置并发度（默认 1，即逐个升级）
```

### 8.5 单独更新 Worker/Team 配置和技能

支持单独更新某个 Worker 或 Team 工作目录下的配置和技能到 OSS 中 controller 存放的最新版本：

```bash
# 更新单个 Worker 的配置和技能到最新版本
hiclaw config push --worker alice
# 将 OSS system/ 下的最新配置/技能同步到 agents/alice/ 的工作目录
# 通过 Matrix @mention 触发 alice 立即 file-sync

# 更新整个 Team 的配置和技能
hiclaw config push --team alpha-team
# 同步到 Leader + 所有 Workers 的工作目录
# 逐个通过 Matrix @mention 触发 file-sync

# 更新 Team 中某个特定 Worker
hiclaw config push --worker alpha-dev --team alpha-team
```

流程：

```
1. hiclaw config push --worker alice
   ↓
2. Controller 从 OSS system/skills/{latest}/ 读取最新配置
   ↓
3. 推送到 OSS agents/alice/skills/ 和 agents/alice/config/
   ↓
4. 通过 Matrix @mention alice: "配置已更新，请执行 file-sync"
   ↓
5. Worker alice 执行 file-sync，拉取新配置，无需重启
```

### 8.6 基础设施升级

Tuwunel/MinIO/Higress 作为独立的 StatefulSet/Deployment，通过 Helm 升级：

```bash
# 全量升级（所有组件，含 controller）
helm upgrade hiclaw ./hiclaw --set global.imageTag=v1.2.0

# 仅升级 Higress
helm upgrade hiclaw ./hiclaw --set higress.image.tag=v1.2.0 --reuse-values
```

基础设施升级注意事项：
- Tuwunel 升级：短暂的 Matrix 消息中断，Worker 有重连机制，影响可控
- MinIO 升级：短暂的文件同步中断，Worker 本地有缓存，影响可控
- Higress 升级：短暂的 API Gateway 中断，影响 Worker 的 LLM 调用和 MCP 工具

### 8.7 升级编排（Helm Hooks）— 待讨论

> 以下 Helm Hooks 方案作为待讨论项，不是 MVP 必须实现的。初期可以通过 hiclaw CLI 手动执行 pre/post upgrade 步骤。

```yaml
# pre-upgrade hook: 通知所有 Worker 保存状态（待讨论）
apiVersion: batch/v1
kind: Job
metadata:
  name: hiclaw-pre-upgrade
  annotations:
    "helm.sh/hook": pre-upgrade
spec:
  template:
    spec:
      containers:
        - name: pre-upgrade
          image: hiclaw/hiclaw-controller:{{ .Values.controller.image.tag }}
          command: ["hiclaw", "config", "push", "--apply-to-all"]
      restartPolicy: Never
```

### 8.7 版本兼容性矩阵

Controller 需要维护与 Worker/Manager 的版本兼容性：

```go
// 版本兼容性检查
type CompatibilityMatrix struct {
    ControllerVersion string
    MinWorkerVersion  string   // 支持的最低 Worker 版本
    MinManagerVersion string   // 支持的最低 Manager 版本
    SkillsVersion     string   // 当前内置 Skills 版本
}

// Controller 启动时检查
func (c *CompatibilityMatrix) CheckWorker(workerVersion string) error {
    if semver.Compare(workerVersion, c.MinWorkerVersion) < 0 {
        return fmt.Errorf("worker version %s < minimum %s, upgrade required",
            workerVersion, c.MinWorkerVersion)
    }
    return nil
}
```

## 9. hiclaw CLI incluster 模式完善

### 9.1 双模式统一

```go
// CLI 根据环境自动选择后端
func newResourceClient() ResourceClient {
    mode := os.Getenv("HICLAW_KUBE_MODE")
    if mode == "" {
        // 自动检测：如果在 K8s Pod 内，使用 incluster
        if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
            mode = "incluster"
        } else {
            mode = "embedded"
        }
    }

    switch mode {
    case "incluster":
        return &K8sResourceClient{
            client: getK8sClient(),
        }
    default:
        return &MinIOResourceClient{
            mcBinary: "mc",
            alias:    "hiclaw",
        }
    }
}

// ResourceClient 统一接口
type ResourceClient interface {
    Apply(ctx context.Context, resources []Resource) error
    Get(ctx context.Context, kind, name string) (*Resource, error)
    List(ctx context.Context, kind string) ([]Resource, error)
    Delete(ctx context.Context, kind, name string) error
}
```

### 9.2 K8sResourceClient 实现

```go
type K8sResourceClient struct {
    client client.Client
}

func (c *K8sResourceClient) Apply(ctx context.Context, resources []Resource) error {
    for _, res := range resources {
        existing := res.DeepCopy()
        err := c.client.Get(ctx, client.ObjectKeyFromObject(existing), existing)
        if err != nil {
            // 不存在，创建
            return c.client.Create(ctx, &res)
        }
        // 已存在，更新 spec
        existing.SetSpec(res.GetSpec())
        return c.client.Update(ctx, existing)
    }
    return nil
}

func (c *K8sResourceClient) Get(ctx context.Context, kind, name string) (*Resource, error) {
    obj := newObjectByKind(kind)
    err := c.client.Get(ctx, types.NamespacedName{
        Name:      name,
        Namespace: "default",
    }, obj)
    return obj, err
}

func (c *K8sResourceClient) List(ctx context.Context, kind string) ([]Resource, error) {
    list := newListByKind(kind)
    err := c.client.List(ctx, list)
    return list.GetItems(), err
}

func (c *K8sResourceClient) Delete(ctx context.Context, kind, name string) error {
    obj := newObjectByKind(kind)
    obj.SetName(name)
    obj.SetNamespace("default")
    return c.client.Delete(ctx, obj)
}
```

### 9.3 CLI 新增命令

```bash
# 资源管理（已有，扩展 incluster 支持）
hiclaw apply -f resource.yaml          # 创建/更新资源
hiclaw apply worker --name alice --model qwen3.5-plus  # 命令式创建
hiclaw apply team --name alpha-team --image hiclaw/worker-agent:v1.2.0  # 指定镜像
hiclaw get workers|teams|humans        # 查看资源
hiclaw delete worker alice             # 删除资源

# Runtime 镜像升级（per-worker / per-team）
hiclaw apply worker --name alice --image hiclaw/worker-agent:v1.2.0
  # 升级独立 Worker 的基础镜像（触发滚动替换）
hiclaw apply team --name alpha-team --image hiclaw/worker-agent:v1.2.0
  # 升级整个 Team 的基础镜像（Leader + 所有 Workers）
hiclaw apply worker --name alpha-qa --team alpha-team --image hiclaw/worker-agent:v1.2.0
  # 升级 Team 中某个特定 Worker 的镜像

# 配置/技能推送（从 OSS system/ 同步到 Worker 工作目录）
hiclaw config push --worker alice
  # 更新 alice 工作目录下的配置和技能到 controller 存放的最新版本
  # 通过 Matrix @mention 触发 alice 立即 file-sync
hiclaw config push --team alpha-team
  # 更新整个 Team（Leader + Workers）的配置和技能
hiclaw config push --apply-to-all
  # 更新所有 Worker 和 Team 的配置和技能

# Worker 生命周期管理（Team Leader 也可使用，受权限隔离）
hiclaw worker status --team alpha-team  # 查看 Team 内 Worker 状态
hiclaw worker wake --name alice         # 唤醒 Worker
hiclaw worker sleep --name alice        # 休眠 Worker
hiclaw worker ensure-ready --name alice # 确保 Worker 就绪
hiclaw worker check-idle --team alpha-team  # 检查空闲状态

# Debug 相关
hiclaw debug create --target team/alpha-team  # 创建 DebugWorker
hiclaw debug create --target worker/alice     # 针对单个 Worker
hiclaw debug list                             # 列出所有 DebugWorker
hiclaw debug delete --name debug-alpha-team   # 删除 DebugWorker

# 状态查看
hiclaw status                       # 集群整体状态
hiclaw status workers               # 所有 Worker 状态
hiclaw status teams                 # 所有 Team 状态
hiclaw version                      # 各组件版本信息
```

## 10. 实施计划

### Phase 1: Controller 核心重构（去脚本化）

目标：将 Reconciler 从依赖 bash 脚本改为纯 Go 实现

1. 实现 Go 服务客户端
   - `internal/matrix/client.go` — Matrix API（注册用户、创建 Room、邀请、发消息）
   - `internal/oss/client.go` — 基于 mc 的 S3 API 统一客户端（对接 MinIO / 阿里云 OSS）
   - 扩展 `internal/controller/higress_client.go` — Consumer 管理、AI Route 配置

2. 实现 WorkerBackend 抽象层
   - `internal/backend/interface.go` — 接口定义
   - `internal/backend/docker.go` — Docker 后端（替代 docker-proxy，使用 Docker SDK）
   - `internal/backend/kubernetes.go` — K8s 后端（incluster / ACK）
   - `internal/backend/factory.go` — 自动选择

3. 重写 Reconciler
   - WorkerReconciler: 纯 Go 创建流程（Matrix 注册 → Higress 配置 → OSS 推送 → Backend 创建）
   - TeamReconciler: 纯 Go 创建流程（Leader 创建 → Worker 创建 → Team Room → 权限配置）
   - HumanReconciler: 纯 Go 创建流程（Matrix 注册 → 权限配置 → Room 邀请 → 邮件通知）

4. 集群初始化引擎
   - `internal/orchestrator/initializer.go` — 替代 manager/scripts/init/*.sh

5. 配置版本管理
   - `internal/orchestrator/version_manager.go` — Skill 热更新 + Runtime 滚动升级
   - `hiclaw config push` 命令实现

### Phase 2: incluster 模式 & Helm

目标：支持 K8s 原生部署

1. K8sBackend 实现
   - Worker Pod 模板生成
   - Pod 健康检查 & 就绪探针
   - Service 创建（端口暴露）

2. hiclaw CLI incluster 模式
   - K8sResourceClient 实现
   - 自动检测运行环境
   - 新增 worker lifecycle / config push / debug / status 命令

3. Helm Chart
   - Chart 结构 & templates
   - values.yaml 默认配置
   - CRD 安装（含 Manager、DebugWorker）
   - RBAC 配置

4. 新增 CRD
   - Manager CRD + ManagerReconciler
   - DebugWorker CRD + DebugWorkerReconciler（实时工作目录挂载 + debug-analysis skill）

### Phase 3: Manager Agent 改造 & Team Leader 增强

目标：Manager 变为可选部署，Team Leader 承担团队内 Worker 生命周期管理

1. Manager Skill 改造
   - 所有资源管理 skill 改为调用 hiclaw CLI
   - 移除直接操作 Docker/Matrix/Higress 的脚本

2. Manager 无状态化
   - state.json 持久化到 OSS
   - 配置从 OSS 拉取
   - K8s 下通过 Manager CRD 管理

3. Team Leader Heartbeat 机制
   - 参考 CoPaw Manager 的 Heartbeat 实现
   - 配置化的检查间隔和活跃时间窗口
   - Worker 存活检查 / 空闲检测 / 自动睡眠唤醒

4. Team Leader Worker 生命周期管理
   - Leader permissions 配置（canScaleWorkers / maxWorkers / allowedModels）
   - Quota 检查机制（Controller 端强制执行）
   - 权限隔离（CallerIdentity，Leader 只能管理本 Team Worker）
   - Leader 内置 hiclaw CLI（注入 HICLAW_CALLER_* 环境变量）

### Phase 4: Debug 能力 & 平滑升级

目标：完善运维能力

1. DebugWorker 实现
   - debug-worker 镜像构建（内置源码 + debug-analysis skill）
   - mc mirror 实时同步目标成员工作目录
   - Matrix 消息导出 + Session 日志分析
   - Team 默认 DebugWorker（spec.debug.enabled）

2. 平滑升级机制
   - Skill/配置热更新流程（OSS 推送 + Matrix 通知 file-sync）
   - Per-Worker / Per-Team 独立镜像升级（滚动替换）
   - `hiclaw config push` 单独更新配置/技能
   - 版本兼容性检查

3. 可观测性
   - Controller metrics（Prometheus）
   - Worker 健康检查（基础存活 + 语义级）
   - 升级进度追踪

## 11. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 去脚本化过程中遗漏逻辑 | Worker 创建失败 | 逐个脚本对照重写，保留脚本作为 fallback，灰度切换 |
| K8s 网络模型差异 | Worker 间通信异常 | K8sBackend 创建 Service，Higress 路由指向 Service 而非容器 IP |
| OSS 热更新延迟 | Skill 版本不一致 | Matrix @mention 触发即时 file-sync + versions.json 记录每个 Worker 版本 |
| Manager 职责分离不干净 | 功能缺失或重复 | 明确的职责矩阵（第 4.1 节），逐步迁移而非一次性切换 |
| Team Leader 权限隔离失效 | 跨 Team 操作 | Controller 端强制 CallerIdentity 检查，Leader CLI 注入身份环境变量 |
| Team Leader quota 绕过 | 资源超限 | Controller 端强制检查，Leader 的 hiclaw CLI 无法绕过 |
| DebugWorker 数据安全 | 敏感信息泄露 | accessControl 严格限制，retention 自动清理，仅 admin/team admin 可访问 |
| embedded 模式 controller 容器拆分 | 部署复杂度增加 | Docker Compose 编排，install 脚本自动处理容器间网络和依赖 |
