# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

### Controller

- **fix(hiclaw): fix `hiclaw apply` silently ignoring all resources due to `loadResources()` parsing bug** — `loadResources()` called `strings.TrimSpace(line)` first (removing leading spaces), then checked `strings.HasPrefix(line, "  name:")` — this prefix could never match after trimming, so `r.Name` was always empty and every resource was silently skipped. Fixed by changing the check to `strings.HasPrefix(line, "name:")` (and the corresponding `TrimPrefix`) to match the already-trimmed line.

- **fix(controller): handle stuck Phase="Pending" resources after failed package resolution** — When `ResolveAndExtract` or `create-worker.sh` failed, the `r.Status().Update()` setting `Phase="Failed"` could silently fail due to a resource version conflict, leaving the worker permanently stuck at `Phase="Pending"`. Fixed by: (1) refreshing the object via `r.Get()` before each error-path status update to avoid conflicts; (2) treating `Phase="Pending"` with a non-empty error `Message` as retriable, so the reconciler calls `handleCreate` instead of the no-op `handleUpdate`.

### Security

- **fix(security): restrict cloud worker OSS access with STS inline policy** — In cloud mode (Alibaba Cloud SAE), all workers shared the same RRSA role with unrestricted OSS bucket access, allowing any worker to read/write other workers' and manager's files. Now `oss-credentials.sh` injects an inline policy into the STS `AssumeRoleWithOIDC` request when `HICLAW_WORKER_NAME` is set, restricting the STS token to `agents/{worker}/*` and `shared/*` prefixes only — matching the per-worker MinIO policy used in local mode. Manager (which does not set `HICLAW_WORKER_NAME`) retains full access.

### Cloud Runtime
- **fix(cloud): auto-refresh STS credentials for all mc invocations** — wrap mc binary with `mc-wrapper.sh` that calls `ensure_mc_credentials` before every invocation, preventing token expiry after ~50 minutes in cloud mode. Affects: manager, worker, copaw.
- fix(copaw): refresh STS credentials in Python sync loops to prevent MinIO sync failure after token expiry

- fix(cloud): set `HICLAW_RUNTIME=aliyun` explicitly in Dockerfile.aliyun instead of relying on OIDC file detection at runtime
- fix(cloud): respect pre-set `HICLAW_RUNTIME` in hiclaw-env.sh — only auto-detect when unset
- fix: add explicit Matrix room join with retry before sending welcome message to prevent race condition

