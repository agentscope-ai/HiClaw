---
name: mcp-server-management
description: Manage MCP Servers on the Higress AI Gateway -- create, update, list, delete servers, and control consumer access. Use when configuring MCP tool servers (e.g., GitHub, Amap) or granting/revoking worker access to MCP tools.
---

# MCP Server Management

## Overview

You manage MCP Servers on the Higress AI Gateway. MCP Servers expose REST APIs as tools that you and Workers can invoke. Use the unified setup script to create/update servers from YAML templates, and the curl/mcporter commands below for listing, deleting, calling tools, and fine-grained access control.

> **Cloud deployment (SAE) note:** The `setup-mcp-server.sh` script and Higress Console API commands in this document are only available in local deployment mode. In cloud mode (`HICLAW_RUNTIME=aliyun`), MCP Servers are managed through the Alibaba Cloud AI Gateway console. Cloud MCP Server management via script will be supported in a future release. If the admin asks to configure an MCP Server in cloud mode, direct them to the AI Gateway console.

Built-in YAML templates are at `/opt/hiclaw/agent/skills/mcp-server-management/references/mcp-*.yaml`.

## Environment Variables

```bash
HICLAW_AI_GATEWAY_DOMAIN  # AI Gateway domain (e.g., aigw-local.hiclaw.io)
HIGRESS_COOKIE_FILE       # Session cookie file for Higress Console
HICLAW_GITHUB_TOKEN       # GitHub PAT (may be empty if not configured during installation)
```

## Create / Update MCP Server (Script)

Use the unified script for all MCP server creation and updates:

```bash
bash /opt/hiclaw/agent/skills/mcp-server-management/scripts/setup-mcp-server.sh <server-name> <credential-value> [--yaml-file <path>] [--api-domain <domain>]
```

### Arguments

| Argument | Required | Description |
|---|---|---|
| `server-name` | yes | MCP server name without `mcp-` prefix (e.g., `github`, `weather`) |
| `credential-value` | yes | The credential (e.g., GitHub PAT, API key) |
| `--yaml-file` | no | Path to a user-provided YAML config file. Required when no built-in template exists. |
| `--api-domain` | no | Explicit API domain for DNS service source. If omitted, auto-extracted from the YAML's first `requestTemplate.url`. Required when URLs use variables instead of literal domains. |

The credential key is always `accessToken` — all YAML configs must use `accessToken: ""` in `server.config`. The script substitutes it with the real credential value.

### Examples

```bash
# Built-in template: GitHub — domain auto-extracted (api.github.com)
bash /opt/hiclaw/agent/skills/mcp-server-management/scripts/setup-mcp-server.sh github "ghp_xxxxxxxxxxxx"

# User-provided YAML: custom weather service
bash /opt/hiclaw/agent/skills/mcp-server-management/scripts/setup-mcp-server.sh weather "my-key" \
    --yaml-file /tmp/mcp-weather.yaml --api-domain "api.weather.com"
```

### YAML Resolution

The script resolves the YAML config in this order:
1. `--yaml-file` flag (highest priority — user-provided file)
2. Built-in template at `references/mcp-<server-name>.yaml`
3. If neither exists, exits with an error listing available built-in templates

### Domain Resolution

The script determines the API domain in this order:
1. `--api-domain` flag (highest priority)
2. Auto-extracted from the first `requestTemplate.url` in the YAML
3. If neither yields a valid domain, exits with an error asking to re-run with `--api-domain`

### What the Script Does

1. Determines the API domain (`--api-domain` flag or auto-extracted from YAML) and registers a DNS service source
2. Reads the YAML config, substitutes `accessToken: ""` with the real credential
3. Creates/updates the MCP Server via `PUT /v1/mcpServer` (upsert) and authorizes your consumer
4. Updates your `./config/mcporter.json` so you can call the new tools via `mcporter`
5. Reads `~/workers-registry.json`, authorizes all existing Workers, and updates/creates each Worker's `config/mcporter.json` (pushed to MinIO)

The script is fully idempotent — safe to re-run for credential rotation or updates.

### When to Use

- User provides a credential via chat (e.g., "here is my GitHub token: ghp_xxx")
- User asks you to enable a new integration or rotate a credential
- User provides a custom YAML config for a service not in the built-in templates
- `HICLAW_GITHUB_TOKEN` was empty during installation and user provides it later

## Custom MCP Services (User-Provided YAML)

Only `mcp-github.yaml` is built-in. For any other service, generate the YAML config based on the user's API description, then deploy it with the setup script.

### End-to-End Workflow

1. User describes the HTTP API they want to add (endpoints, auth method, parameters)
2. You generate the YAML config following the format spec below
3. Write the YAML to `/tmp/mcp-<name>.yaml`
4. Run `setup-mcp-server.sh` with `--yaml-file`
5. Confirm to the user

### Generating YAML from API Description

When a user says something like "I want to add a weather API, the endpoint is `GET https://api.weather.com/v1/forecast?city=xxx`, auth via `X-API-Key` header", generate the YAML config following this spec:

#### YAML Structure

```yaml
server:
  name: <server-name>-mcp-server
  config:
    accessToken: ""    # Unified credential key — setup script substitutes the real value
  # allowTools:             # Optional: restrict which tools are exposed
  #   - tool_name_1
  #   - tool_name_2

tools:
- name: <tool_name>
  description: "<clear description of what this tool does>"
  args:
  - name: <arg_name>
    description: "<arg description>"
    type: string            # string | number | integer | boolean | array | object
    required: true           # true | false
    # default: <value>      # Optional default value
    # position: query       # Optional: query | path | header | cookie | body
    # For array type:
    # items:
    #   type: string
    # For object type:
    # properties:
    #   subfield1:
    #     type: string
  requestTemplate:
    url: "https://<api-domain>/path/{{.args.<arg>}}"
    method: GET              # GET | POST | PUT | DELETE | PATCH
    headers:
    - key: Authorization
      value: "Bearer {{.config.accessToken}}"
    # Request body — choose ONE of these approaches:
    # Option A: Auto-serialize all args to URL query params
    # argsToUrlParam: true
    # Option B: Auto-serialize all args to JSON body
    # argsToJsonBody: true
    # Option C: Auto-serialize all args to form-encoded body
    # argsToFormBody: true
    # Option D: Manual body template (most flexible)
    # body: |
    #   {
    #     "param1": "{{.args.arg1}}",
    #     "param2": {{.args.arg2}},
    #     "complex": {{toJson .args.arg3}}
    #   }
  # responseTemplate:       # Optional: transform API response for readability
  #   body: |
  #     {{- range $i, $item := .results }}
  #     - **{{$item.name}}**: {{$item.value}}
  #     {{- end }}
  #   # Or prepend/append context to raw JSON:
  #   # prependBody: |
  #   #   # Response field meanings:
  #   #   - field1: description
  #   # appendBody: |
  #   #   Use this data to...
```

#### Template Syntax Reference

The YAML uses GJSON Template syntax (Go templates + GJSON paths + Sprig functions):

| Syntax | Description | Example |
|---|---|---|
| `{{.args.<name>}}` | Reference a tool argument | `{{.args.city}}` |
| `{{.config.<key>}}` | Reference server config value | `{{.config.accessToken}}` |
| `{{toJson .args.<name>}}` | Serialize array/object arg to JSON | `{{toJson .args.filters}}` |
| `{{.args.<str> \| b64enc}}` | Base64-encode a string | `{{.args.content \| b64enc}}` |
| `{{add $index 1}}` | Sprig math functions | `Item {{add $index 1}}` |
| `{{upper .args.name}}` | Sprig string functions | `{{upper .args.code}}` |
| `{{gjson "path.to.field"}}` | GJSON path query on response | `{{gjson "users.#.name"}}` |
| `{{if .args.opt}}...{{end}}` | Conditional | Optional params |
| `{{range .items}}...{{end}}` | Loop | Format lists |

#### Parameter Passing: Choose the Right Approach

| Approach | When to Use |
|---|---|
| URL with `{{.args.*}}` inline | GET requests with few params in path/query |
| `argsToUrlParam: true` | GET requests with many query params — auto-appends all args |
| `argsToJsonBody: true` | POST/PUT with JSON body — auto-serializes all args |
| `argsToFormBody: true` | POST with form-encoded body |
| `body: \|` template | Complex body structure, conditional fields, nested objects |

#### Generation Guidelines

1. Use descriptive `name` for tools (snake_case, e.g., `get_forecast`, `search_users`)
2. Write clear `description` — this is what the LLM sees when deciding which tool to use
3. Mark `required: true` only for truly required params; use `default` for optional ones
4. Choose the simplest parameter passing approach that works
5. Add `responseTemplate` only when the raw JSON response is too verbose or hard to read — for simple APIs, omit it and let the raw response pass through
6. Always use `accessToken` as the credential key in `server.config` — this is the unified convention
7. Always leave the credential value as `""` — the setup script handles substitution

### Example: User Says "Add a Weather API"

User: "I want to add a weather API. Endpoint: `GET https://api.openweather.com/v1/weather?q={city}&units={units}`, auth via `X-API-Key` header."

You generate and write:

```bash
cat > /tmp/mcp-weather.yaml << 'YAML'
server:
  name: weather-mcp-server
  config:
    accessToken: ""
tools:
- name: get_weather
  description: "Get current weather for a city"
  args:
  - name: city
    description: "City name (e.g., London, Tokyo)"
    type: string
    required: true
  - name: units
    description: "Temperature units"
    type: string
    required: false
    default: "metric"
  requestTemplate:
    url: "https://api.openweather.com/v1/weather?q={{.args.city}}&units={{.args.units}}"
    method: GET
    headers:
    - key: X-API-Key
      value: "{{.config.accessToken}}"
YAML

bash /opt/hiclaw/agent/skills/mcp-server-management/scripts/setup-mcp-server.sh \
    weather "<user-provided-key>" --yaml-file /tmp/mcp-weather.yaml
```

### After Running

1. Wait ~10s for the auth plugin to activate
2. Use your **mcporter skill** to verify the new MCP server works correctly before notifying Workers:
   - `mcporter list` — confirm the server appears and is healthy
   - `mcporter list <server-name> --schema` — review available tools
   - Call at least one representative tool to verify end-to-end connectivity and correct responses
   - If any test fails, debug and fix before proceeding — do not push broken tools to Workers
3. Confirm to the user that the MCP server is configured and tested
4. Notify relevant Workers — do **not** broadcast to all Workers. Only @mention Workers that:
   - The user explicitly asked you to notify, or
   - You judge will need this tool based on their current task or role
   
   Message format:
   ```
   @{worker}:{domain} New MCP server `{mcp-server-name}` has been configured with tools: {tool list from YAML}.
   Please use your file-sync skill to pull the updated config, then use your mcporter skill to discover and learn the new tools.
   ```
   The tool list should be extracted from the YAML config's `tools[].name` fields so the Worker knows what's available.

### Security

- **Never echo credentials** in chat messages
- Credentials are stored only in Higress MCP Server config — Workers never see them directly
- Workers access MCP servers through the gateway proxy using their own key-auth tokens

## Built-in Templates

| Template | Server Name | Description |
|---|---|---|
| `mcp-github.yaml` | `mcp-github` | GitHub: repos, issues, PRs, code search |

Only GitHub has a built-in template. All other services require user-provided YAML via `--yaml-file`. All YAML configs use `accessToken` as the unified credential key.

## List MCP Servers

```bash
curl -s http://127.0.0.1:8001/v1/mcpServers -b "${HIGRESS_COOKIE_FILE}" | jq
```

## Get MCP Server Details

```bash
curl -s "http://127.0.0.1:8001/v1/mcpServer?name=<mcp-server-name>" -b "${HIGRESS_COOKIE_FILE}" | jq
```

## Delete MCP Server

```bash
curl -X DELETE "http://127.0.0.1:8001/v1/mcpServer?name=<mcp-server-name>" -b "${HIGRESS_COOKIE_FILE}"
```

## Consumer Authorization

The setup script handles authorization automatically. For manual adjustments:

```bash
# Authorize consumers (REPLACE operation — include ALL consumers)
curl -X PUT http://127.0.0.1:8001/v1/mcpServer/consumers \
  -b "${HIGRESS_COOKIE_FILE}" \
  -H 'Content-Type: application/json' \
  -d '{"mcpServerName":"<name>","consumers":["manager","worker-alice"]}'

# Revoke a consumer from ALL MCP servers
curl -X DELETE "http://127.0.0.1:8001/v1/mcpServer/consumers?consumer=worker-alice" \
  -b "${HIGRESS_COOKIE_FILE}"
```

## Tool-Level Access Control

Add `allowTools` to the YAML template's `server` section to restrict exposed tools:

```yaml
server:
  name: github-mcp-server
  config:
    accessToken: "..."
  allowTools:
    - search_repositories
    - get_file_contents
```

When `allowTools` is set, only listed tools are available. Re-run the setup script to apply changes.

## Important Notes

- MCP server creation/update takes ~10s for the auth plugin to activate
- MCP Server SSE endpoint always returns 200; auth is checked on `POST /mcp/<name>/message`
- See the "Template Syntax Reference" table above for the full YAML template syntax
