# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- fix(manager,worker): replace all `~/hiclaw-fs/` tilde-paths with correct absolute paths (`/root/hiclaw-fs/` for shared data, `~/` for worker's own agent dir) in AGENTS.md, TOOLS.md, all SKILL.md files, and scripts — Manager container sets `HOME=/root/manager-workspace`, so `~/hiclaw-fs/` was expanding to the wrong location
- fix(manager): allow unstable room versions in Tuwunel to fix room version 11 error
- feat(manager): reduce default context windows (qwen3.5-plus: 960k→200k, unknown models: 200k→150k) and support `--context-window` override for unknown models in model-switch skills
- feat(manager): switch group session reset from idle (2880min) to daily at 04:00, matching DM sessions; remove keepalive mechanism (session-keepalive.sh, notify-admin-keepalive.sh, HEARTBEAT step 7, AGENTS.md keepalive response section)
- feat(copaw): buffer non-mentioned group messages as history context with `[Chat messages since your last reply - for context]` / `[Current message - respond to this]` markers (matching OpenClaw convention); download images for history when vision is enabled; bridge `historyLimit` config ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- fix(copaw): strip leading `$` from Matrix event IDs in media filenames to avoid URI-encoding issues breaking agentscope's image extension check ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- chore(copaw): use registry mirror for Python base image in Dockerfile; bump copaw-worker to 0.1.2 ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- refactor(manager): move higress-gateway-management and coding-cli-management skills to skills-alpha/ so they are not auto-loaded by Manager; update references in TOOLS.md, docs, and task-coordination SKILL.md
- feat(manager): unify "Be Responsive but Not Noisy" and "Incoming Message Format" sections across Manager, OpenClaw Worker, and CoPaw Worker AGENTS.md; add missing behavioral sections (task execution, task directory rules, progress tracking, project participation, etc.) to CoPaw Worker AGENTS.md for parity with OpenClaw Worker
- fix(manager): set proper Matrix room power levels — Admin and Manager get power level 100 (admin), Workers default to 0 (regular user); switch from `trusted_private_chat` to `private_chat` preset with `power_level_content_override` in create-worker.sh, create-project.sh, and matrix-server-management SKILL.md
- fix(manager): add `state.json` initialization and `manage-state.sh` script to fix state.json never being created — add template + startup init in `upgrade-builtins.sh` and `lifecycle-worker.sh`; replace manual jq edits with atomic script calls (add-finite/add-infinite/complete/executed/list) in task-management SKILL.md and HEARTBEAT.md; add `title` field to each task entry for quick identification
- refactor(manager,worker): move coding-cli worker skill to skills-alpha/worker-skills/ since it depends on the alpha coding-cli-management skill; remove coding-cli references from worker-management SKILL.md, TOOLS.md, task-coordination SKILL.md, and Worker AGENTS.md files
- fix(manager): convert project-management SKILL.md message templates from Chinese to English reference templates with "adapt language to human admin's preference" guidance — fixes intermittent test failures caused by language mismatch between SKILL.md templates and test grep patterns
- fix(worker): remove `.openclaw/**` from file-sync exclude list so OpenClaw session and cron configurations are synced to MinIO
- fix(copaw): Windows compatibility — catch `NotImplementedError` for signal handlers on Windows `ProactorEventLoop`; support `mc.exe` download on Windows in `_ensure_mc`; use `Path.as_posix()` for MinIO object keys to avoid backslash separators
