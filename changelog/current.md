# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

**What's New**

- **Team, Human & Declarative Management (hiclaw-controller)** — Introduced the `hiclaw-controller` binary for declarative resource management. Define Teams, Humans, and Workers as YAML resources and apply them with `hiclaw apply`. Teams group Workers under a leader with shared goals; Humans bind Matrix users to Workers for direct interaction. The controller watches for resource changes and reconciles state automatically. Supports inline `identity`, `soul`, and `agents` fields for Worker configuration, eliminating the need for separate files.

- **MCP Proxy Support** — New `mcp-proxy` feature allows proxying existing MCP servers through the HiClaw gateway, enabling Workers to access external MCP tools without direct network exposure.

- **OpenClaw CMS Plugin Integration** — Manager now integrates `openclaw-cms-plugin` install and runtime wiring, extending the agent's content management capabilities.

- **Docker Network Aliases** — Replaced ExtraHosts IP injection with Docker network aliases, simplifying container networking and improving reliability across restarts.

- **hiclawMode Gateway Config** — Switched from `mergeConsecutiveMessages` to `hiclawMode` in Higress gateway init config, providing a unified HiClaw-specific configuration mode.

- **MiniMax M2.7 Default Model** — Upgraded MiniMax default model to M2.7 for improved performance.

- **Interactive Version Selection** — Install scripts now prompt users to select a specific version during installation.

- **Post-Install Verification** — New verification script runs after installation to confirm all components are healthy.

- **Multi-Phase Collaboration Protocol** — Added multi-phase collaboration protocol to task-lifecycle, improving coordination between Manager and Workers on complex tasks.

- **Template Import Flow** — Added `hiclaw-find-worker` as a dedicated Manager skill so worker creation can follow the template/package-import flow and avoid manually creating workers.

- **Template Package Shorthand** — Added shorthand package syntax with optional version and defaults to simplify worker template imports.

- **Generalized Template Imports** — Expanded the `hiclaw-find-worker` import flow so market and registry template imports behave consistently.

- **Market Worker Import Flow** — Refined market worker import behaviour for cleaner path handling and clearer completion.

**Bug Fixes**

- Fixed stale local declarative config after delete in embedded mode — `start-mc-mirror.sh` now mirrors `hiclaw-config/` with `--remove`, so deleting a resource removes the corresponding local watched YAML instead of leaving stale files under `/root/hiclaw-fs/hiclaw-config/`.

- Fixed `hiclaw apply` silently ignoring all resources — `loadResources()` called `strings.TrimSpace(line)` first then checked `strings.HasPrefix(line, "  name:")` which could never match after trimming. Fixed by checking `strings.HasPrefix(line, "name:")` on the already-trimmed line.

- Fixed stuck `Phase="Pending"` resources after failed package resolution — `r.Status().Update()` could silently fail due to resource version conflict, leaving workers permanently stuck. Fixed by refreshing the object via `r.Get()` before error-path status updates and treating `Phase="Pending"` with non-empty error `Message` as retriable.

- Fixed concurrent route authorization in gateway — added optimistic locking retry to prevent race conditions when multiple Workers register routes simultaneously.

- Fixed cloud worker OSS access security — in cloud mode (Alibaba Cloud SAE), all workers shared the same RRSA role with unrestricted OSS bucket access. Now `oss-credentials.sh` injects an inline STS policy restricting tokens to `agents/{worker}/*` and `shared/*` prefixes only.

- Fixed Docker container escape risk — added Docker API proxy (`hiclaw-docker-proxy`) to restrict container access to the Docker daemon, preventing potential container escape attacks.

- Fixed `create-worker.sh` robustness — added Matrix room deduplication check and failure notification to prevent silent failures when room creation encounters conflicts.

- Fixed `state.json` registration — enforce registration for all task types and add idle-stop safety to prevent Workers from being stopped while tasks are active.

- Fixed Element Web CSP violation — use external JS file for browser bypass instead of inline script to comply with Content Security Policy.

- Fixed Worker OSS paths — added writable OSS paths to openclaw worker AGENTS.md for cloud deployments.

- Fixed auto-refresh STS credentials for all `mc` invocations — wrapped mc binary with `mc-wrapper.sh` that calls `ensure_mc_credentials` before every invocation, preventing token expiry after ~50 minutes in cloud mode.

- Fixed CoPaw STS credential refresh in Python sync loops to prevent MinIO sync failure after token expiry.

- Fixed cloud runtime detection — set `HICLAW_RUNTIME=aliyun` explicitly in Dockerfile.aliyun; respect pre-set `HICLAW_RUNTIME` in hiclaw-env.sh instead of always auto-detecting.

- Fixed reliable welcome message delivery in cloud deployment with proper runtime detection.

- Fixed Worker import: deploy cron jobs from zip to worker; add install command hints when HiClaw is not found; update migrate skill import command with correct CLI usage.

- Fixed reinstall bug in PowerShell script; clean up docker-proxy container and hiclaw-net network on reinstall.

- Fixed Worker containers not added to hiclaw-net network for service connectivity.

- Fixed install UX: show friendly labels instead of env var names in upgrade prompts.

- Fixed unused openclaw hooks config causing startup failure — removed the config.

- Fixed shell script safety in Manager init scripts.

- Fixed explicit Matrix room join with retry before sending welcome message to prevent race condition.

- Fixed worker template package path encoding by URL-encoding package paths during import requests.

- Fixed controller preflight by using lightweight agentspec checks and requiring online agentspec versions.

- Fixed anonymous preflight for agent specs by switching anonymous preflight to agentspec list checks.

- Support `HICLAW_NACOS_USERNAME` and `HICLAW_NACOS_PASSWORD` as default Nacos credentials when `nacos://` URIs omit `user:pass@`; extract Nacos address from URI and add preflight validation.

- Install `@nacos-group/cli` in Worker images so both OpenClaw and CoPaw workers can call `nacos-cli` directly for Nacos-backed skill and agentspec workflows.

- Rework Worker `find-skills` discovery — introduce the `hiclaw-find-skill` wrapper for both OpenClaw and CoPaw workers, infer the backend from `HICLAW_SKILLS_API_URL` (`https://...` for skills.sh, `nacos://host[:port][/namespace]` for Nacos with default port `8848`), surface the resolved registry in command output, propagate registry/Nacos auth during install and Worker creation, and improve Nacos recall/ranking with regression coverage for `react performance` and `pr review`.

- Simplify Worker-facing `find-skills` guidance so registry/backend details stay internal to the runtime and are not exposed in Worker skill docs.

- Default `HICLAW_SKILLS_API_URL` to `nacos://market.hiclaw.io/public` for install flows and Manager-created Workers.

- Added Manager-side worker template search/import flow backed by Nacos AgentSpecs, so the Manager can recommend matching templates and install them after admin confirmation.

---

**新增功能**

- **Team、Human 与声明式管理 (hiclaw-controller)** — 引入 `hiclaw-controller` 二进制文件实现声明式资源管理。通过 YAML 定义 Team、Human 和 Worker 资源，使用 `hiclaw apply` 应用。Team 将 Worker 组织在 leader 下共享目标；Human 将 Matrix 用户绑定到 Worker 实现直接交互。Controller 监听资源变更并自动协调状态。支持 Worker 配置中的内联 `identity`、`soul`、`agents` 字段，无需单独文件。

- **MCP 代理支持** — 新增 `mcp-proxy` 功能，允许通过 HiClaw 网关代理现有 MCP 服务器，使 Worker 无需直接网络暴露即可访问外部 MCP 工具。

- **OpenClaw CMS 插件集成** — Manager 现在集成 `openclaw-cms-plugin` 的安装和运行时接入，扩展 Agent 的内容管理能力。

- **Docker 网络别名** — 用 Docker 网络别名替代 ExtraHosts IP 注入，简化容器网络配置，提升重启后的可靠性。

- **hiclawMode 网关配置** — Higress 网关初始化配置从 `mergeConsecutiveMessages` 切换为 `hiclawMode`，提供统一的 HiClaw 专属配置模式。

- **MiniMax M2.7 默认模型** — 将 MiniMax 默认模型升级至 M2.7，提升性能。

- **交互式版本选择** — 安装脚本现在支持在安装过程中选择特定版本。

- **安装后验证** — 新增验证脚本，安装完成后自动确认所有组件健康。

- **多阶段协作协议** — 在 task-lifecycle 中新增多阶段协作协议，改进 Manager 与 Worker 在复杂任务上的协调。

- **模板化 Worker 导入** — 新增 Manager 内建技能 `hiclaw-find-worker`，将 Worker 创建统一为模板/包导入流程，避免回退到手工创建。

- **模板包简写支持** — 增加模板包简写语法，支持可选版本和默认值，简化导入输入。

- **模板导入路径泛化** — 扩展 `hiclaw-find-worker` 的导入入口，使 market / registry 场景使用一致的导入方式。

- **优化 market Worker 导入流程** — 完善市场 Worker 导入链路，使模板包路径和导入闭环处理更加稳定。

**Bug 修复**

- 修复 embedded 模式删除后本地声明式配置残留问题 — `start-mc-mirror.sh` 现在对 `hiclaw-config/` 使用 `--remove`，删除资源时会同步移除本地被监听的 YAML，避免 `/root/hiclaw-fs/hiclaw-config/` 下残留 stale 文件。

- 修复 `hiclaw apply` 静默忽略所有资源 — `loadResources()` 先调用 `strings.TrimSpace(line)` 再检查 `strings.HasPrefix(line, "  name:")`，去除空格后永远无法匹配。修复为对已 trim 的行检查 `strings.HasPrefix(line, "name:")`。

- 修复包解析失败后资源卡在 `Phase="Pending"` — `r.Status().Update()` 可能因资源版本冲突静默失败，导致 Worker 永久卡住。修复方式：在错误路径状态更新前通过 `r.Get()` 刷新对象，并将带有非空错误 `Message` 的 `Phase="Pending"` 视为可重试。

- 修复网关并发路由授权 — 添加乐观锁重试，防止多个 Worker 同时注册路由时的竞态条件。

- 修复云端 Worker OSS 访问安全 — 云模式下所有 Worker 共享同一 RRSA 角色且 OSS 访问不受限。现在 `oss-credentials.sh` 注入内联 STS 策略，将令牌限制在 `agents/{worker}/*` 和 `shared/*` 前缀。

- 修复 Docker 容器逃逸风险 — 新增 Docker API 代理（`hiclaw-docker-proxy`），限制容器对 Docker daemon 的访问。

- 修复 `create-worker.sh` 健壮性 — 新增 Matrix 房间去重检查和失败通知，防止房间创建冲突时静默失败。

- 修复 `state.json` 注册 — 强制所有任务类型注册，新增空闲停止安全检查，防止任务活跃时 Worker 被停止。

- 修复 Element Web CSP 违规 — 使用外部 JS 文件替代内联脚本以符合内容安全策略。

- 修复 Worker OSS 路径 — 为云端部署在 openclaw worker AGENTS.md 中添加可写 OSS 路径。

- 修复所有 `mc` 调用的 STS 凭证自动刷新 — 用 `mc-wrapper.sh` 包装 mc 二进制文件，每次调用前执行 `ensure_mc_credentials`，防止云模式下约 50 分钟后令牌过期。

- 修复 CoPaw Python 同步循环中的 STS 凭证刷新，防止令牌过期后 MinIO 同步失败。

- 修复云端运行时检测 — 在 Dockerfile.aliyun 中显式设置 `HICLAW_RUNTIME=aliyun`；hiclaw-env.sh 中尊重预设的 `HICLAW_RUNTIME` 而非始终自动检测。

- 修复云端部署中欢迎消息的可靠投递和运行时检测。

- 为 Worker 新增双后端 `find-skills` 支持 —— 引入 `hiclaw-find-skill` 统一封装，默认使用 Nacos 发现技能，并可通过 `HICLAW_FIND_SKILL_BACKEND` 切回 `skills_sh`；同时为 OpenClaw 和 CoPaw Worker 安装运行时 wrapper。

- 修复 Worker 导入：从 zip 部署 cron job 到 Worker；未安装 HiClaw 时添加安装命令提示；更新 migrate skill 导入命令的 CLI 用法。

- 修复 PowerShell 脚本重装 bug；重装时清理 docker-proxy 容器和 hiclaw-net 网络。

- 修复 Worker 容器未加入 hiclaw-net 网络导致服务连接失败。

- 修复安装体验：升级提示中显示友好标签替代环境变量名。

- 修复未使用的 openclaw hooks 配置导致启动失败 — 移除该配置。

- 修复 Manager 初始化脚本中的 shell 脚本安全问题。

- 修复发送欢迎消息前显式加入 Matrix 房间并重试，防止竞态条件。

- 修复 Worker 模板包导入时路径未做 URL 编码导致的失败。

- 调整 Worker 模板导入默认 Nacos 配置：`HICLAW_NACOS_REGISTRY_URI` 改为 `nacos://market.hiclaw.io:80/public`，主机默认值同步改为 `market.hiclaw.io`，并补充控制器预检以校验 `apply` 时的 Worker 名称与 AgentSpec 在线状态（保留独立默认端口回退值不变）。

- 修复匿名预检场景：匿名预检改为基于 agentspec 列表进行检查。

- 支持 `HICLAW_NACOS_USERNAME` 和 `HICLAW_NACOS_PASSWORD` 作为默认 Nacos 凭证（当 `nacos://` URI 省略 `user:pass@` 时）；从 URI 提取 Nacos 地址并添加预检验证。

- 在 Worker 镜像中安装 `@nacos-group/cli`，让 OpenClaw 和 CoPaw Worker 都能直接使用 `nacos-cli` 执行基于 Nacos 的 skill 与 agentspec 工作流。

- 重构 Worker `find-skills` 发现链路 —— 为 OpenClaw 和 CoPaw Worker 引入统一的 `hiclaw-find-skill` wrapper，按 `HICLAW_SKILLS_API_URL` 推断后端（`https://...` 走 skills.sh，`nacos://host[:port][/namespace]` 走 Nacos，默认端口 `8848`），在命令输出中明确展示实际 registry 来源，在安装与创建 Worker 时透传 registry / Nacos 鉴权配置，并优化 Nacos 检索召回与排序，补充 `react performance` / `pr review` 回归测试。

- 简化 Worker 侧 `find-skills` 使用说明 —— 将 registry / backend 细节保留在运行时内部，不再在 Worker 技能文档中暴露这些实现信息。

- 将安装流程与 Manager 创建 Worker 时的 `HICLAW_SKILLS_API_URL` 默认值改为 `nacos://market.hiclaw.io/public`。

- 新增基于 Nacos AgentSpec 的 Worker 模板搜索与导入流程，让 Manager 可以先推荐匹配模板、经管理员确认后再安装。

- 修复 `hiclaw-find-worker` 自动读取 `~/.nacos-cli/default.conf` 导致默认 registry 被本地 profile 覆盖；现在只使用显式 `HICLAW_NACOS_REGISTRY_URI` 或内建默认配置。

---

- feat: add Team, Human, and declarative management (hiclaw-controller) ([fd3b413](https://github.com/alibaba/hiclaw/commit/fd3b413))
- feat(controller): support inline identity/soul/agents fields for Worker config ([e21d489](https://github.com/alibaba/hiclaw/commit/e21d489))
- feat(mcp): add mcp-proxy support for proxying existing MCP servers ([61300b7](https://github.com/alibaba/hiclaw/commit/61300b7))
- feat(manager): integrate openclaw-cms-plugin install and runtime wiring ([1b5a5d8](https://github.com/alibaba/hiclaw/commit/1b5a5d8))
- feat(init): switch from mergeConsecutiveMessages to hiclawMode ([81eb6ca](https://github.com/alibaba/hiclaw/commit/81eb6ca))
- feat: upgrade MiniMax default model to M2.7 ([f058051](https://github.com/alibaba/hiclaw/commit/f058051))
- feat(install): add interactive version selection prompt ([5c11316](https://github.com/alibaba/hiclaw/commit/5c11316))
- feat(install): add post-install verification script ([ce4bfe2](https://github.com/alibaba/hiclaw/commit/ce4bfe2))
- fix(gateway): add optimistic locking retry for concurrent route authorization ([2565e8c](https://github.com/alibaba/hiclaw/commit/2565e8c))
- fix(hiclaw): fix hiclaw apply silently ignoring all resources due to loadResources() parsing bug ([fd3b413](https://github.com/alibaba/hiclaw/commit/fd3b413))
- fix(controller): handle stuck Phase="Pending" resources after failed package resolution ([fd3b413](https://github.com/alibaba/hiclaw/commit/fd3b413))
- fix(security): restrict cloud worker OSS access with STS inline policy ([85e61e9](https://github.com/alibaba/hiclaw/commit/85e61e9))
- fix(security): add Docker API proxy to prevent container escape ([e97e821](https://github.com/alibaba/hiclaw/commit/e97e821))
- fix(worker): improve create-worker robustness with room dedup and failure notification ([8bfe39f](https://github.com/alibaba/hiclaw/commit/8bfe39f))
- fix(manager): enforce state.json registration for all task types and add idle-stop safety ([fa223d2](https://github.com/alibaba/hiclaw/commit/fa223d2))
- fix(element-web): use external JS file for browser bypass to comply with CSP ([d8fd9c4](https://github.com/alibaba/hiclaw/commit/d8fd9c4))
- fix(worker): add writable OSS paths to openclaw worker AGENTS.md ([4527240](https://github.com/alibaba/hiclaw/commit/4527240))
- fix(cloud): wrap mc binary for automatic STS credential refresh ([9e2f2e5](https://github.com/alibaba/hiclaw/commit/9e2f2e5))
- fix(copaw): refresh STS credentials in sync loops to prevent MinIO failure ([5a825e6](https://github.com/alibaba/hiclaw/commit/5a825e6))
- fix(cloud): reliable runtime detection and welcome message delivery ([c6fe492](https://github.com/alibaba/hiclaw/commit/c6fe492))
- fix(import): deploy cron jobs from zip to worker ([e5fd638](https://github.com/alibaba/hiclaw/commit/e5fd638))
- fix(import): add install command hints when HiClaw is not found ([b871a10](https://github.com/alibaba/hiclaw/commit/b871a10))
- fix: update migrate skill import command with correct CLI usage and download URLs ([ff8589b](https://github.com/alibaba/hiclaw/commit/ff8589b))
- fix: Fix the reinstall bug in Powershell script ([653c7f7](https://github.com/alibaba/hiclaw/commit/653c7f7))
- fix(install): clean up docker-proxy container and hiclaw-net network on reinstall ([5fff4bb](https://github.com/alibaba/hiclaw/commit/5fff4bb))
- fix: add Worker containers to hiclaw-net network for service connectivity ([6431f66](https://github.com/alibaba/hiclaw/commit/6431f66))
- fix(install): show friendly labels instead of env var names in upgrade prompts ([a1d985f](https://github.com/alibaba/hiclaw/commit/a1d985f))
- fix(config): remove unused openclaw hooks config to prevent startup failure ([1c73772](https://github.com/alibaba/hiclaw/commit/1c73772))
- fix(manager): improve shell script safety in init scripts ([3f8603a](https://github.com/alibaba/hiclaw/commit/3f8603a))
- fix: add explicit Matrix room join with retry before sending welcome message ([0569d1a](https://github.com/alibaba/hiclaw/commit/0569d1a))
- fix: add multi-phase collaboration protocol to task-lifecycle ([d9393fa](https://github.com/alibaba/hiclaw/commit/d9393fa))
- fix(controller): support HICLAW_NACOS_USERNAME/PASSWORD as default Nacos credentials ([ccf242c](https://github.com/alibaba/hiclaw/commit/ccf242c))
- feat(worker): install @nacos-group/cli in OpenClaw and CoPaw worker images ([unreleased](https://github.com/higress-group/hiclaw))
- refactor(network): replace ExtraHosts IP injection with Docker network aliases ([0eb635d](https://github.com/alibaba/hiclaw/commit/0eb635d))
- refactor: unify DM room creation into manager agent startup ([0569d1a](https://github.com/alibaba/hiclaw/commit/0569d1a))
- feat(memory): add default embedding model (text-embedding-v4) support for Manager and Worker, with openclaw→copaw bridge
- feat(manager): add CoPaw runtime support for Manager container ([f6c8f8d](https://github.com/maplefeng-a/hiclaw/commit/f6c8f8d))
- feat(manager): support worker template import flow, including `hiclaw-find-worker`, generalized import paths, package shorthand, controller preflight fixes, URL-encoded package paths, and explicit registry defaults ([c010206](https://github.com/alibaba/hiclaw/commit/c010206))
