# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

### Security

- **fix(security): restrict cloud worker OSS access with STS inline policy** — In cloud mode (Alibaba Cloud SAE), all workers shared the same RRSA role with unrestricted OSS bucket access, allowing any worker to read/write other workers' and manager's files. Now `oss-credentials.sh` injects an inline policy into the STS `AssumeRoleWithOIDC` request when `HICLAW_WORKER_NAME` is set, restricting the STS token to `agents/{worker}/*` and `shared/*` prefixes only — matching the per-worker MinIO policy used in local mode. Manager (which does not set `HICLAW_WORKER_NAME`) retains full access.
- **fix(copaw): refresh STS credentials in Python sync loops to prevent MinIO sync failure after token expiry

