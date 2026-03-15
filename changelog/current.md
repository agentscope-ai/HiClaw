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
