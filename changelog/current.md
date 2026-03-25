# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

### Security

- **fix(security): restrict cloud worker OSS access with STS inline policy** — In cloud mode (Alibaba Cloud SAE), all workers shared the same RRSA role with unrestricted OSS bucket access, allowing any worker to read/write other workers' and manager's files. Now `oss-credentials.sh` injects an inline policy into the STS `AssumeRoleWithOIDC` request when `HICLAW_WORKER_NAME` is set, restricting the STS token to `agents/{worker}/*` and `shared/*` prefixes only — matching the per-worker MinIO policy used in local mode. Manager (which does not set `HICLAW_WORKER_NAME`) retains full access.

### Cloud Runtime
- **fix(cloud): auto-refresh STS credentials for all mc invocations** — wrap mc binary with `mc-wrapper.sh` that calls `ensure_mc_credentials` before every invocation, preventing token expiry after ~50 minutes in cloud mode. Affects: manager, worker, copaw.
- fix(copaw): refresh STS credentials in Python sync loops to prevent MinIO sync failure after token expiry

- fix(cloud): set `HICLAW_RUNTIME=aliyun` explicitly in Dockerfile.aliyun instead of relying on OIDC file detection at runtime
- fix(cloud): respect pre-set `HICLAW_RUNTIME` in hiclaw-env.sh — only auto-detect when unset
- fix: add explicit Matrix room join with retry before sending welcome message to prevent race condition

