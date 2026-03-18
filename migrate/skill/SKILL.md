---
name: hiclaw-migrate
description: Analyze current OpenClaw setup and generate a migration package (ZIP) for importing into HiClaw as a managed Worker
---

# HiClaw Migration Skill

This skill helps you analyze your current OpenClaw environment and generate a migration package that can be imported into a HiClaw Manager as a managed Worker.

## What It Does

1. **Analyzes** your current configuration: skills, cron jobs, workspace files, system tool dependencies
2. **Generates** a migration ZIP package containing:
   - Adapted AGENTS.md and SOUL.md for HiClaw Worker format
   - Custom skills (excluding HiClaw built-in ones)
   - Adapted cron job definitions
   - Memory files
   - A Dockerfile that extends the HiClaw Worker base image with your required system tools
   - A manifest.json with migration metadata

## Usage

### Quick Start

Run the analysis script to scan your environment:

```bash
bash /path/to/hiclaw-migrate/scripts/analyze.sh
```

This produces a `tool-analysis.json` in the output directory with detected dependencies.

Then generate the ZIP package:

```bash
bash /path/to/hiclaw-migrate/scripts/generate-zip.sh \
    --name <suggested-worker-name> \
    --output /tmp/migration-output
```

### Parameters

**analyze.sh**:
- `--state-dir <path>`: OpenClaw state directory (default: `~/.openclaw`)
- `--output <dir>`: Output directory for analysis results (default: `/tmp/hiclaw-migration`)

**generate-zip.sh**:
- `--name <name>`: Suggested worker name (default: hostname)
- `--state-dir <path>`: OpenClaw state directory (default: `~/.openclaw`)
- `--analysis <path>`: Path to tool-analysis.json from analyze.sh
- `--output <dir>`: Output directory (default: `/tmp/hiclaw-migration`)
- `--base-image <image>`: HiClaw Worker base image (default: `hiclaw/worker-agent:latest`)

### Output

The ZIP file will be at `<output>/migration-<name>-<timestamp>.zip`. Transfer this file to the HiClaw Manager host and run:

```bash
bash hiclaw-import.sh --zip migration-<name>-<timestamp>.zip
```

## What Is NOT Migrated

- **Auth profiles / API keys**: HiClaw uses its own AI Gateway with per-worker credentials
- **Device identity**: New identity is generated during Worker creation
- **Sessions**: Conversation history is not transferred (sessions reset daily in HiClaw)
- **Extensions**: HiClaw Workers use a different plugin system; only custom skills are migrated
