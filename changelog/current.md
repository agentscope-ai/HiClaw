# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- feat(manager,worker): add local Codex runtime wiring so manager/workers can run as Codex sessions with host `~/.codex` auth and no API key ([71ef7a7](https://github.com/higress-group/hiclaw/commit/71ef7a7))
- fix(manager): preserve worker runtime when recreating local workers so codex workers do not fall back to openclaw (uncommitted)
- fix(manager,worker): send Matrix typing notifications while Codex runtime is handling a turn (uncommitted)
- fix(manager,worker): re-check Matrix room membership on each turn so DM rooms upgraded to groups do not stay misclassified (uncommitted)
- fix(worker): pass assigned Matrix room id into worker runtime and auto-join missing worker rooms on startup (uncommitted)
- fix(manager,worker): skip group-room router on explicit @mentions and keep the Codex app-server warm across turns to reduce reply latency (uncommitted)
- fix(manager): default the Manager to auto-follow allowed group-room conversations instead of requiring @mentions for every turn (uncommitted)
