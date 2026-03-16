# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- feat(copaw): convert Markdown to HTML in Matrix messages using markdown-it-py (same engine as OpenClaw) with linkify, breaks, strikethrough, and table support
- feat(manager): add find-worker.sh to consolidate worker availability check (registry + state + lifecycle + SOUL.md) into a single script call
- fix(manager): lifecycle-worker.sh idle detection now considers infinite tasks — Workers with active infinite tasks are no longer auto-stopped
- fix(manager): HEARTBEAT.md Steps 5/6 updated to treat infinite tasks as active for idle detection and anomaly checks
- feat(manager): task-management SKILL.md adds finite vs infinite decision guide for the Agent
- feat(manager): add resolve-notify-channel.sh to unify admin notification channel resolution (primary-channel → Matrix DM fallback)
- feat(manager): add manage-primary-channel.sh for validated, atomic primary-channel.json operations (confirm/reset/show)
- feat(manager): task-management SKILL.md adds admin notification step on finite task completion
- feat(manager): project-management SKILL.md adds admin notification step on project task completion
- refactor(manager): HEARTBEAT.md Step 7 and Step 1 now use resolve-notify-channel.sh instead of inline channel resolution
- refactor(manager): channel-management SKILL.md replaces all manual cat/jq writes with manage-primary-channel.sh calls
- fix(manager): TOOLS.md channel-management first-contact trigger corrected from "first time" to "channel mismatch", added show command
- fix(manager): TOOLS.md clarifies copaw runtime vs deployment mode (copaw ≠ remote), adds Deployment column to runtime table
- feat(manager): TOOLS.md task-management fewshot now includes infinite task trigger scenario
- fix(manager): manage-state.sh `executed` action no longer errors when infinite task is missing from active_tasks (backward compat with legacy tasks)
- feat(manager): add delegation-first principle to SOUL.md — Manager prioritizes assigning tasks to Workers over self-execution
- feat(manager): task-management SKILL.md Step 0 decision flow now explicitly marks Worker delegation as preferred and self-execution as last resort
- fix(worker): fix `hiclaw-sync: Permission denied` after upgrade — replace symlink with `/bin/sh` wrapper so execution does not depend on `+x` permission bit (MinIO does not preserve Unix permissions); add `chmod +x` in `hiclaw-sync.sh` and entrypoint fallback sync to restore script permissions after pull
- fix(install): upgrade now pulls both openclaw and copaw worker images when the other runtime's image exists locally, ensuring all worker containers get updated regardless of the selected default runtime
- fix(manager): add cooldown (default 1h) to worker builtin-upgrade notification — prevents repeated Matrix messages wasting Worker tokens when Manager crash-loops
- fix(copaw): deduplicate customized skills that shadow builtins after upgrade — removes stale customized_skills/ copies when a newer CoPaw version ships the same skill as a builtin, preventing duplicate skill entries in the UI
- docs(manager): improve CoPaw console documentation in SKILL.md — add trigger keywords, description, and scope notes; restructure TOOLS.md to clearly separate Skills vs Operations sections
- fix(manager): worker AGENTS.md @mention protocol — require @mention when replying to Manager progress inquiries; change phase completion to task-only completion notification (TASK_COMPLETED format)
- fix(copaw): skill sync now mirrors entire skill directory (including scripts/ and references/) instead of only pulling SKILL.md, matching OpenClaw worker's mc mirror behavior; restores +x on .sh files after pull
- feat(manager): pre-configure all known models in openclaw.json templates — switching between known models is now a hot-reload (no restart); unknown models are appended dynamically with RESTART_REQUIRED output
- feat(manager): model-switch and worker-model-switch scripts detect known vs unknown models via openclaw.json models array instead of overwriting models[0]
- feat(manager): model-switch and worker-model-switch SKILL.md updated — Agent checks script output for RESTART_REQUIRED to decide whether to prompt user for restart
- feat(manager): add known-models.json and upgrade-path merge — on Manager restart, existing openclaw.json (Manager + all Workers in MinIO) gets missing known models merged in, enabling hot-switch without restart for upgraded deployments
- feat(copaw): add E2EE support — bridge `encryption` flag from openclaw.json, create matrix-nio client with crypto store when enabled, handle encrypted media events (RoomEncryptedImage/Audio/Video/File), auto-upload E2E keys, auto-query device keys on sync, ignore unverified devices for bot use case; set device_id from whoami for proper Olm key association
- fix(copaw): upgrade matrix-nio dependency to `matrix-nio[e2e]` to include olm/peewee/atomicwrites for E2EE support
- fix(manager): worker openclaw.json upgrade now adds missing `encryption` field for existing workers (previously only new workers got it from template)
- fix(copaw): persist Matrix sync token to disk and restore on restart — prevents replaying old messages after container restart; catch-up sync with callbacks suppressed for old-version upgrades
- fix(copaw): add full E2EE key maintenance to sync loop — keys_claim and send_to_device_messages were missing, preventing Olm session establishment needed to decrypt messages in encrypted rooms; also fixes catch-up sync not actually suppressing callbacks (temporarily removes event callbacks during initial sync)
- fix(copaw): slash commands in group rooms — skip history prepend when message starts with "/" so CoPaw's command parser recognises the command; strip @mention prefix before slash command detection
- feat(manager): add model alias support to openclaw.json — all known models get `agents.defaults.models` alias entries (e.g. `"hiclaw-gateway/claude-sonnet-4-6": {"alias": "claude-sonnet-4-6"}`); templates, upgrade merge, and model-switch scripts all generate aliases; Worker upgrade merge is now unconditional and idempotent (diff-based push)
- fix(worker): exclude `.openclaw/matrix/**` and `.openclaw/canvas/**` from MinIO sync — Matrix crypto SQLite corrupts when synced via object storage (no POSIX file locks); canvas is regenerated on startup
- fix(manager/worker): clean `.openclaw/matrix` on startup — prevents "database disk image is malformed" errors after unclean shutdown; E2EE sessions are automatically re-negotiated
- fix(manager): Worker AGENTS.md upgrade now uses builtin-section merge instead of mc cp overwrite — preserves Worker's custom content after `<!-- hiclaw-builtin-end -->` marker; legacy files without markers are overwritten and gain marker protection on first upgrade
- fix(copaw): inner `.copaw/AGENTS.md` and `.copaw/SOUL.md` changes now sync back to outer layer before MinIO push — previously Agent modifications to these files were silently lost on restart or re-bridge
- fix(copaw): `_sync_skills` now mirrors full skill directories (SKILL.md + scripts/ + references/) from outer `skills/` to inner `.copaw/active_skills/` — previously only SKILL.md was copied, causing missing scripts after skill updates
