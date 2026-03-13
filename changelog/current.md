# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

**What's New**

- Exposed OpenClaw Console at host port 18888 via nginx reverse proxy with automatic gateway token injection into localStorage — no manual token entry needed. Added a dedicated Higress route (`console-local.hiclaw.io`) with basic-auth protection using admin credentials. Fixed device-pairing chicken-and-egg problem by disabling device auth on the gateway side.
- Enforced explicit room targeting across all heartbeat and agent messaging — Heartbeat caches `admin_dm_room_id` in state.json and uses the `message` tool for all notifications; replaced the deprecated `/hooks/agent` webhook with the built-in `message` tool for primary channel messaging.
- Switched group session reset trigger from idle timeout (2880 min) to a fixed daily schedule at 04:00, matching DM session behavior; removed the keepalive mechanism (session-keepalive.sh, notify-admin-keepalive.sh, HEARTBEAT step 7).
- Reduced default context windows (qwen3.5-plus: 960k→200k, unknown models: 200k→150k) and added `--context-window` override support for unknown models in model-switch skills.
- Added `state.json` initialization and `manage-state.sh` atomic script (add-finite/add-infinite/complete/executed/list) — state.json is now created on first boot via upgrade-builtins.sh and lifecycle-worker.sh; each task entry includes a `title` field for quick identification.
- Unified agent behavioral guidelines ("Be Responsive but Not Noisy", "Incoming Message Format", task execution rules, progress tracking, project participation) across Manager, OpenClaw Worker, and CoPaw Worker AGENTS.md.

**Bug Fixes**

- Fixed worker container recreation on Manager upgrade — workers are now correctly stopped and removed when upgrading an existing installation.
- Fixed all `~/hiclaw-fs/` tilde-path expansions to use correct absolute paths (`/root/hiclaw-fs/` for shared data) across AGENTS.md, TOOLS.md, all SKILL.md files, and scripts.
- Fixed `.openclaw/` directory exclusion in file-sync so OpenClaw session and cron configurations are synced to MinIO.
- Fixed `container_api_available` check to work under strict mode (`set -euo pipefail`).
- Fixed `mc mirror --watch` background process cleanup on container shutdown.
- Fixed Matrix room power levels — Admin and Manager get level 100 (admin), Workers default to 0; switched from `trusted_private_chat` to `private_chat` preset with `power_level_content_override`.
- Fixed Tuwunel unstable room versions error (room version 11 compatibility).
- Fixed CoPaw Windows compatibility — `NotImplementedError` for signal handlers on `ProactorEventLoop`; `mc.exe` download on Windows; `Path.as_posix()` for MinIO object keys.
- Registered all Higress route domains via `/v1/domains` API (previously missing for Matrix Client, File System, AI Gateway, and Console routes).

---

**新增功能**

- 通过 nginx 反向代理在主机 18888 端口暴露 OpenClaw 控制台，自动向 localStorage 注入 gateway token（无需手动输入）。新增 Higress 路由（`console-local.hiclaw.io`）并以管理员凭据开启 basic-auth 保护。通过在 gateway 侧禁用设备认证，解决了设备配对的先有鸡还是先有蛋问题。
- 强化心跳和 Agent 消息的明确房间定向 — 心跳将 `admin_dm_room_id` 缓存到 state.json，所有通知改用 `message` 工具发送；废弃 `/hooks/agent` webhook，改用内置 `message` 工具用于主 channel 消息发送。
- 将 Group 会话重置触发方式从空闲超时（2880 分钟）改为每天 04:00 固定定时，与 DM 会话保持一致；移除 keepalive 机制（session-keepalive.sh、notify-admin-keepalive.sh、HEARTBEAT 第 7 步）。
- 降低默认上下文窗口（qwen3.5-plus：960k→200k，未知模型：200k→150k），并在模型切换 skill 中为未知模型新增 `--context-window` 覆盖支持。
- 新增 `state.json` 初始化和 `manage-state.sh` 原子操作脚本（add-finite/add-infinite/complete/executed/list）— state.json 现于首次启动时通过 upgrade-builtins.sh 和 lifecycle-worker.sh 自动创建；每条任务记录包含 `title` 字段便于快速识别。
- 统一了 Manager、OpenClaw Worker 和 CoPaw Worker AGENTS.md 中的 Agent 行为准则（"适度响应"、"消息格式"、任务执行规则、进度跟踪、项目参与等）。

**Bug 修复**

- 修复 Manager 升级时 Worker 容器重建问题 — 升级已有安装时 Worker 现在能被正确停止和删除。
- 修复所有 `~/hiclaw-fs/` tilde 路径，改为正确的绝对路径（`/root/hiclaw-fs/`），涉及 AGENTS.md、TOOLS.md、所有 SKILL.md 和脚本文件。
- 修复文件同步中 `.openclaw/` 目录被排除的问题，使 OpenClaw 会话和 cron 配置能同步到 MinIO。
- 修复 `container_api_available` 检查在严格模式（`set -euo pipefail`）下报错的问题。
- 修复容器关闭时 `mc mirror --watch` 后台进程未被清理的问题。
- 修复 Matrix 房间权限等级设置 — 管理员和 Manager 获得 100 级（管理员），Worker 默认 0 级；改用 `private_chat` preset 并设置 `power_level_content_override`。
- 修复 Tuwunel 不稳定房间版本错误（room version 11 兼容性）。
- 修复 CoPaw Windows 兼容性 — `ProactorEventLoop` 信号处理器 `NotImplementedError`；Windows 下 `mc.exe` 下载；MinIO object key 使用 `Path.as_posix()`。
- 补全 Higress 路由的 `/v1/domains` 注册调用（此前 Matrix Client、文件系统、AI Gateway 和控制台路由均缺少域名创建步骤）。

---

- feat(manager): support openclaw gateway restart and expose openclaw console at port 18888 via nginx reverse proxy ([d80c539](https://github.com/alibaba/hiclaw/commit/d80c539))
- fix(manager): auto-inject gateway token into openclaw console via nginx sub_filter so Control UI auto-authenticates without manual token entry ([8df7e7f](https://github.com/alibaba/hiclaw/commit/8df7e7f))
- fix(manager): disable device pairing for openclaw console access from nginx proxy — set `gateway.controlUi.dangerouslyDisableDeviceAuth=true` to rely solely on the shared token ([5a0df68](https://github.com/alibaba/hiclaw/commit/5a0df68))
- feat(manager): add OpenClaw Console Higress route (`console-local.hiclaw.io`) with service source, domain registration, and basic-auth plugin using `HICLAW_ADMIN_USER`/`HICLAW_ADMIN_PASSWORD`; register all route domains via `/v1/domains` API; add `HICLAW_CONSOLE_DOMAIN` and `HICLAW_PORT_OPENCLAW_CONSOLE` to install scripts ([9f40f20](https://github.com/alibaba/hiclaw/commit/9f40f20))
- feat(manager,copaw,worker): enforce explicit room targeting in heartbeat — cache `admin_dm_room_id` in state.json, use `message` tool for all notifications, improve agent communication guidelines ([7e364c8](https://github.com/alibaba/hiclaw/commit/7e364c8))
- refactor(manager): replace `/hooks/agent` webhook with built-in `message` tool for primary channel messaging ([6dce243](https://github.com/alibaba/hiclaw/commit/6dce243))
- feat(manager): switch group session reset from idle timeout (2880min) to daily at 04:00; remove keepalive mechanism ([2422c98](https://github.com/alibaba/hiclaw/commit/2422c98))
- feat(manager): reduce default context windows (qwen3.5-plus: 960k→200k, unknown models: 200k→150k) and support `--context-window` override for unknown models in model-switch skills ([ed413cb](https://github.com/alibaba/hiclaw/commit/ed413cb))
- fix(manager,worker): fix `~/hiclaw-fs/` tilde-path resolution to absolute `/root/hiclaw-fs/`; add `state.json` initialization and `manage-state.sh` atomic script; move coding-cli and higress-gateway-management skills to skills-alpha/; set proper Matrix room power levels; unify AGENTS.md behavioral guidelines; fix project-management SKILL.md templates ([142c716](https://github.com/alibaba/hiclaw/commit/142c716))
- fix(manager): fix worker container recreation on Manager upgrade ([d4ca298](https://github.com/alibaba/hiclaw/commit/d4ca298))
- fix(worker): sync `.openclaw/` directory to MinIO for session/cron persistence ([4e5b702](https://github.com/alibaba/hiclaw/commit/4e5b702))
- fix: make `container_api_available` work in strict mode (`set -euo pipefail`) ([eb4e73b](https://github.com/alibaba/hiclaw/commit/eb4e73b))
- fix: add proper cleanup for `mc mirror --watch` background process ([75e3ce9](https://github.com/alibaba/hiclaw/commit/75e3ce9))
- fix(copaw): Windows compatibility — `NotImplementedError` for signal handlers on `ProactorEventLoop`; `mc.exe` download; `Path.as_posix()` for MinIO object keys ([6e21acb](https://github.com/alibaba/hiclaw/commit/6e21acb))
- fix(manager): allow unstable room versions in Tuwunel to fix room version 11 error ([4f4fde6](https://github.com/alibaba/hiclaw/commit/4f4fde6))
- feat(copaw): buffer non-mentioned group messages as history context; download images for history when vision is enabled; bridge `historyLimit` config; strip leading `$` from Matrix event IDs in media filenames; bump copaw-worker to 0.1.2 ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- chore(manager,worker): update `openclaw-base` image tag to `20260313-5a0df68`
