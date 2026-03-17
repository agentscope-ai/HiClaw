# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- fix(manager): normalize worker name to lowercase in create-worker.sh to match Tuwunel's username storage behavior, fixing invite failures when worker names contain uppercase letters
- feat(cloud): add Alibaba Cloud SAE deployment support with unified cloud/local abstraction layer
