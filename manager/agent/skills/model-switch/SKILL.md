---
name: model-switch
description: Switch the Manager Agent's own LLM model. Use when the human admin requests changing the Manager's model.
---

# Model Switch

Switch the Manager's own LLM model. The script tests connectivity first, then hot-patches `openclaw.json` — OpenClaw reloads within ~300ms, no restart needed.

## Usage

```bash
bash /opt/hiclaw/agent/skills/model-switch/scripts/update-manager-model.sh <MODEL_ID> [--context-window <SIZE>] [--no-reasoning]
```

Examples:
```bash
bash /opt/hiclaw/agent/skills/model-switch/scripts/update-manager-model.sh claude-sonnet-4-6
bash /opt/hiclaw/agent/skills/model-switch/scripts/update-manager-model.sh my-custom-model --context-window 300000
bash /opt/hiclaw/agent/skills/model-switch/scripts/update-manager-model.sh deepseek-chat --no-reasoning
```

## What the script does

1. Strips any `hiclaw-gateway/` prefix from the model name
2. Resolves `contextWindow` and `maxTokens` for the model (uses `--context-window` override if provided)
3. Tests the model via `POST /v1/chat/completions` on the AI Gateway — exits with error if unreachable
4. Patches `openclaw.json`: updates `models[0].id/name/reasoning/contextWindow/maxTokens` and `agents.defaults.model.primary`

## Reasoning control

By default, reasoning (extended thinking) is enabled for all models. To disable it, pass `--no-reasoning`:

```bash
bash /opt/hiclaw/agent/skills/model-switch/scripts/update-manager-model.sh deepseek-chat --no-reasoning
```

This sets `"reasoning": false` in `openclaw.json`. Omitting the flag keeps reasoning enabled (`"reasoning": true`).

## On failure

If the gateway test fails (non-200), the script prints:

```
ERROR: Model test failed (HTTP <code>): <response>
The model '<name>' is not reachable via the AI Gateway.
Please check the Higress Console to confirm the AI route is configured for this model:
  http://<manager-host>:8001  →  AI Routes → verify provider and model mapping
```

No changes are made to `openclaw.json` in this case.

## Important

**NEVER use `session_status` tool to change the model** — that only affects the current session temporarily and does not persist. Always use this script.

## Supported models with known context windows

| Model | contextWindow | maxTokens |
|-------|--------------|-----------|
| gpt-5.4 | 1,050,000 | 128,000 |
| gpt-5.3-codex / gpt-5-mini / gpt-5-nano | 400,000 | 128,000 |
| claude-opus-4-6 | 1,000,000 | 128,000 |
| claude-sonnet-4-6 | 1,000,000 | 64,000 |
| claude-haiku-4-5 | 200,000 | 64,000 |
| qwen3.5-plus | 200,000 | 64,000 |
| deepseek-chat / deepseek-reasoner / kimi-k2.5 | 256,000 | 128,000 |
| glm-5 / MiniMax-M2.5 / MiniMax-M2.5-highspeed | 200,000 | 128,000 |
| *(other)* | 150,000 | 128,000 |

## Switching to an unknown model

When the human admin requests switching to a model **not listed in the table above**, you MUST:

1. **Ask the admin for the model's context window size** before running the script. Example: "This model is not in the known list. What is its context window size (in tokens)?"
2. Once the admin provides the context window, run the script with `--context-window`:
   ```bash
   bash /opt/hiclaw/agent/skills/model-switch/scripts/update-manager-model.sh <MODEL_ID> --context-window <SIZE>
   ```
3. If the admin does not know the context window, use the default (150,000) by omitting `--context-window`.
4. If the admin wants to disable reasoning, add `--no-reasoning` to the command.
