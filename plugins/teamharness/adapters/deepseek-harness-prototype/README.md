# TeamHarness DeepSeek Harness prototype

This is throwaway code for one question: can a stock DeepSeek Harness release load TeamHarness role context and skills, connect to the existing TeamHarness MCP server, call its tools, and uninstall cleanly without changing DSH itself?

It is not a production adapter and is not registered in `plugins/teamharness/plugin.yaml`.

The smoke run is pinned to DeepSeek Harness `dsh-v0.1.1-rc.2` at commit `b150a551b8d465e31e418e1b2eaf5e79bbb7d28e`. Build that checkout first, then run:

```powershell
pwsh -File plugins/teamharness/adapters/deepseek-harness-prototype/smoke.ps1 -DshRoot ../deepseek-harness-rc2
```

The script uses a temporary `DSH_HOME`, installs this directory as a DSH bundle, boots the resolved composition, verifies the prompt/skill/MCP path, removes the bundle, and deletes the temporary home after a successful run.
