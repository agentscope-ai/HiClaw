# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- feat(manager): add model-switch skill with `update-manager-model.sh` script for runtime model switching ([00cbaa5](https://github.com/higress-group/hiclaw/commit/00cbaa59a211ad42779e495ae4a04eb09e664999))
- feat(manager): add task-management skill (extracted from AGENTS.md) covering task workflow and state file spec ([00cbaa5](https://github.com/higress-group/hiclaw/commit/00cbaa59a211ad42779e495ae4a04eb09e664999))
- feat(manager): add `manager/scripts/lib/builtin-merge.sh` — shared library for idempotent builtin section merging ([00cbaa5](https://github.com/higress-group/hiclaw/commit/00cbaa59a211ad42779e495ae4a04eb09e664999))
- fix(manager): fix `upgrade-builtins.sh` duplicate-insertion bug — awk now uses exact line match, preventing repeated marker injection on re-run ([00cbaa5](https://github.com/higress-group/hiclaw/commit/00cbaa59a211ad42779e495ae4a04eb09e664999))
- fix(manager): detect and auto-repair corrupted AGENTS.md when marker count != 1 or heading is duplicated ([47c5578](https://github.com/higress-group/hiclaw/commit/47c5578ccdc74c540904c30b606cf8c6b722ddf3), [c28f82d](https://github.com/higress-group/hiclaw/commit/c28f82d979a604ef96804aa03289f0849197aa0f), [078f3f8](https://github.com/higress-group/hiclaw/commit/078f3f86c9f5215dc3c50ab34626792f72332074))
- feat(manager): expand worker-management skill and `lifecycle-worker.sh` with improved worker lifecycle handling ([00cbaa5](https://github.com/higress-group/hiclaw/commit/00cbaa59a211ad42779e495ae4a04eb09e664999))
- fix(manager): `setup-higress.sh` — multiple route/consumer/MCP init fixes ([d259177](https://github.com/higress-group/hiclaw/commit/d259177ffdac5cbe504d067ec8e45fd7c1c5b859))
- fix(manager): `start-manager-agent.sh` — wait for Tuwunel Matrix API ready before proceeding, add detailed logging for token acquisition ([d259177](https://github.com/higress-group/hiclaw/commit/d259177ffdac5cbe504d067ec8e45fd7c1c5b859), [1a9e1d8](https://github.com/higress-group/hiclaw/commit/1a9e1d8956c1d580d4e6aaa2646203edd4f5999b))
- fix(manager): support Podman by replacing hardcoded `docker` commands with runtime detection; fix `jq` availability inside container; fix provider switch menu text ([9d57ef8](https://github.com/higress-group/hiclaw/commit/9d57ef8656597cb2eb57424ffec2ece2438c93cd))
