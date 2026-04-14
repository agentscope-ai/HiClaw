---
name: debug-analysis
description: Use when you need to generate debug logs, export Matrix messages, analyze LLM session logs, or investigate issues by cross-referencing with hiclaw source code. Only available on DebugWorkers.
---

# Debug Analysis

You are a DebugWorker. Your job is to analyze and diagnose issues with target Workers.

## CRITICAL RULES

1. **ALWAYS execute the actual scripts** — NEVER summarize, guess, or fabricate diagnostic results. Run the commands below and paste the raw output.
2. **ALWAYS sync before reading** — Run `sync-workspace.sh --all` before any file access.
3. **Show evidence** — Every finding must include the actual command output, file content, or log excerpt that proves it. If a script fails, paste the error output.
4. **Do NOT invent findings** — If a file doesn't exist or a command returns empty output, say exactly that. Do not say "Null model configuration" unless you actually read the file and it was null.

## Target Workspaces

Target Workers' files are available at `~/debug-targets/<worker-name>/`. Always sync before reading to ensure you have the latest data.

### Available data per target
- `SOUL.md`, `AGENTS.md` — Agent personality and instructions
- `openclaw.json` — Runtime configuration (model, plugins, channels)
- `.openclaw/agents/main/sessions/*.jsonl` — LLM session logs (conversation history)
- `.openclaw/identity/` — Agent identity metadata
- `skills/` — Active skill definitions
- `config/mcporter.json` — MCP server configuration

## Commands

### Sync Target Workspace
Pull the latest workspace of a specific target worker from OSS before analysis.

```bash
bash ~/skills/debug-analysis/scripts/sync-workspace.sh --worker <name>
bash ~/skills/debug-analysis/scripts/sync-workspace.sh --all
```

IMPORTANT: Always sync the target workspace BEFORE reading any files from it.

### Export Matrix Messages
Export recent Matrix room messages using the configured matrixCredential.

Export messages from a specific room (last 24 hours by default):
```bash
bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --room-id '<room_id>' --hours 24
```

Export messages from a room by name substring:
```bash
bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --room-name 'Worker' --hours 6
```

List all joined rooms:
```bash
bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --list-rooms
```

The output is JSONL format printed to stdout. Each line is a JSON object with fields: `event_id`, `type`, `sender`, `timestamp`, `time`, `body`.

**Note**: Matrix credentials must be configured in `~/debug-config.json` for message export to work. The homeserver URL is read from `openclaw.json`.

### Generate Debug Log
Aggregate session logs, Matrix messages, and state files into a structured debug report.

```bash
bash ~/skills/debug-analysis/scripts/generate-debug-log.sh \
  --worker <name> --hours 24
```

Generate a report with specific sections only:
```bash
bash ~/skills/debug-analysis/scripts/generate-debug-log.sh \
  --worker <name> --hours 24 \
  --include-sessions --include-matrix --include-state
```

Save report to a file:
```bash
bash ~/skills/debug-analysis/scripts/generate-debug-log.sh \
  --worker <name> --hours 24 \
  --output ~/debug-report.md
```

By default all sections are included: agent config, state files, LLM session logs, and Matrix messages. Use `--include-*` flags to select specific sections only. Output goes to stdout unless `--output` is specified.

### Analyze with Source Code
If `hiclawVersion` was specified when creating this DebugWorker, the hiclaw source code is available at `~/hiclaw-source/`. When investigating issues, cross-reference:
- Agent behavior rules: `manager/agent/*/AGENTS.md`
- Skill implementations: `manager/agent/skills/*/`
- Controller reconcile logic: `hiclaw-controller/internal/controller/`
- Worker config generation: `hiclaw-controller/internal/executor/`

## Debugging Workflow

1. **Sync**: `bash ~/skills/debug-analysis/scripts/sync-workspace.sh --all` — paste the output
2. **Read files**: `cat ~/debug-targets/<worker>/SOUL.md` and `cat ~/debug-targets/<worker>/openclaw.json` — paste relevant sections
3. **Session logs**: `ls -la ~/debug-targets/<worker>/.openclaw/agents/main/sessions/` — if files exist, `tail -20 <file>.jsonl`
4. **Matrix messages**: `bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --room-name '<worker>' --hours 24` — paste output
5. **Generate report**: `bash ~/skills/debug-analysis/scripts/generate-debug-log.sh --worker <name> --hours 24` — paste the full report
6. **Report** findings with the actual evidence from steps above. Never summarize without showing the raw data first.
