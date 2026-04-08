# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

### Features

- **feat(mem0): add Mem0 long-term memory support for the Manager and OpenClaw Workers** — HiClaw now supports Mem0 in Platform mode, so the Manager and OpenClaw Workers can retain long-term memory across sessions. Each OpenClaw Worker keeps its memory isolated under its own worker identity. CoPaw Workers are not supported yet.
