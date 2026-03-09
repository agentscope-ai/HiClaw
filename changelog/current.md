# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- feat(manager): add AI identity section to Manager and Worker SOUL.md templates, ensuring agents understand they are AI not human and can work continuously ([ecca010](https://github.com/higress-group/hiclaw/commit/ecca010))
- fix: set container timezone from TZ env var in both Manager and Worker (install tzdata in base image, configure /etc/localtime and /etc/timezone at startup)
- feat(manager): add User-Agent header (HiClaw/<version>) to default AI route via headerControl, and send it in LLM connectivity tests ([3242d06](https://github.com/higress-group/hiclaw/commit/3242d0630d196c35b5df6fd6fbd7ac6e6b72c08a))
- feat(openclaw-base): install cron package in base image, start crond in Manager (supervisord) and Worker (entrypoint)
- fix(manager): unify worker file-sync notification — replace runtime-specific `hiclaw-sync` command with generic "use your file-sync skill" message in `lifecycle-worker.sh`, `push-worker-skills.sh`, `create-worker.sh`, and `start-manager-agent.sh`; update `worker-management/SKILL.md` accordingly
- fix(manager): fix `create-worker.sh` to push runtime-specific `AGENTS.md` for copaw workers instead of always using openclaw's `worker-agent/AGENTS.md`
- feat(manager): add `copaw-worker-agent/AGENTS.md` describing copaw worker workspace layout and MinIO-based file access (no `~/hiclaw-fs/` mount)
- fix(manager): update task/project notification messages in `task-management/SKILL.md` and `project-management/SKILL.md` to use MinIO paths (`hiclaw/hiclaw-storage/...`) instead of local `~/hiclaw-fs/` paths, compatible with both openclaw and copaw workers
- fix(worker): update `file-sync/SKILL.md` to document MinIO→local path mapping so worker knows where to find files after `hiclaw-sync`
- fix(copaw): patch copaw module-level path constants (`WORKING_DIR`, `SECRET_DIR`, `_PROVIDERS_JSON`) at runtime in `bridge.py` so providers.json is written to and read from the correct worker-specific directory (fixes 401 AuthenticationError on LLM calls)
- feat(copaw): add `copaw/` package — HiClaw's CoPaw Worker runtime (`copaw-worker` CLI) that bridges openclaw.json → CoPaw config, implements MatrixChannel, and syncs config from MinIO
- fix(manager): copaw install command now uses `HICLAW_PORT_GATEWAY` (external port) instead of internal `:8080` so the command works on the host machine
- feat(copaw): add optional `--console-port` to copaw-worker; headless mode saves ~500MB RAM; startup prints memory tip in both cases; SKILL.md and create-worker.sh updated accordingly
- fix(copaw): fix MatrixChannel not mentioning sender in replies (missing `sender_id` in meta payload caused manager to ignore worker replies)
- feat(copaw): sync skills from MinIO on startup (`_sync_skills`)
- feat(copaw): rewrite `sync.py` to use mc CLI for all MinIO operations (mc cat, mc ls, mc alias set); remove httpx + AWS Signature V4 implementation
- feat(copaw): add CoPaw-specific file-sync skill (`manager/agent/copaw-worker-agent/skills/file-sync/`) with `copaw-sync.py` script for manual sync trigger; `create-worker.sh` selects runtime-specific file-sync skill from `/opt/hiclaw/agent/copaw-worker-agent/` for copaw runtime
- feat(copaw): add local→MinIO change-triggered push loop (`push_loop` / `push_local` in `sync.py`); started alongside the existing remote→local sync loop in `worker.py`; mirrors openclaw worker entrypoint behavior (5s poll, excludes `.copaw/` internals)
