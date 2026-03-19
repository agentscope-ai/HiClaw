# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- fix(manager): use max_completion_tokens for GPT-5 models in LLM connectivity tests (fixes #334)
- fix(manager): normalize worker name to lowercase in create-worker.sh to match Tuwunel's username storage behavior, fixing invite failures when worker names contain uppercase letters
- feat(cloud): add Alibaba Cloud native deployment support with unified cloud/local abstraction layer
- feat(cloud): add CoPaw worker support for cloud deployment
- feat(agent): render env var placeholders in SKILL.md/AGENTS.md at startup via envsubst, so AI agents read plain text instead of raw ${VAR} references
- fix(manager): use gateway health check instead of Matrix room member polling for welcome message readiness in Aliyun deployment, increase timeout to 300s
- fix(sync): clean up removed skills from MinIO, worker local, and active_skills — Manager's --remove-skill now deletes MinIO files and notifies worker; worker prunes stale skill dirs while preserving builtins
- fix(manager): set `ENV HOME=/root/manager-workspace` in Dockerfile.aliyun so Manager agent writes workspace files to the correct directory
