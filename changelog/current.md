# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- fix(copaw): refresh STS credentials in Python sync loops to prevent MinIO sync failure after token expiry
- fix(cloud): set `HICLAW_RUNTIME=aliyun` explicitly in Dockerfile.aliyun instead of relying on OIDC file detection at runtime
- fix(cloud): respect pre-set `HICLAW_RUNTIME` in hiclaw-env.sh — only auto-detect when unset
- fix(cloud): add explicit Matrix room join with retry before sending welcome message to prevent race condition

