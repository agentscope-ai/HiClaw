# TeamHarness DeepSeek Harness prototype

This adapter lets a stock DeepSeek Harness release load Controller-projected TeamHarness context and role-specific Skills, then connect to the existing TeamHarness MCP server without changing DSH itself.

It is still marked as a prototype and is not registered in `plugins/teamharness/plugin.yaml`. The managed Worker image installs it directly into a dedicated headless DSH profile.

The smoke run is pinned to DeepSeek Harness `dsh-v0.1.1-rc.2` at commit `b150a551b8d465e31e418e1b2eaf5e79bbb7d28e`. Build that checkout first, then run:

```powershell
pwsh -File plugins/teamharness/adapters/deepseek-harness-prototype/smoke.ps1 -DshRoot ../deepseek-harness-rc2
```

The script uses a temporary `DSH_HOME`, installs this directory as a DSH bundle, boots both Worker and standalone compositions, verifies the prompt/Skill/MCP path, removes the bundle, and deletes the temporary home after a successful run.

Additional live checks are documented in `deepseek-harness/README.md`. They cover a real model turn, Matrix, MinIO, and Controller-driven create/recovery/delete behavior.
