# HiClaw Controller 重构进度跟踪

> 基于 `hiclaw-controller-refactor.md` 设计文档，对照 `hiclaw-controller-refactor` 分支实际实现情况。
>
> 更新时间：2026-04-09

## 总览

| Phase | 目标 | 完成度 | 状态 |
|-------|------|--------|------|
| Phase 1 | Controller 核心重构（去脚本化） | ~85% | 进行中 |
| Phase 2 | incluster 模式 & Helm | ~75% | 进行中 |
| Phase 3 | Manager Agent 改造 & Team Leader 增强 | 0% | 未开始 |
| Phase 4 | Debug 能力 & 平滑升级 | 0% | 未开始 |

---

## Phase 1: Controller 核心重构（去脚本化）

### 1.1 Go 服务客户端

| 项目 | 设计路径 | 实际路径 | 状态 |
|------|---------|---------|------|
| Matrix API 客户端 | `internal/matrix/client.go` | `internal/matrix/client.go` + `types.go` | ✅ 完成 |
| OSS/MinIO 统一客户端 | `internal/oss/client.go` | `internal/oss/client.go` + `types.go` + `minio.go` + `minio_admin.go` | ✅ 完成 |
| Higress/Gateway 客户端 | `internal/controller/higress_client.go`（扩展） | `internal/gateway/client.go` + `higress.go` + `types.go` | ✅ 完成（路径有调整） |

额外实现（设计文档未单独列出）：

| 项目 | 路径 | 说明 |
|------|------|------|
| STS 凭证管理 | `internal/credentials/sts.go` | 云端 STS Token 服务 |
| Agent 配置生成 | `internal/agentconfig/generator.go` | AGENTS.md 合并、MCP 端口配置 |
| 认证鉴权 | `internal/auth/` | K8s ServiceAccount 认证 + RBAC 鉴权 + 中间件 |

### 1.2 WorkerBackend 抽象层

| 项目 | 设计路径 | 实际路径 | 状态 |
|------|---------|---------|------|
| 接口定义 | `internal/backend/interface.go` | `internal/backend/interface.go` | ✅ 完成 |
| Docker 后端 | `internal/backend/docker.go` | `internal/backend/docker.go` | ✅ 完成 |
| K8s 后端 | `internal/backend/kubernetes.go` | `internal/backend/kubernetes.go` | ✅ 完成 |
| 后端自动选择 | `internal/backend/factory.go` | `internal/backend/registry.go` | ✅ 完成（文件名不同） |

额外实现（超出设计文档）：

| 项目 | 路径 | 说明 |
|------|------|------|
| SAE 后端 | `internal/backend/sae.go` | 阿里云 Serverless App Engine |
| APIG 网关后端 | `internal/backend/apig.go` | 阿里云 API Gateway |
| 网关抽象 | `internal/backend/gateway.go` | 网关层统一抽象 |
| 云凭证管理 | `internal/backend/cloud_credentials.go` | 云端凭证注入 |

### 1.3 纯 Go Reconciler

| 项目 | 设计路径 | 实际路径 | 状态 |
|------|---------|---------|------|
| WorkerReconciler | `internal/controller/worker_controller.go` | `internal/controller/worker_controller.go` | ✅ 完成 |
| TeamReconciler | `internal/controller/team_controller.go` | `internal/controller/team_controller.go` | ✅ 完成 |
| HumanReconciler | `internal/controller/human_controller.go` | `internal/controller/human_controller.go` | ✅ 完成 |

### 1.4 集群初始化引擎

| 项目 | 设计路径 | 实际路径 | 状态 |
|------|---------|---------|------|
| Initializer | `internal/orchestrator/initializer.go` | `internal/initializer/initializer.go` | ✅ 完成（路径有调整） |

### 1.5 配置版本管理

| 项目 | 设计路径 | 状态 |
|------|---------|------|
| ConfigVersionManager | `internal/orchestrator/version_manager.go` | ❌ 未实现 |
| versions.json 管理 | OSS `system/versions.json` | ❌ 未实现 |
| Skill 热更新（UpgradeSkills） | — | ❌ 未实现 |
| Runtime 滚动升级（UpgradeRuntime） | — | ❌ 未实现 |
| `hiclaw config push` 命令 | CLI | ❌ 未实现 |

### 1.6 项目结构对比

| 设计文档目录 | 实际情况 |
|-------------|---------|
| `internal/controller/` | ✅ 存在，含 worker/team/human 三个 controller |
| `internal/backend/` | ✅ 存在，且比设计更丰富（多了 SAE/APIG） |
| `internal/matrix/` | ✅ 存在 |
| `internal/oss/` | ✅ 存在 |
| `internal/orchestrator/` | ❌ 不存在，逻辑分散在 app/ 和 service/ |
| `internal/server/http.go` | ✅ 存在，拆分为多个 handler 文件 |
| `internal/apiserver/embedded.go` | ✅ 存在 |
| `internal/store/kine.go` | ✅ 存在 |
| `internal/watcher/file_watcher.go` | ✅ 存在 |
| `internal/mail/smtp.go` | ✅ 存在 |

实际额外新增的目录（设计文档未列出）：

| 目录 | 说明 |
|------|------|
| `internal/agentconfig/` | Agent 配置生成、AGENTS.md 合并、MCP 端口、协调逻辑 |
| `internal/auth/` | 认证（SA Token）、鉴权（RBAC）、中间件 |
| `internal/service/` | Provisioner、Deployer、Credentials、Worker 环境变量 |
| `internal/credentials/` | STS 凭证管理 |
| `internal/proxy/` | 安全代理 |
| `internal/httputil/` | HTTP 响应工具 |
| `internal/config/` | 配置加载 |

---

## Phase 2: incluster 模式 & Helm

### 2.1 K8sBackend

| 项目 | 状态 | 说明 |
|------|------|------|
| K8sBackend 实现 | ✅ 完成 | `internal/backend/kubernetes.go` |
| Worker Pod 模板生成 | ✅ 完成 | `internal/service/deployer.go` |
| Pod 健康检查 & 就绪探针 | ⚠️ 需确认 | 就绪检测在早期 commit 中实现 |
| Service 创建（端口暴露） | ✅ 完成 | `internal/service/provisioner_expose.go` |

### 2.2 hiclaw CLI REST API 客户端改造

CLI 已完全重写为 REST API 客户端，不再直接操作 MinIO。所有命令通过 `HICLAW_CONTROLLER_URL`（默认 `http://localhost:8090`）调用 controller REST API，Token 通过 `HICLAW_AUTH_TOKEN` 或 SA token 文件自动发现。

| 项目 | 状态 | 说明 |
|------|------|------|
| CLI 重写为 REST API 客户端 | ✅ 完成 | `cmd/hiclaw/client.go` — HTTP client + token/URL 发现 |
| create worker/team/human/manager | ✅ 完成 | `cmd/hiclaw/create.go` — POST /api/v1/{resource}s |
| get workers/teams/humans/managers | ✅ 完成 | `cmd/hiclaw/get.go` — GET (list/detail/--team 过滤/-o json) |
| update worker/team/manager | ✅ 完成 | `cmd/hiclaw/update.go` — PUT /api/v1/{resource}s/{name} |
| delete worker/team/human/manager | ✅ 完成 | `cmd/hiclaw/delete.go` — DELETE /api/v1/{resource}s/{name} |
| worker wake/sleep/ensure-ready | ✅ 完成 | `cmd/hiclaw/worker_cmd.go` — POST lifecycle endpoints |
| worker status (--name / --team) | ✅ 完成 | `cmd/hiclaw/worker_cmd.go` — GET status endpoint |
| status / version | ✅ 完成 | `cmd/hiclaw/status_cmd.go` |
| 表格/详情/JSON 输出格式化 | ✅ 完成 | `cmd/hiclaw/output.go` |
| apply -f resource.yaml | ❌ TODO | Phase 3: 声明式 YAML apply（解析 YAML → REST API） |
| apply --prune | ❌ TODO | Phase 3: 全量同步（LIST + DELETE 多余资源） |
| apply worker --zip | ❌ TODO | Phase 3: ZIP 包导入 |
| config push 命令 | ❌ 未实现 | — |
| debug 命令 | ❌ 未实现 | — |

### 2.3 Helm Chart

| 项目 | 状态 | 说明 |
|------|------|------|
| Chart 结构 | ✅ 完成 | `helm/hiclaw/` |
| Controller Deployment | ✅ 完成 | `templates/controller/deployment.yaml` |
| Controller Service | ✅ 完成 | `templates/controller/service.yaml` |
| Controller RBAC | ✅ 完成 | `templates/controller/rbac.yaml` |
| Controller ServiceAccount | ✅ 完成 | `templates/controller/serviceaccount.yaml` |
| Tuwunel StatefulSet | ✅ 完成 | `templates/matrix-server/tuwunel-statefulset.yaml` |
| MinIO StatefulSet | ✅ 完成 | `templates/object-storage/minio-statefulset.yaml` |
| Higress（子 chart） | ✅ 完成 | `charts/higress-2.2.0.tgz` |
| Element Web | ✅ 完成 | `templates/element-web/` |
| Manager Deployment | ✅ 完成 | `templates/manager/deployment.yaml` |
| values.yaml | ✅ 完成 | 含 values-kind.yaml 本地开发配置 |
| Ingress | ❌ 未实现 | 设计文档中有 `ingress.yaml` |

### 2.4 CRD

| CRD | Helm crds/ | api/v1beta1/types.go | Reconciler | 状态 |
|-----|-----------|---------------------|------------|------|
| Worker | ✅ | ✅ | ✅ | 完成 |
| Team | ✅ | ✅ | ✅ | 完成 |
| Human | ✅ | ✅ | ✅ | 完成 |
| Manager | ❌ | ❌ | ❌ | 未实现 |
| DebugWorker | ❌ | ❌ | ❌ | 未实现 |

---

## Phase 3: Manager Agent 改造 & Team Leader 增强

全部未开始。

| 项目 | 状态 |
|------|------|
| Manager Skill 改造（调用 hiclaw CLI 替代直接脚本） | ❌ |
| Manager 无状态化（state.json → OSS） | ❌ |
| Manager CRD 驱动部署 | ❌ |
| Team Leader Heartbeat 机制 | ❌ |
| Team Leader Worker 生命周期管理 | ❌ |
| Leader permissions 配置 | ❌ |
| Quota 检查机制 | ❌ |
| CallerIdentity 权限隔离 | ❌ |

---

## Phase 4: Debug 能力 & 平滑升级

全部未开始。

| 项目 | 状态 |
|------|------|
| DebugWorker CRD 定义 | ❌ |
| DebugWorkerReconciler | ❌ |
| debug-analysis skill | ❌ |
| 工作目录实时挂载（mc mirror） | ❌ |
| Matrix 消息导出 | ❌ |
| Skill/配置热更新（零停机） | ❌ |
| Controller 升级机制 | ❌ |
| Runtime 滚动升级 | ❌ |
| 版本兼容性矩阵 | ❌ |
| Helm Hooks 升级编排 | ❌ |

---

## 关键 Commit 时间线

| Commit | 里程碑 |
|--------|--------|
| `ae39750` | 起点：将 docker-proxy 重构为统一 Worker 生命周期管理（orchestrator） |
| `a45b662` | 新增 SAE 后端、APIG 网关、认证、STS Token |
| `821cbf0` | 抽象 Backend Provider 层 |
| `453910d` ~ `d3a4334` | 将 orchestrator 重命名为 controller |
| `931f33e` ~ `72d2778` | 初始 Helm Chart + Kind 本地 K8s 环境 |
| `30223b3` | 新增 CRD 定义和 controller 配置 |
| `f15b231` ~ `a340ab2` | Agent 配置生成和合并功能 |
| `2b0bb85` ~ `f535136` | HTTP Server 重构、API 错误处理、RBAC |
| `f30e870` ~ `fd1b9b3` | K8s ServiceAccount 认证鉴权 |
| `bbb4ae3` | Tuwunel/MinIO 改为 StatefulSet |
| `53a28ad` | local-k8s-up.sh 更新 + Worker 管理增强 |
| `3c17fe1` ~ `9a0bf49` | Manager CRD + ManagerReconciler + Initializer（Go） |
| (pending) | hiclaw CLI 重写为 REST API 客户端（去 mcExec，纯 HTTP client） |

---
