#!/bin/bash
# create-worker.sh - One-shot Worker creation script
#
# Automates the full Worker lifecycle: Matrix registration, room creation,
# Higress consumer setup, AI route & MCP authorization, config generation,
# MinIO sync, skills push, and container startup.
#
# Usage:
#   create-worker.sh --name <NAME> [--model <MODEL_ID>] [--mcp-servers s1,s2] [--skills s1,s2] [--find-skills] [--skills-api-url <URL>] [--remote]
#
# Prerequisites:
#   - SOUL.md must already exist at /root/hiclaw-fs/agents/<NAME>/SOUL.md
#   - Environment: HICLAW_REGISTRATION_TOKEN, HICLAW_MATRIX_DOMAIN,
#     HICLAW_AI_GATEWAY_DOMAIN, HICLAW_ADMIN_USER, HIGRESS_COOKIE_FILE,
#     MANAGER_MATRIX_TOKEN

set -e
source /opt/hiclaw/scripts/lib/hiclaw-env.sh
source /opt/hiclaw/scripts/lib/container-api.sh
source /opt/hiclaw/scripts/lib/gateway-api.sh

# Override log() to also write to container's main stdout (/proc/1/fd/1)
# so that logs are visible in `docker logs` / SAE log viewer even when
# this script is executed by OpenClaw's exec tool (which captures stdout).
log() {
    local msg="[hiclaw $(date '+%Y-%m-%d %H:%M:%S')] $1"
    echo "${msg}"
    # Write to PID 1's stdout if available (container main process)
    if [ -w /proc/1/fd/1 ]; then
        echo "${msg}" > /proc/1/fd/1
    fi
}

# ============================================================
# Parse arguments
# ============================================================
WORKER_NAME=""
MODEL_ID=""
MCP_SERVERS=""
WORKER_SKILLS="file-sync,mcporter"
REMOTE_MODE=false
ENABLE_FIND_SKILLS=false
SKILLS_API_URL=""
WORKER_RUNTIME="${HICLAW_DEFAULT_WORKER_RUNTIME:-openclaw}"   # openclaw | copaw
CONSOLE_PORT=""             # copaw only: web console port (e.g. 8088)

while [ $# -gt 0 ]; do
    case "$1" in
        --name)       WORKER_NAME="$2"; shift 2 ;;
        --model)      MODEL_ID="$2"; shift 2 ;;
        --mcp-servers) MCP_SERVERS="$2"; shift 2 ;;
        --skills)     WORKER_SKILLS="$2"; shift 2 ;;
        --find-skills) ENABLE_FIND_SKILLS=true; shift ;;
        --skills-api-url) SKILLS_API_URL="$2"; shift 2 ;;
        --remote)     REMOTE_MODE=true; shift ;;
        --runtime)    WORKER_RUNTIME="$2"; shift 2 ;;
        --console-port) CONSOLE_PORT="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [ -z "${WORKER_NAME}" ]; then
    echo "Usage: create-worker.sh --name <NAME> [--model <MODEL_ID>] [--mcp-servers s1,s2] [--skills s1,s2] [--find-skills] [--skills-api-url <URL>] [--remote] [--runtime openclaw|copaw] [--console-port <PORT>]"
    exit 1
fi

# Normalize worker name to lowercase
# Tuwunel (Matrix server) stores usernames in lowercase, so we must ensure
# consistency to avoid issues when inviting workers to rooms.
WORKER_NAME=$(echo "${WORKER_NAME}" | tr 'A-Z' 'a-z')

# copaw runtime supports both container and pip-installed modes
# (previously forced REMOTE_MODE=true; now containers are supported)

# If find-skills is enabled, add it to the skills list
# Fallback: if HICLAW_SKILLS_API_URL env is set and no --skills-api-url was passed, use it
if [ -z "${SKILLS_API_URL}" ] && [ -n "${HICLAW_SKILLS_API_URL}" ]; then
    SKILLS_API_URL="${HICLAW_SKILLS_API_URL}"
fi
if [ "${ENABLE_FIND_SKILLS}" = true ]; then
    if ! echo "${WORKER_SKILLS}" | grep -q '\bfind-skills\b'; then
        WORKER_SKILLS="${WORKER_SKILLS},find-skills"
    fi
fi

MATRIX_DOMAIN="${HICLAW_MATRIX_DOMAIN:-matrix-local.hiclaw.io:8080}"
ADMIN_USER="${HICLAW_ADMIN_USER:-admin}"
CONSUMER_NAME="worker-${WORKER_NAME}"
SOUL_FILE="/root/hiclaw-fs/agents/${WORKER_NAME}/SOUL.md"

if [ ! -f "${SOUL_FILE}" ]; then
    cat << EOF
{"error": "SOUL.md not found at ${SOUL_FILE}", "hint": "Create it first with:"}
---HINT---
mkdir -p /root/hiclaw-fs/agents/${WORKER_NAME}
cat > /root/hiclaw-fs/agents/${WORKER_NAME}/SOUL.md << 'SOULEOF'
# ${WORKER_NAME} - Worker Agent

## AI Identity

**You are an AI Agent, not a human.**

- Both you and the Manager are AI agents that can work 24/7
- You do not need rest, sleep, or "off-hours"
- You can immediately start the next task after completing one
- Your time units are **minutes and hours**, not "days"

## Role
- Name: ${WORKER_NAME}
- Role: <describe the worker's role>

## Behavior
- Be helpful and concise
SOULEOF
---END---
EOF
    exit 1
fi

_fail() {
    echo '{"error": "'"$1"'"}'
    exit 1
}

# ============================================================
# Ensure credentials are available
# ============================================================
SECRETS_FILE="/data/hiclaw-secrets.env"
if [ -f "${SECRETS_FILE}" ]; then
    source "${SECRETS_FILE}"
fi

if [ -z "${MANAGER_MATRIX_TOKEN}" ]; then
    MANAGER_PASSWORD="${HICLAW_MANAGER_PASSWORD:-}"
    if [ -z "${MANAGER_PASSWORD}" ]; then
        _fail "MANAGER_MATRIX_TOKEN not set and HICLAW_MANAGER_PASSWORD not available"
    fi
    MANAGER_MATRIX_TOKEN=$(curl -sf -X POST ${HICLAW_MATRIX_SERVER}/_matrix/client/v3/login \
        -H 'Content-Type: application/json' \
        -d '{"type":"m.login.password","identifier":{"type":"m.id.user","user":"manager"},"password":"'"${MANAGER_PASSWORD}"'"}' \
        2>/dev/null | jq -r '.access_token // empty')
    if [ -z "${MANAGER_MATRIX_TOKEN}" ]; then
        _fail "Failed to obtain Manager Matrix token"
    fi
    log "Obtained Manager Matrix token via login"
fi

gateway_ensure_session || _fail "Failed to establish gateway session"

# ============================================================
# Step 1: Register Matrix Account
# ============================================================
log "Step 1: Registering Matrix account for ${WORKER_NAME}..."
WORKER_USER_ID="@${WORKER_NAME}:${MATRIX_DOMAIN}"
WORKER_CREDS_FILE="/data/worker-creds/${WORKER_NAME}.env"
mkdir -p /data/worker-creds

# Reuse persisted password if available, otherwise generate new
if [ -f "${WORKER_CREDS_FILE}" ]; then
    source "${WORKER_CREDS_FILE}"
    log "  Loaded persisted credentials for ${WORKER_NAME}"
else
    WORKER_PASSWORD=$(generateKey 16)
fi
[ -z "${WORKER_MINIO_PASSWORD}" ] && WORKER_MINIO_PASSWORD=$(generateKey 24)

REG_RESP=$(curl -s -X POST ${HICLAW_MATRIX_SERVER}/_matrix/client/v3/register \
    -H 'Content-Type: application/json' \
    -d '{
        "username": "'"${WORKER_NAME}"'",
        "password": "'"${WORKER_PASSWORD}"'",
        "auth": {
            "type": "m.login.registration_token",
            "token": "'"${HICLAW_REGISTRATION_TOKEN}"'"
        }
    }' 2>/dev/null) || true

if echo "${REG_RESP}" | jq -e '.access_token' > /dev/null 2>&1; then
    WORKER_MATRIX_TOKEN=$(echo "${REG_RESP}" | jq -r '.access_token')
    log "  Registered new account: ${WORKER_USER_ID}"
else
    # Account already exists — login with persisted password
    log "  Account exists, logging in..."
    LOGIN_RESP=$(curl -s -X POST ${HICLAW_MATRIX_SERVER}/_matrix/client/v3/login \
        -H 'Content-Type: application/json' \
        -d '{
            "type": "m.login.password",
            "identifier": {"type": "m.id.user", "user": "'"${WORKER_NAME}"'"},
            "password": "'"${WORKER_PASSWORD}"'"
        }' 2>/dev/null) || true

    if echo "${LOGIN_RESP}" | jq -e '.access_token' > /dev/null 2>&1; then
        WORKER_MATRIX_TOKEN=$(echo "${LOGIN_RESP}" | jq -r '.access_token')
        log "  Logged in: ${WORKER_USER_ID}"
    else
        _fail "Failed to register or login Matrix account for ${WORKER_NAME}. If re-creating, delete /data/worker-creds/${WORKER_NAME}.env and try again."
    fi
fi

# Pre-generate gateway key if not loaded from persisted creds (for new workers)
[ -z "${WORKER_GATEWAY_KEY}" ] && WORKER_GATEWAY_KEY=$(generateKey 32)

# Persist credentials for future re-creation
cat > "${WORKER_CREDS_FILE}" <<CREDS
WORKER_PASSWORD="${WORKER_PASSWORD}"
WORKER_MINIO_PASSWORD="${WORKER_MINIO_PASSWORD}"
WORKER_GATEWAY_KEY="${WORKER_GATEWAY_KEY}"
CREDS
chmod 600 "${WORKER_CREDS_FILE}"

# ============================================================
# Step 1b: Create storage user with restricted permissions
# ============================================================
if [ "${HICLAW_RUNTIME}" != "aliyun" ]; then
    log "Step 1b: Creating MinIO user for ${WORKER_NAME}..."
    POLICY_NAME="worker-${WORKER_NAME}"
    POLICY_FILE=$(mktemp /tmp/minio-policy-XXXXXX.json)
    cat > "${POLICY_FILE}" <<POLICY
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": ["arn:aws:s3:::hiclaw-storage"],
      "Condition": {
        "StringLike": {
          "s3:prefix": [
            "agents/${WORKER_NAME}", "agents/${WORKER_NAME}/*",
            "shared", "shared/*"
          ]
        }
      }
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": [
        "arn:aws:s3:::hiclaw-storage/agents/${WORKER_NAME}/*",
        "arn:aws:s3:::hiclaw-storage/shared/*"
      ]
    }
  ]
}
POLICY
    mc admin user add hiclaw "${WORKER_NAME}" "${WORKER_MINIO_PASSWORD}" 2>/dev/null || true
    mc admin policy remove hiclaw "${POLICY_NAME}" 2>/dev/null || true
    mc admin policy create hiclaw "${POLICY_NAME}" "${POLICY_FILE}"
    mc admin policy attach hiclaw "${POLICY_NAME}" --user "${WORKER_NAME}"
    rm -f "${POLICY_FILE}"
    log "  MinIO user ${WORKER_NAME} created with policy ${POLICY_NAME}"
else
    log "Step 1b: Skipped (cloud mode uses RRSA for storage auth)"
fi

# ============================================================
# Step 2: Create Matrix Room (3-party)
# ============================================================
log "Step 2: Creating Matrix room..."
MANAGER_MATRIX_ID="@manager:${MATRIX_DOMAIN}"
ADMIN_MATRIX_ID="@${ADMIN_USER}:${MATRIX_DOMAIN}"
# Build initial_state for room creation: add E2EE encryption state if enabled
ROOM_E2EE_INITIAL_STATE=""
if [ "${HICLAW_MATRIX_E2EE:-0}" = "1" ] || [ "${HICLAW_MATRIX_E2EE:-}" = "true" ]; then
    ROOM_E2EE_INITIAL_STATE=',"initial_state":[{"type":"m.room.encryption","state_key":"","content":{"algorithm":"m.megolm.v1.aes-sha2"}}]'
    log "  E2EE enabled: adding m.room.encryption to room initial_state"
fi

ROOM_RESP=$(curl -sf -X POST ${HICLAW_MATRIX_SERVER}/_matrix/client/v3/createRoom \
    -H "Authorization: Bearer ${MANAGER_MATRIX_TOKEN}" \
    -H 'Content-Type: application/json' \
    -d '{
        "name": "Worker: '"${WORKER_NAME}"'",
        "topic": "Communication channel for '"${WORKER_NAME}"'",
        "invite": [
            "'"${ADMIN_MATRIX_ID}"'",
            "@'"${WORKER_NAME}"':'"${MATRIX_DOMAIN}"'"
        ],
        "preset": "trusted_private_chat",
        "power_level_content_override": {
            "users": {
                "'"${MANAGER_MATRIX_ID}"'": 100,
                "'"${ADMIN_MATRIX_ID}"'": 100,
                "@'"${WORKER_NAME}"':'"${MATRIX_DOMAIN}"'": 0
            }
        }'"${ROOM_E2EE_INITIAL_STATE}"'
    }' 2>/dev/null) || _fail "Failed to create Matrix room"

ROOM_ID=$(echo "${ROOM_RESP}" | jq -r '.room_id // empty')
if [ -z "${ROOM_ID}" ]; then
    _fail "Failed to create Matrix room: ${ROOM_RESP}"
fi
log "  Room created: ${ROOM_ID}"

# ============================================================
# Steps 3-5: Gateway consumer and authorization
# ============================================================
WORKER_KEY="${WORKER_GATEWAY_KEY}"

log "Step 3: Creating gateway consumer..."
CONSUMER_RESULT=$(gateway_create_consumer "${CONSUMER_NAME}" "${WORKER_KEY}") \
    || _fail "Gateway consumer creation failed for ${CONSUMER_NAME}"
log "  Consumer result: ${CONSUMER_RESULT}"

# Cloud backend may return a platform-assigned API key — use it if present
GW_API_KEY=$(echo "${CONSUMER_RESULT}" | jq -r '.api_key // empty' 2>/dev/null)
if [ -n "${GW_API_KEY}" ] && [ "${GW_API_KEY}" != "${WORKER_KEY}" ]; then
    WORKER_KEY="${GW_API_KEY}"
    WORKER_GATEWAY_KEY="${GW_API_KEY}"
    log "  Using platform-assigned API key (prefix: ${WORKER_KEY:0:8}...)"
fi

# Pass consumer_id to gateway_authorize_routes (used by cloud backend)
GATEWAY_CONSUMER_ID=$(echo "${CONSUMER_RESULT}" | jq -r '.consumer_id // empty' 2>/dev/null)
export GATEWAY_CONSUMER_ID

log "Step 4: Authorizing AI routes..."
gateway_authorize_routes "${CONSUMER_NAME}"
log "  Routes authorized"

log "Step 5: Authorizing MCP servers..."
gateway_authorize_mcp "${CONSUMER_NAME}" "${MCP_SERVERS}"
log "  MCP authorization complete"

# ============================================================
# Step 6: Generate openclaw.json
# ============================================================
log "Step 6: Generating openclaw.json..."
GEN_ARGS=("${WORKER_NAME}" "${WORKER_MATRIX_TOKEN}" "${WORKER_KEY}")
if [ -n "${MODEL_ID}" ]; then
    GEN_ARGS+=("${MODEL_ID}")
fi
bash /opt/hiclaw/agent/skills/worker-management/scripts/generate-worker-config.sh "${GEN_ARGS[@]}"

# Generate mcporter-servers.json if MCP servers are authorized
if [ -n "${TARGET_MCP_LIST}" ]; then
    log "  Generating mcporter-servers.json..."
    # MCP servers are hosted on the AI Gateway domain
    AIGW_DOMAIN="${HICLAW_AI_GATEWAY_DOMAIN:-aigw-local.hiclaw.io}"
    MCPORTER_JSON='{"mcpServers":{'
    FIRST=true
    IFS=',' read -ra MCP_ARR2 <<< "${TARGET_MCP_LIST}"
    for mcp_name in "${MCP_ARR2[@]}"; do
        mcp_name=$(echo "${mcp_name}" | tr -d ' ')
        [ -z "${mcp_name}" ] && continue
        if [ "${FIRST}" = true ]; then FIRST=false; else MCPORTER_JSON="${MCPORTER_JSON},"; fi
        MCPORTER_JSON="${MCPORTER_JSON}\"${mcp_name}\":{\"url\":\"http://${AIGW_DOMAIN}:8080/mcp-servers/${mcp_name}/mcp\",\"transport\":\"http\",\"headers\":{\"Authorization\":\"Bearer ${WORKER_KEY}\"}}"
    done
    MCPORTER_JSON="${MCPORTER_JSON}}}"
    echo "${MCPORTER_JSON}" | jq . > "/root/hiclaw-fs/agents/${WORKER_NAME}/mcporter-servers.json"
fi

# Step 6.5 removed: Workers do NOT get other workers in their groupAllowFrom by default.
# By default, a Worker only accepts @mentions from Manager and the human admin.
# This prevents infinite mutual-mention loops between Workers.
# Inter-worker direct @mentions must be explicitly enabled per-project when needed.
# Pre-compute deployment hint for registry (actual DEPLOY_MODE is finalized in Step 9)
# "remote" = admin will run the worker themselves; "local" = Manager-managed container
# If container creation fails in Step 9, this will be corrected to "remote" afterward.
if [ "${REMOTE_MODE}" = true ]; then
    DEPLOY_MODE_HINT="remote"
else
    DEPLOY_MODE_HINT="local"
fi

REGISTRY_FILE_EARLY="${HOME}/workers-registry.json"

# ============================================================
# Step 7: Update Manager groupAllowFrom
# ============================================================
log "Step 7: Updating Manager groupAllowFrom..."
MANAGER_CONFIG="${HOME}/openclaw.json"
WORKER_MATRIX_ID="@${WORKER_NAME}:${MATRIX_DOMAIN}"
if [ -f "${MANAGER_CONFIG}" ]; then
    ALREADY_IN=$(jq -r --arg w "${WORKER_MATRIX_ID}" \
        '.channels.matrix.groupAllowFrom // [] | map(select(. == $w)) | length' \
        "${MANAGER_CONFIG}" 2>/dev/null || echo "0")
    if [ "${ALREADY_IN}" = "0" ]; then
        jq --arg w "${WORKER_MATRIX_ID}" \
            '.channels.matrix.groupAllowFrom += [$w]' \
            "${MANAGER_CONFIG}" > /tmp/manager-config-updated.json
        mv /tmp/manager-config-updated.json "${MANAGER_CONFIG}"
        log "  Added ${WORKER_MATRIX_ID} to groupAllowFrom"
    else
        log "  ${WORKER_MATRIX_ID} already in groupAllowFrom"
    fi
fi

# ============================================================
# Step 8: Sync to MinIO
# ============================================================
log "Step 8: Syncing to storage..."
ensure_mc_credentials 2>/dev/null || true
mc mirror "/root/hiclaw-fs/agents/${WORKER_NAME}/" "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/" --overwrite 2>&1 | tail -5
mc stat "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/SOUL.md" > /dev/null 2>&1 \
    || _fail "SOUL.md not found in MinIO after sync"
mc stat "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/openclaw.json" > /dev/null 2>&1 \
    || _fail "openclaw.json not found in MinIO after sync"

# Write Matrix password directly to MinIO (never touches Worker's local filesystem)
# Worker reads it via mc cat on startup for E2EE re-login
_tmp_pw="/tmp/matrix-pw-$$"
echo -n "${WORKER_PASSWORD}" > "${_tmp_pw}"
mc cp "${_tmp_pw}" "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/credentials/matrix/password" 2>/dev/null \
    || log "  WARNING: Failed to write Matrix password to MinIO"
rm -f "${_tmp_pw}"

log "  MinIO sync verified"

# Push Worker agent files from Manager image (AGENTS.md + file-sync skill)
# Use runtime-specific file-sync skill for copaw workers
if [ "${WORKER_RUNTIME}" = "copaw" ]; then
    WORKER_AGENT_SRC="/opt/hiclaw/agent/copaw-worker-agent"
    FILESYNC_SRC="${WORKER_AGENT_SRC}/skills/file-sync"
else
    WORKER_AGENT_SRC="/opt/hiclaw/agent/worker-agent"
    FILESYNC_SRC="${WORKER_AGENT_SRC}/skills/file-sync"
fi

if [ -d "${WORKER_AGENT_SRC}" ]; then
    log "  Merging AGENTS.md (runtime=${WORKER_RUNTIME}) to worker MinIO..."
    source /opt/hiclaw/scripts/lib/builtin-merge.sh
    update_builtin_section_minio \
        "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/AGENTS.md" \
        "${WORKER_AGENT_SRC}/AGENTS.md" \
        || log "  WARNING: Failed to merge AGENTS.md"
    
    if [ -d "${FILESYNC_SRC}" ]; then
        log "  Pushing file-sync skill (${WORKER_RUNTIME}) to worker MinIO..."
        mc mirror "${FILESYNC_SRC}/" \
            "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/skills/file-sync/" --overwrite \
            || log "  WARNING: Failed to push file-sync skill"
        log "  Worker agent files pushed"
    else
        log "  WARNING: file-sync skill not found at ${FILESYNC_SRC}"
    fi
else
    log "  WARNING: worker-agent directory not found at ${WORKER_AGENT_SRC}"
fi

# Step 8b removed: Do NOT add the new Worker to existing Workers' groupAllowFrom.
# Workers only accept @mentions from Manager and admin by default.
# This prevents inter-worker mention loops. Enable peer mentions explicitly if needed.

# ============================================================
# Step 8.5: Update workers-registry.json and push skills
# ============================================================
log "Step 8.5: Updating workers-registry and pushing skills..."
REGISTRY_FILE="${HOME}/workers-registry.json"

# Ensure registry file exists
if [ ! -f "${REGISTRY_FILE}" ]; then
    log "  Initializing workers-registry.json..."
    echo '{"version":1,"updated_at":"","workers":{}}' > "${REGISTRY_FILE}"
fi

# Build skills JSON array from WORKER_SKILLS (comma-separated)
SKILLS_JSON="["
FIRST_SKILL=true
# Ensure file-sync is always included
SKILLS_WITH_FILESYNC="${WORKER_SKILLS}"
if ! echo "${SKILLS_WITH_FILESYNC}" | grep -q '\bfile-sync\b'; then
    SKILLS_WITH_FILESYNC="file-sync,${SKILLS_WITH_FILESYNC}"
fi
IFS=',' read -ra SKILL_ARR <<< "${SKILLS_WITH_FILESYNC}"
for skill in "${SKILL_ARR[@]}"; do
    skill=$(echo "${skill}" | tr -d ' ')
    [ -z "${skill}" ] && continue
    if [ "${FIRST_SKILL}" = true ]; then FIRST_SKILL=false; else SKILLS_JSON="${SKILLS_JSON},"; fi
    SKILLS_JSON="${SKILLS_JSON}\"${skill}\""
done
SKILLS_JSON="${SKILLS_JSON}]"

# Upsert worker entry into registry
NOW_TS=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
WORKER_MATRIX_USER_ID="@${WORKER_NAME}:${MATRIX_DOMAIN}"

jq --arg w "${WORKER_NAME}" \
   --arg uid "${WORKER_MATRIX_USER_ID}" \
   --arg rid "${ROOM_ID}" \
   --arg ts "${NOW_TS}" \
   --arg runtime "${WORKER_RUNTIME}" \
   --arg deployment "${DEPLOY_MODE_HINT}" \
   --argjson skills "${SKILLS_JSON}" \
   '.workers[$w] = {
     "matrix_user_id": $uid,
     "room_id": $rid,
     "runtime": $runtime,
     "deployment": $deployment,
     "skills": $skills,
     "created_at": (if .workers[$w].created_at? then .workers[$w].created_at else $ts end),
     "skills_updated_at": $ts
   } | .updated_at = $ts' \
   "${REGISTRY_FILE}" > /tmp/workers-registry-updated.json
mv /tmp/workers-registry-updated.json "${REGISTRY_FILE}"

log "  Registry updated for ${WORKER_NAME}: skills=${SKILLS_WITH_FILESYNC}"

# Push skills to worker's MinIO workspace (Worker not yet started, no notification)
bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh \
    --worker "${WORKER_NAME}" --no-notify \
    || log "  WARNING: push-worker-skills.sh returned non-zero (non-fatal)"

# ============================================================
# Step 9: Start Worker
# ============================================================
DEPLOY_MODE="remote"
CONTAINER_ID=""
INSTALL_CMD=""
WORKER_STATUS="pending_install"

_build_install_cmd() {
    # copaw workers run on the host, so use the externally-exposed gateway port.
    # openclaw workers run inside a container, so use the internal port 8080.
    local fs_domain="${HICLAW_FS_DOMAIN:-fs-local.hiclaw.io}"
    local fs_internal_endpoint="http://${fs_domain}:8080"
    local fs_external_port="${HICLAW_PORT_GATEWAY:-18080}"
    local fs_external_endpoint="http://${fs_domain}:${fs_external_port}"
    local fs_access_key="${WORKER_NAME}"
    local fs_secret_key="${WORKER_MINIO_PASSWORD}"

    if [ "${WORKER_RUNTIME}" = "copaw" ]; then
        # copaw-worker is a pip package running on the host; use external port.
        # Use Alibaba Cloud PyPI mirror for faster downloads in China.
        local cmd="pip install -i https://mirrors.aliyun.com/pypi/simple/ copaw-worker && copaw-worker"
        cmd="${cmd} --name ${WORKER_NAME}"
        cmd="${cmd} --fs ${fs_external_endpoint}"
        cmd="${cmd} --fs-key ${fs_access_key}"
        cmd="${cmd} --fs-secret ${fs_secret_key}"
        cmd="${cmd} --console-port ${CONSOLE_PORT:-8088}"
        echo "${cmd}"
        return
    fi

    local cmd="bash hiclaw-install.sh worker --name ${WORKER_NAME} --fs ${fs_internal_endpoint} --fs-key ${fs_access_key} --fs-secret ${fs_secret_key}"

    # Add find-skills related options if enabled
    if [ "${ENABLE_FIND_SKILLS}" = true ]; then
        cmd="${cmd} --find-skills"
        if [ -n "${SKILLS_API_URL}" ]; then
            cmd="${cmd} --skills-api-url ${SKILLS_API_URL}"
        fi
    fi

    echo "${cmd}"
}

# Build extra environment variables JSON for container creation
_build_extra_env() {
    local items=()
    if [ "${ENABLE_FIND_SKILLS}" = true ] && [ -n "${SKILLS_API_URL}" ]; then
        items+=("SKILLS_API_URL=${SKILLS_API_URL}")
    fi
    if [ -n "${CONSOLE_PORT}" ]; then
        items+=("HICLAW_CONSOLE_PORT=${CONSOLE_PORT}")
    fi
    if [ ${#items[@]} -eq 0 ]; then
        echo "[]"
    else
        printf '%s\n' "${items[@]}" | jq -R . | jq -s .
    fi
}

if [ "${REMOTE_MODE}" = true ]; then
    log "Step 9: Remote mode requested"
    INSTALL_CMD=$(_build_install_cmd)
elif [ "${HICLAW_RUNTIME}" = "aliyun" ]; then
    log "Step 9: Creating Worker via cloud backend (SAE)..."

    # Build complete SAE environment variables (Worker needs these to connect)
    SAE_ENVS=$(jq -cn \
        --arg worker_key "${WORKER_KEY}" \
        --arg matrix_url "${HICLAW_MATRIX_URL:-}" \
        --arg matrix_domain "${MATRIX_DOMAIN}" \
        --arg matrix_token "${WORKER_MATRIX_TOKEN}" \
        --arg ai_gw_url "${HICLAW_AI_GATEWAY_URL:-}" \
        --arg oss_bucket "${HICLAW_OSS_BUCKET:-hiclaw-cloud-storage}" \
        --arg region "${HICLAW_REGION:-cn-hangzhou}" \
        '{
            "HICLAW_WORKER_GATEWAY_KEY": $worker_key,
            "HICLAW_MATRIX_URL": $matrix_url,
            "HICLAW_MATRIX_DOMAIN": $matrix_domain,
            "HICLAW_WORKER_MATRIX_TOKEN": $matrix_token,
            "HICLAW_AI_GATEWAY_URL": $ai_gw_url,
            "HICLAW_OSS_BUCKET": $oss_bucket,
            "HICLAW_REGION": $region
        }')
    log "  SAE_ENVS: ${SAE_ENVS:0:200}..."

    CREATE_OUTPUT=$(sae_create_worker "${WORKER_NAME}" "${SAE_ENVS}" 2>/dev/null) || true
    log "  SAE create response: ${CREATE_OUTPUT:0:300}"
    SAE_STATUS=$(echo "${CREATE_OUTPUT}" | jq -r '.status // "error"' 2>/dev/null)

    if [ "${SAE_STATUS}" = "created" ] || [ "${SAE_STATUS}" = "exists" ]; then
        DEPLOY_MODE="cloud"
        WORKER_STATUS="starting"
        log "  SAE application ready for ${WORKER_NAME}"
    else
        log "  WARNING: SAE application creation returned: ${CREATE_OUTPUT}"
        WORKER_STATUS="error"
    fi
elif container_api_available; then
    log "Step 9: Starting Worker container locally (runtime=${WORKER_RUNTIME})..."
    EXTRA_ENV_JSON=$(_build_extra_env)

    if [ "${WORKER_RUNTIME}" = "copaw" ]; then
        CREATE_OUTPUT=$(container_create_copaw_worker "${WORKER_NAME}" "${WORKER_NAME}" "${WORKER_MINIO_PASSWORD}" "${EXTRA_ENV_JSON}" 2>&1) || true
    else
        CREATE_OUTPUT=$(container_create_worker "${WORKER_NAME}" "${WORKER_NAME}" "${WORKER_MINIO_PASSWORD}" "${EXTRA_ENV_JSON}" 2>&1) || true
    fi

    CONTAINER_ID=$(echo "${CREATE_OUTPUT}" | tail -1)
    CONSOLE_HOST_PORT=$(echo "${CREATE_OUTPUT}" | grep -o 'CONSOLE_HOST_PORT=[0-9]*' | head -1 | cut -d= -f2)
    if [ -n "${CONTAINER_ID}" ] && [ ${#CONTAINER_ID} -ge 12 ]; then
        DEPLOY_MODE="local"
        if [ -n "${CONSOLE_HOST_PORT}" ]; then
            log "  Console available at host port ${CONSOLE_HOST_PORT}"
        fi
        log "  Waiting for Worker agent to be ready..."
        if [ "${WORKER_RUNTIME}" = "copaw" ]; then
            if container_wait_copaw_worker_ready "${WORKER_NAME}" 120; then
                WORKER_STATUS="ready"
                log "  CoPaw Worker agent is ready!"
            else
                WORKER_STATUS="starting"
                log "  WARNING: CoPaw Worker agent not ready within timeout (container may still be initializing)"
            fi
        else
            if container_wait_worker_ready "${WORKER_NAME}" 120; then
                WORKER_STATUS="ready"
                log "  Worker agent is ready!"
            else
                WORKER_STATUS="starting"
                log "  WARNING: Worker agent not ready within timeout (container may still be initializing)"
            fi
        fi
    else
        log "  WARNING: Container creation failed, falling back to remote mode"
        INSTALL_CMD=$(_build_install_cmd)
    fi
else
    log "Step 9: No container runtime socket available"
    INSTALL_CMD=$(_build_install_cmd)
fi

# ============================================================
# Step 9b: Correct deployment field if actual mode differs from hint
# ============================================================
if [ "${DEPLOY_MODE}" = "remote" ] && [ "${DEPLOY_MODE_HINT}" = "local" ]; then
    log "Step 9b: Container creation failed, correcting deployment to 'remote' in registry..."
    jq --arg w "${WORKER_NAME}" '.workers[$w].deployment = "remote"' \
        "${REGISTRY_FILE}" > /tmp/workers-registry-deploy-fix.json
    mv /tmp/workers-registry-deploy-fix.json "${REGISTRY_FILE}"
fi

# ============================================================
# Output JSON result
# ============================================================
RESULT=$(jq -n \
    --arg name "${WORKER_NAME}" \
    --arg user_id "${WORKER_USER_ID}" \
    --arg room_id "${ROOM_ID}" \
    --arg consumer "${CONSUMER_NAME}" \
    --arg mode "${DEPLOY_MODE}" \
    --arg runtime "${WORKER_RUNTIME}" \
    --arg container_id "${CONTAINER_ID}" \
    --arg status "${WORKER_STATUS}" \
    --arg install_cmd "${INSTALL_CMD:-}" \
    --arg console_host_port "${CONSOLE_HOST_PORT:-}" \
    --argjson skills "${SKILLS_JSON}" \
    '{
        worker_name: $name,
        matrix_user_id: $user_id,
        room_id: $room_id,
        consumer: $consumer,
        runtime: $runtime,
        skills: $skills,
        mode: $mode,
        container_id: $container_id,
        status: $status,
        install_cmd: (if $install_cmd == "" then null else $install_cmd end),
        console_host_port: (if $console_host_port == "" then null else $console_host_port end)
    }')

echo "---RESULT---"
echo "${RESULT}"
