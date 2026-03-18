# Import Worker Guide

Import pre-configured Workers into HiClaw — either by migrating a standalone OpenClaw instance or by importing a community Worker template.

## Overview

The Worker Import system consists of two parts:

1. **Import Script** (`hiclaw-import.sh` / `hiclaw-import.ps1`) — runs on the HiClaw host, takes a Worker package (ZIP), performs all registration and configuration, then tells the Manager to start the container
2. **Migration Skill** (`migrate/skill/`) — runs on a standalone OpenClaw instance, analyzes its environment and generates a compatible Worker package

The Worker package is a ZIP file containing configuration files and optionally a Dockerfile for custom image builds. When no Dockerfile is included, the standard HiClaw Worker image is used.

## Worker Package Format

A Worker package ZIP has the following structure:

```
worker-package.zip
├── manifest.json           # Package metadata (required)
├── Dockerfile              # Custom image build (optional)
├── config/
│   ├── SOUL.md             # Worker identity and role
│   ├── AGENTS.md           # Custom agent configuration
│   ├── MEMORY.md           # Long-term memory
│   └── memory/             # Memory files
├── skills/                 # Custom skills
│   └── <skill-name>/
│       └── SKILL.md
├── crons/
│   └── jobs.json           # Scheduled tasks
└── tool-analysis.json      # Tool dependency report (informational)
```

### manifest.json

```json
{
  "version": "1.0",
  "source": {
    "openclaw_version": "2026.3.x",
    "hostname": "my-server",
    "os": "Ubuntu 22.04",
    "created_at": "2026-03-18T10:00:00Z"
  },
  "worker": {
    "suggested_name": "my-worker",
    "base_image": "hiclaw/worker-agent:latest",
    "apt_packages": ["ffmpeg", "imagemagick"],
    "pip_packages": [],
    "npm_packages": []
  },
  "proxy": {
    "suggested": false,
    "reason": ""
  }
}
```

## Scenario 1: Migrate a Standalone OpenClaw

If you have an existing OpenClaw instance running on a server and want to bring it under HiClaw management as a Worker, follow these steps.

### Step 1: Install the Migration Skill on the Source OpenClaw

Copy the `migrate/skill/` directory to your OpenClaw's skills folder:

```bash
cp -r migrate/skill/ ~/.openclaw/workspace/skills/hiclaw-migrate/
```

Or ask your OpenClaw to install it:

```
Install the hiclaw-migrate skill from /path/to/hiclaw/migrate/skill/
```

### Step 2: Generate the Migration Package

Ask your OpenClaw to analyze its environment and generate the migration package:

```
Analyze my current setup and generate a HiClaw migration package.
```

The OpenClaw will read the migration skill's instructions, understand HiClaw's Worker architecture, and then:

1. Run `analyze.sh` to scan tool dependencies (skill scripts, shell history, cron payloads, AGENTS.md code blocks)
2. Intelligently adapt your AGENTS.md — keeping your custom role and behavior definitions while removing parts that conflict with HiClaw's builtin Worker configuration (communication protocol, file sync, task execution rules, etc.)
3. Adapt SOUL.md for HiClaw's Worker identity format
4. Generate a Dockerfile that extends the HiClaw Worker base image with your required system tools
5. Package everything into a ZIP and output the file path

This step requires the OpenClaw AI to be involved — the scripts alone cannot intelligently adapt your configuration. The OpenClaw reads the SKILL.md to understand HiClaw's conventions and makes informed decisions about what to keep, modify, or remove.

### Step 3: Review the Package (Recommended)

Before importing, review the generated files:

```bash
unzip -l /tmp/hiclaw-migration/migration-my-worker-*.zip
```

Check `tool-analysis.json` to verify the detected dependencies are correct. Edit the Dockerfile if needed — you can add or remove packages.

### Step 4: Transfer and Import

Transfer the ZIP to the HiClaw Manager host, then run:

```bash
bash hiclaw-import.sh --zip migration-my-worker-20260318-100000.zip
```

The script will:
1. Build a custom Worker image from the Dockerfile
2. Register a Matrix account and create a communication room
3. Create a MinIO user with scoped permissions
4. Configure Higress Gateway consumer and route authorization
5. Generate openclaw.json and push all config to MinIO
6. Update the Manager's workers-registry.json
7. Send a message to the Manager to start the Worker container

### Step 5: Verify

After the script completes, check the Worker in Element Web. The Manager will start the container and the Worker should appear online within a minute.

### What Gets Migrated

| Item | Migrated | Notes |
|------|----------|-------|
| SOUL.md / AGENTS.md | Yes | Adapted for HiClaw format |
| Custom skills | Yes | Placed in `custom-skills/` |
| Cron jobs | Yes | Converted to HiClaw scheduled tasks |
| Memory files | Yes | MEMORY.md and daily notes |
| System tool dependencies | Yes | Installed via custom Dockerfile |
| API keys / auth profiles | No | HiClaw uses its own AI Gateway credentials |
| Device identity | No | New identity generated during registration |
| Conversation sessions | No | Sessions reset daily in HiClaw |
| Discord/Slack channel config | No | HiClaw uses Matrix |

## Scenario 2: Import a Worker Template

Worker templates are pre-built packages that define a Worker's role, skills, and tool dependencies. They can be shared within a team or published to the community.

### Import from a Local ZIP

```bash
bash hiclaw-import.sh --zip devops-worker-template.zip --name devops-alice
```

### Import from a URL

```bash
bash hiclaw-import.sh --zip https://example.com/templates/devops-worker.zip --name devops-alice
```

### Template Without Dockerfile

If the template ZIP does not include a Dockerfile, the standard HiClaw Worker image (`hiclaw/worker-agent`) is used. This is suitable for Workers that only need the built-in tools (git, curl, jq, Node.js, Python, etc.).

```bash
# This works fine — no custom image build needed
bash hiclaw-import.sh --zip simple-worker-template.zip --name bob
```

### Creating a Worker Template

To create a shareable Worker template:

1. Create a `manifest.json`:

```json
{
  "version": "1.0",
  "source": {
    "hostname": "template",
    "os": "N/A",
    "created_at": "2026-03-18T00:00:00Z"
  },
  "worker": {
    "suggested_name": "devops-worker",
    "base_image": "hiclaw/worker-agent:latest",
    "apt_packages": [],
    "pip_packages": [],
    "npm_packages": []
  }
}
```

2. Create `config/SOUL.md` with the Worker's role definition:

```markdown
# DevOps Worker

## AI Identity

**You are an AI Agent, not a human.**

## Role
- Name: devops-worker
- Specialization: CI/CD pipeline management, infrastructure monitoring, deployment automation
- Skills: GitHub Operations, shell scripting, Docker, Kubernetes

## Behavior
- Monitor CI/CD pipelines proactively
- Alert on failures immediately
- Automate routine deployment tasks
```

3. Optionally add `config/AGENTS.md` with custom instructions, `skills/` with custom skill definitions, and a `Dockerfile` if extra tools are needed.

4. Package it:

```bash
cd my-template-dir/
zip -r devops-worker-template.zip manifest.json config/ skills/ Dockerfile
```

## Command Reference

### hiclaw-import.sh (Bash — macOS/Linux)

```bash
bash hiclaw-import.sh --zip <path-or-url> [options]
```

| Option | Description | Default |
|--------|-------------|---------|
| `--zip <path\|url>` | Worker package ZIP (local path or URL) | Required |
| `--name <name>` | Worker name | From manifest |
| `--proxy <url>` | HTTP proxy for Worker runtime | None |
| `--no-proxy <domains>` | Additional domains to bypass proxy | None |
| `--env-file <path>` | HiClaw env file path | `~/hiclaw-manager.env` |
| `--base-image <image>` | Override base image for Dockerfile build | From manifest |
| `--skip-build` | Skip Docker image build | Off |
| `--yes` | Skip interactive confirmations | Off |

### hiclaw-import.ps1 (PowerShell — Windows)

```powershell
.\hiclaw-import.ps1 -Zip <path-or-url> [-Name <name>] [-Proxy <url>] [-NoProxy <domains>] [-EnvFile <path>] [-BaseImage <image>] [-SkipBuild] [-Yes]
```

Parameters mirror the Bash version.

## HTTP Proxy Configuration

For Workers running in China or behind a corporate firewall, use `--proxy` to configure runtime HTTP proxy:

```bash
bash hiclaw-import.sh --zip worker.zip --proxy http://192.168.1.100:7890
```

The proxy is set as environment variables (`HTTP_PROXY`, `HTTPS_PROXY`) in the Worker container. The following domains are automatically excluded from proxy (`NO_PROXY`):

- `*.hiclaw.io` (all HiClaw internal domains)
- `127.0.0.1`, `localhost`
- Manager's Matrix, AI Gateway, and MinIO domains

Use `--no-proxy` to add extra domains:

```bash
bash hiclaw-import.sh --zip worker.zip \
    --proxy http://192.168.1.100:7890 \
    --no-proxy "*.internal.company.com,10.0.0.0/8"
```

Note: The proxy is for Worker runtime only. During image build, proxy is passed as Docker build args and cleared in the final image.

## Troubleshooting

### Import script fails at "Checking Manager container"

The HiClaw Manager container must be running. Start it with:

```bash
docker start hiclaw-manager
```

### Image build fails

Check the Dockerfile in the ZIP package. Common issues:
- Package names may differ between Ubuntu versions
- pip/npm packages may have been renamed or removed

You can edit the Dockerfile in the extracted ZIP and retry, or use `--skip-build` with a pre-built image.

### Worker starts but doesn't respond

1. Check Worker container logs: `docker logs hiclaw-worker-<name>`
2. Verify the Worker appears in Element Web in its dedicated room
3. Ensure the Manager's `workers-registry.json` has the correct entry
4. Try sending `@<worker-name>:<matrix-domain> hello` in the Worker's room
