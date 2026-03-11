# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- fix(manager): allow unstable room versions in Tuwunel to fix room version 11 error
- feat(manager): reduce default context windows (qwen3.5-plus: 960k→200k, unknown models: 200k→150k) and support `--context-window` override for unknown models in model-switch skills
- feat(manager): switch group session reset from idle (2880min) to daily at 04:00, matching DM sessions; remove keepalive mechanism (session-keepalive.sh, notify-admin-keepalive.sh, HEARTBEAT step 7, AGENTS.md keepalive response section)
- feat(copaw): buffer non-mentioned group messages as history context with `[Chat messages since your last reply - for context]` / `[Current message - respond to this]` markers (matching OpenClaw convention); download images for history when vision is enabled; bridge `historyLimit` config ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- fix(copaw): strip leading `$` from Matrix event IDs in media filenames to avoid URI-encoding issues breaking agentscope's image extension check ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- chore(copaw): use registry mirror for Python base image in Dockerfile; bump copaw-worker to 0.1.2 ([7eec4a5](https://github.com/higress-group/hiclaw/commit/7eec4a5))
- refactor(manager): move higress-gateway-management skill to unstable/ so it is not auto-loaded by Manager; remove references from TOOLS.md, README, and mcp-server-management SKILL.md
- feat(manager): unify "Be Responsive but Not Noisy" and "Incoming Message Format" sections across Manager, OpenClaw Worker, and CoPaw Worker AGENTS.md; add missing behavioral sections (task execution, task directory rules, progress tracking, project participation, etc.) to CoPaw Worker AGENTS.md for parity with OpenClaw Worker
