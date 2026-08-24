# DeepSeek Harness adapter prototype result

## Question

Can an unmodified DeepSeek Harness release load TeamHarness role context and role-appropriate skills, expose the existing TeamHarness MCP server as native DSH tools, survive a repeated install, and uninstall without leaving bundle rows in the selected profile?

## Environment

- AgentTeams base: `223ddc2b8073e4c8b93bcbb15e1d717f196c04d9`
- DeepSeek Harness: `dsh-v0.1.1-rc.2` / `b150a551b8d465e31e418e1b2eaf5e79bbb7d28e`
- Node.js: `v22.21.0`
- pnpm: `11.7.0`
- Python: `3.12.10`
- Host: Windows

## Command

```powershell
pwsh -NoProfile -File plugins/teamharness/adapters/deepseek-harness-prototype/smoke.ps1 -DshRoot ../deepseek-harness-rc2
```

## Result

The command completed with exit code 0 on 2026-08-24.

- DSH installed the packed adapter bundle into an isolated profile.
- The assembled DSH system prompt contained the TeamHarness team contract, Worker role prompt, and member facts read from the controller-shaped `runtime.yaml` fixture.
- The adapter read `plugin.yaml` and materialized only the five Skills allowed for the Worker role: `mcporter`, `find-skills`, `communication`, `file-sharing`, and `task-execution`.
- Leader-only Skills and the unregistered `organization` directory were not exposed.
- DSH registered `mcp__teamharness__health` and `mcp__teamharness__filesync` through its public MCP client.
- Calling `mcp__teamharness__health` returned `{"ok":true,"tool":"health","status":"ok"}`.
- Calling `mcp__teamharness__filesync` with `dryRun: true` resolved a real file path to an `mc cp` command without contacting object storage.
- The Worker-only runtime config hid `mcp__teamharness__message` from DSH's tool registry.
- Installing the same package again left exactly one TeamHarness MCP row and the second runtime smoke also passed.
- Removing the package removed its dependency and bundle rows from the profile.
- The successful run removed its temporary DSH home and workspace.

## What this does not prove

This prototype does not run a paid model turn, send a real Matrix message, contact MinIO or OSS, add a Controller-managed DSH Worker, or test restart and recovery. Those are separate managed-runtime questions. The result only proves the local TeamHarness adapter seam proposed for the first change.

## Design answer

The local adapter is viable without patching DSH or running DSH inside QwenPaw. One detail changed after the first run: pointing DSH at the whole TeamHarness Skills tree is incorrect because it exposes Leader-only Skills to Workers. The adapter must build a role-specific Skill root from `plugin.yaml` before DSH starts.
