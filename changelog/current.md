# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- feat(manager): add Team Leader heartbeat and worker lifecycle builtins for team-scoped sleep/wake coordination (unreleased)

- fix(manager): make find-skills use deterministic script paths in worker/copaw SKILL.md, render canonical install/search commands from `hiclaw-find-skill.sh`, and treat "import/install xxx skill from market" as a direct install trigger

- chore(base): pin higress/all-in-one base image tag from `sha-d32debd` to `2.2.1` in `openclaw-base/Dockerfile`, `hiclaw-controller/Dockerfile.embedded`, and `manager/docker-legacy/Dockerfile.copaw-all-in-one`

- fix(manager): start Higress WASM plugin-server (`/usr/share/nginx/html/plugins/...` on port 8002) from the same nginx instance as Element Web. Our supervisord configs (both embedded and legacy) override the base image's supervisord which previously launched a separate `plugin-server` program — without it Envoy cannot fetch `ai-proxy`/`key-auth`/`ai-statistics` WASM modules and forwards LLM requests to upstream without Host header rewrite, resulting in 404s from the LLM backend. Listens on both IPv4 and IPv6 because Envoy resolves `localhost` to `::1` first.

- fix(worker): stop Worker-side sync from pushing `SOUL.md`, `AGENTS.md`, and `HEARTBEAT.md` back to MinIO. These files are authoritatively written by the controller (via inline `spec.soul`/`spec.agents` or package contents); the conditional `mc cp`-if-newer loop in `worker-entrypoint.sh` raced with controller pushes — for example `hiclaw apply -f` of a Worker with inline overrides would be reverted by a stale Worker push shortly after, leaving MinIO with the ORIGINAL package content. The corresponding `mc mirror` exclude list already covered these files; the individual re-push was added speculatively for "personality evolution" but no code path actually writes these files from the Worker side.

