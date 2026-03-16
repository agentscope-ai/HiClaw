#!/bin/bash
# start-manager-agent.sh - Initialize and start the Manager Agent
# This is the last component to start (priority 800).
# It waits for all dependencies, creates Matrix users, configures Higress,
# creates symlinks for host directory access, and launches OpenClaw.

source /opt/hiclaw/scripts/lib/base.sh

# ============================================================
# Set timezone from TZ env var
# ============================================================
if [ -n "${TZ}" ] && [ -f "/usr/share/zoneinfo/${TZ}" ]; then
    ln -sf "/usr/share/zoneinfo/${TZ}" /etc/localtime
    echo "${TZ}" > /etc/timezone
    log "Timezone set to ${TZ}"
fi

MATRIX_DOMAIN="${HICLAW_MATRIX_DOMAIN:-matrix-local.hiclaw.io:8080}"
AI_GATEWAY_DOMAIN="${HICLAW_AI_GATEWAY_DOMAIN:-aigw-local.hiclaw.io}"

# ============================================================
# Create symlink for host directory access
# If /host-share is mounted, create a symlink using the original host's HOME path
# ============================================================
if [ -d "/host-share" ]; then
    # Determine the original host's home directory path for consistent access
    # The user can set HOST_ORIGINAL_HOME via environment variable if needed
    ORIGINAL_HOST_HOME="${HOST_ORIGINAL_HOME:-$HOME}"

    # Only create the symlink if it won't conflict with existing paths in the container
    if [ ! -e "${ORIGINAL_HOST_HOME}" ] && [ "${ORIGINAL_HOST_HOME}" != "/" ] && [ "${ORIGINAL_HOST_HOME}" != "/root" ] && [ "${ORIGINAL_HOST_HOME}" != "/data" ] && [ "${ORIGINAL_HOST_HOME}" != "/host-share" ]; then
        # Create parent directories if they don't exist
        mkdir -p "$(dirname "${ORIGINAL_HOST_HOME}")"

        # Create symlink from original host home path to the mounted host share
        ln -sfn /host-share "${ORIGINAL_HOST_HOME}"
        log "Created symlink: ${ORIGINAL_HOST_HOME} -> /host-share"
        log "Host files now accessible at: ${ORIGINAL_HOST_HOME}/"
    else
        log "Skipping symlink creation to avoid conflict with existing path: ${ORIGINAL_HOST_HOME}"
        # Create a fallback symlink with a different name
        ln -sfn /host-share /root/host-home
        log "Created fallback symlink: /root/host-home -> /host-share"
    fi
else
    log "Host share directory (/host-share) not found, skipping symlink creation"
fi

# Add local domains to /etc/hosts so they resolve inside the container
HOSTS_DOMAINS="${MATRIX_DOMAIN%%:*} ${HICLAW_MATRIX_CLIENT_DOMAIN:-matrix-client-local.hiclaw.io} ${AI_GATEWAY_DOMAIN} ${HICLAW_FS_DOMAIN:-fs-local.hiclaw.io}"
if ! grep -q "${AI_GATEWAY_DOMAIN}" /etc/hosts 2>/dev/null; then
    echo "127.0.0.1 ${HOSTS_DOMAINS}" >> /etc/hosts
    log "Added local domains to /etc/hosts"
fi

# ============================================================
# Auto-generate secrets if not provided via environment
# Persisted to /data so they survive container restart
# ============================================================
SECRETS_FILE="/data/hiclaw-secrets.env"
if [ -f "${SECRETS_FILE}" ]; then
    source "${SECRETS_FILE}"
    log "Loaded persisted secrets from ${SECRETS_FILE}"
fi

if [ -z "${HICLAW_MANAGER_GATEWAY_KEY}" ]; then
    export HICLAW_MANAGER_GATEWAY_KEY="$(generateKey 32)"
    log "Auto-generated HICLAW_MANAGER_GATEWAY_KEY"
fi
if [ -z "${HICLAW_MANAGER_PASSWORD}" ]; then
    export HICLAW_MANAGER_PASSWORD="$(generateKey 16)"
    log "Auto-generated HICLAW_MANAGER_PASSWORD"
fi

# Persist secrets so they survive supervisord restart
mkdir -p /data
cat > "${SECRETS_FILE}" <<EOF
export HICLAW_MANAGER_GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY}"
export HICLAW_MANAGER_PASSWORD="${HICLAW_MANAGER_PASSWORD}"
EOF
chmod 600 "${SECRETS_FILE}"

# ============================================================
# Wait for all dependencies
# ============================================================
waitForService "Higress Gateway" "127.0.0.1" 8080 180
waitForService "Higress Console" "127.0.0.1" 8001 180
waitForService "Tuwunel" "127.0.0.1" 6167 120
waitForHTTP "Tuwunel Matrix API" "http://127.0.0.1:6167/_tuwunel/server_version" 120
waitForService "MinIO" "127.0.0.1" 9000 120

# ============================================================
# Initialize / upgrade Manager workspace (local only, not synced to MinIO)
# First boot: full init via upgrade-builtins.sh
# Subsequent boots: compare image version; upgrade only if changed
# ============================================================
mkdir -p /root/manager-workspace

IMAGE_VERSION=$(cat /opt/hiclaw/agent/.builtin-version 2>/dev/null || echo "unknown")
INSTALLED_VERSION=$(cat /root/manager-workspace/.builtin-version 2>/dev/null || echo "")

if [ ! -f /root/manager-workspace/.initialized ]; then
    log "First boot: initializing manager workspace..."
    bash /opt/hiclaw/scripts/init/upgrade-builtins.sh
    touch /root/manager-workspace/.initialized
    log "Manager workspace initialized (version: ${IMAGE_VERSION})"
elif [ "${IMAGE_VERSION}" != "${INSTALLED_VERSION}" ] || [ "${IMAGE_VERSION}" = "latest" ]; then
    log "Upgrade detected: ${INSTALLED_VERSION} -> ${IMAGE_VERSION}${IMAGE_VERSION:+ (latest: always upgrade)}"
    bash /opt/hiclaw/scripts/init/upgrade-builtins.sh
    log "Manager workspace upgraded to version: ${IMAGE_VERSION}"
else
    log "Workspace up to date (version: ${IMAGE_VERSION})"
fi

# Wait for mc mirror initialization (shared + worker data in /root/hiclaw-fs/)
log "Waiting for MinIO storage initialization..."
while [ ! -f /root/hiclaw-fs/.initialized ]; do sleep 2; done
log "MinIO storage initialized"

# ============================================================
# Register Matrix users via Registration API (single-step, no UIAA)
# ============================================================
log "Registering human admin Matrix account..."
curl -sf -X POST http://127.0.0.1:6167/_matrix/client/v3/register \
    -H 'Content-Type: application/json' \
    -d '{
        "username": "'"${HICLAW_ADMIN_USER}"'",
        "password": "'"${HICLAW_ADMIN_PASSWORD}"'",
        "auth": {
            "type": "m.login.registration_token",
            "token": "'"${HICLAW_REGISTRATION_TOKEN}"'"
        }
    }' > /dev/null 2>&1 || log "Admin account may already exist"

log "Registering Manager Agent Matrix account..."
curl -sf -X POST http://127.0.0.1:6167/_matrix/client/v3/register \
    -H 'Content-Type: application/json' \
    -d '{
        "username": "manager",
        "password": "'"${HICLAW_MANAGER_PASSWORD}"'",
        "auth": {
            "type": "m.login.registration_token",
            "token": "'"${HICLAW_REGISTRATION_TOKEN}"'"
        }
    }' > /dev/null 2>&1 || log "Manager account may already exist"

# Get Manager Agent's Matrix access token
log "Obtaining Manager Matrix access token..."
_LOGIN_RESPONSE=$(curl -sf -X POST http://127.0.0.1:6167/_matrix/client/v3/login \
    -H 'Content-Type: application/json' \
    -d '{
        "type": "m.login.password",
        "identifier": {"type": "m.id.user", "user": "manager"},
        "password": "'"${HICLAW_MANAGER_PASSWORD}"'"
    }' 2>&1)
_LOGIN_EXIT=$?
log "Matrix login HTTP exit code: ${_LOGIN_EXIT}"
log "Matrix login response: ${_LOGIN_RESPONSE}"

MANAGER_TOKEN=$(echo "${_LOGIN_RESPONSE}" | jq -r '.access_token' 2>/dev/null)

if [ -z "${MANAGER_TOKEN}" ] || [ "${MANAGER_TOKEN}" = "null" ]; then
    log "ERROR: Failed to obtain Manager Matrix token (exit=${_LOGIN_EXIT})"
    log "ERROR: Login response was: ${_LOGIN_RESPONSE}"
    exit 1
fi
log "Manager Matrix token obtained (token prefix: ${MANAGER_TOKEN:0:10}...)"

# ============================================================
# Initialize Higress Console (Session Cookie auth)
# ============================================================
COOKIE_FILE="/tmp/higress-session-cookie"

# Wait for Higress Console Java app to be fully ready (not just port open)
# The Spring Boot app may take 10-30s after port opens to serve requests.
# On first boot: /system/init creates admin. On restart: init returns "already initialized".
# IMPORTANT: Always attempt /system/init first (idempotent), then login.
log "Waiting for Higress Console to be fully ready and initializing admin..."
INIT_DONE=false
for i in $(seq 1 90); do
    INIT_RESULT=$(curl -s -X POST http://127.0.0.1:8001/system/init \
        -H 'Content-Type: application/json' \
        -d '{"adminUser":{"name":"'"${HICLAW_ADMIN_USER}"'","password":"'"${HICLAW_ADMIN_PASSWORD}"'","displayName":"'"${HICLAW_ADMIN_USER}"'"}}' 2>/dev/null) || true
    if echo "${INIT_RESULT}" | grep -qE '"success":true|already.?init' 2>/dev/null; then
        INIT_DONE=true
        break
    fi
    if echo "${INIT_RESULT}" | grep -q '"name"' 2>/dev/null; then
        INIT_DONE=true
        break
    fi
    sleep 2
done

if [ "${INIT_DONE}" != "true" ]; then
    log "ERROR: Higress Console did not become ready within 180s"
    exit 1
fi
log "Higress Console init done"

# Login: init uses "name", login uses "username"
log "Logging into Higress Console..."
LOGIN_OK=false
for i in $(seq 1 10); do
    HTTP_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8001/session/login \
        -H 'Content-Type: application/json' \
        -c "${COOKIE_FILE}" \
        -d '{"username":"'"${HICLAW_ADMIN_USER}"'","password":"'"${HICLAW_ADMIN_PASSWORD}"'"}' 2>/dev/null) || true
    if { [ "${HTTP_CODE}" = "200" ] || [ "${HTTP_CODE}" = "201" ]; } && [ -f "${COOKIE_FILE}" ] && [ -s "${COOKIE_FILE}" ]; then
        LOGIN_OK=true
        break
    fi
    log "Login attempt $i (HTTP ${HTTP_CODE}), retrying in 3s..."
    sleep 3
done

if [ "${LOGIN_OK}" != "true" ]; then
    log "ERROR: Could not login to Higress Console after retries"
    exit 1
fi
log "Higress Console login successful"

# Verify cookie is valid by calling an API endpoint
VERIFY_CODE=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8001/v1/consumers -b "${COOKIE_FILE}" 2>/dev/null) || true
if [ "${VERIFY_CODE}" = "200" ]; then
    log "Console session verified (cookie valid)"
else
    log "WARNING: Console session may be invalid (verify returned HTTP ${VERIFY_CODE})"
    # Try re-login with a fresh cookie file
    rm -f "${COOKIE_FILE}"
    for i in $(seq 1 5); do
        curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8001/session/login \
            -H 'Content-Type: application/json' \
            -c "${COOKIE_FILE}" \
            -d '{"username":"'"${HICLAW_ADMIN_USER}"'","password":"'"${HICLAW_ADMIN_PASSWORD}"'"}' 2>/dev/null
        VERIFY2=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8001/v1/consumers -b "${COOKIE_FILE}" 2>/dev/null) || true
        if [ "${VERIFY2}" = "200" ]; then
            log "Re-login successful, session verified"
            break
        fi
        sleep 2
    done
fi

export HIGRESS_COOKIE_FILE="${COOKIE_FILE}"

# ============================================================
# Configure Higress routes, consumers, MCP servers
# ============================================================
/opt/hiclaw/scripts/init/setup-higress.sh

# ============================================================
# Generate Manager Agent openclaw.json from template
# ============================================================
log "Generating Manager openclaw.json..."
export MANAGER_MATRIX_TOKEN="${MANAGER_TOKEN}"
export MANAGER_GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY}"

# Resolve model parameters based on model name
MODEL_NAME="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
case "${MODEL_NAME}" in
    gpt-5.3-codex|gpt-5-mini|gpt-5-nano)
        export MODEL_CONTEXT_WINDOW=400000 MODEL_MAX_TOKENS=128000 ;;
    claude-opus-4-6)
        export MODEL_CONTEXT_WINDOW=1000000 MODEL_MAX_TOKENS=128000 ;;
    claude-sonnet-4-6)
        export MODEL_CONTEXT_WINDOW=1000000 MODEL_MAX_TOKENS=64000 ;;
    claude-haiku-4-5)
        export MODEL_CONTEXT_WINDOW=200000 MODEL_MAX_TOKENS=64000 ;;
    qwen3.5-plus)
        export MODEL_CONTEXT_WINDOW=200000 MODEL_MAX_TOKENS=64000 ;;
    deepseek-chat|deepseek-reasoner|kimi-k2.5)
        export MODEL_CONTEXT_WINDOW=256000 MODEL_MAX_TOKENS=128000 ;;
    glm-5|MiniMax-M2.5)
        export MODEL_CONTEXT_WINDOW=200000 MODEL_MAX_TOKENS=128000 ;;
    *)
        export MODEL_CONTEXT_WINDOW=150000 MODEL_MAX_TOKENS=128000 ;;
esac
export MODEL_REASONING=true

# E2EE: convert HICLAW_MATRIX_E2EE to JSON boolean for template substitution
if [ "${HICLAW_MATRIX_E2EE:-0}" = "1" ] || [ "${HICLAW_MATRIX_E2EE:-}" = "true" ]; then
    export MATRIX_E2EE_ENABLED=true
else
    export MATRIX_E2EE_ENABLED=false
fi
log "Matrix E2EE: ${MATRIX_E2EE_ENABLED}"

# Resolve input modalities: only vision-capable models get "image"
case "${MODEL_NAME}" in
    gpt-5.4|gpt-5.3-codex|gpt-5-mini|gpt-5-nano|claude-opus-4-6|claude-sonnet-4-6|claude-haiku-4-5|qwen3.5-plus|kimi-k2.5)
        export MODEL_INPUT='["text", "image"]' ;;
    *)
        export MODEL_INPUT='["text"]' ;;
esac

log "Model: ${MODEL_NAME} (context=${MODEL_CONTEXT_WINDOW}, maxTokens=${MODEL_MAX_TOKENS}, reasoning=${MODEL_REASONING}, input=${MODEL_INPUT})"

if [ -f /root/manager-workspace/openclaw.json ]; then
    log "Manager openclaw.json already exists, updating dynamic fields only (preserving user customizations)..."
    # Merge known models into existing config (add missing, preserve user-added)
    # Use known-models.json (valid JSON) instead of template (contains ${VAR} placeholders)
    KNOWN_MODELS=$(cat /opt/hiclaw/configs/known-models.json 2>/dev/null || echo '[]')
    jq --arg token "${MANAGER_TOKEN}" \
       --arg key "${HICLAW_MANAGER_GATEWAY_KEY}" \
       --arg model "${MODEL_NAME}" \
       --argjson e2ee "${MATRIX_E2EE_ENABLED}" \
       --argjson known_models "${KNOWN_MODELS}" \
       '
        # Merge known models: add any model id not already present
        .models.providers["hiclaw-gateway"].models as $existing
        | ($existing | map(.id)) as $existing_ids
        | ($known_models | map(select(.id as $id | $existing_ids | index($id) | not))) as $new
        | .models.providers["hiclaw-gateway"].models = ($existing + $new)
        # Rebuild model aliases from the full models list
        | (.models.providers["hiclaw-gateway"].models | map({ ("hiclaw-gateway/" + .id): { "alias": .id } }) | add // {}) as $aliases
        | .agents.defaults.models = ((.agents.defaults.models // {}) + $aliases)
        | .channels.matrix.accessToken = $token | .hooks.token = $key | .models.providers["hiclaw-gateway"].apiKey = $key
        | .agents.defaults.model.primary = ("hiclaw-gateway/" + $model)
        | .commands.restart = true
        | .gateway.controlUi.dangerouslyDisableDeviceAuth = true
        | .channels.matrix.encryption = $e2ee
       ' \
       /root/manager-workspace/openclaw.json > /tmp/openclaw.json.tmp && \
        mv /tmp/openclaw.json.tmp /root/manager-workspace/openclaw.json
    # Verify the token was written correctly
    _written_token=$(jq -r '.channels.matrix.accessToken' /root/manager-workspace/openclaw.json 2>/dev/null)
    if [ -z "${_written_token}" ] || [ "${_written_token}" = "null" ]; then
        log "ERROR: Matrix token was not written correctly to openclaw.json (got: ${_written_token})"
    else
        log "Matrix token written to openclaw.json (prefix: ${_written_token:0:10}...)"
    fi
else
    log "Manager openclaw.json not found, generating from template..."
    envsubst < /opt/hiclaw/configs/manager-openclaw.json.tmpl > /root/manager-workspace/openclaw.json
    _written_token=$(jq -r '.channels.matrix.accessToken' /root/manager-workspace/openclaw.json 2>/dev/null)
    log "Matrix token written from template (prefix: ${_written_token:0:10}...)"
fi

# ============================================================
# Detect container runtime socket (for direct Worker creation)
# ============================================================
source /opt/hiclaw/scripts/lib/container-api.sh
if container_api_available; then
    log "Container runtime socket detected at ${CONTAINER_SOCKET} — direct Worker creation enabled"
    export HICLAW_CONTAINER_RUNTIME="socket"
else
    log "No container runtime socket found — Worker creation will output install commands"
    export HICLAW_CONTAINER_RUNTIME="none"
fi

# ============================================================
# Upgrade Worker openclaw.json: merge known models + E2EE flag into existing configs
# Existing workers in MinIO may have old single-model configs or missing encryption field.
# Merge template models so they can hot-switch without restart.
# ============================================================
REGISTRY_FILE="/root/manager-workspace/workers-registry.json"
if [ -f "${REGISTRY_FILE}" ]; then
    # Use known-models.json (valid JSON) instead of template (contains ${VAR} placeholders)
    KNOWN_MODELS_FILE="/opt/hiclaw/configs/known-models.json"
    if [ -f "${KNOWN_MODELS_FILE}" ]; then
        _KNOWN_MODELS=$(cat "${KNOWN_MODELS_FILE}")
        for _wname in $(jq -r '.workers | keys[]' "${REGISTRY_FILE}" 2>/dev/null); do
            [ -z "${_wname}" ] && continue
            _minio_path="hiclaw/hiclaw-storage/agents/${_wname}/openclaw.json"
            _tmp_in="/tmp/openclaw-${_wname}-models-upgrade-in.json"
            if mc cp "${_minio_path}" "${_tmp_in}" 2>/dev/null; then
                _tmp_out="/tmp/openclaw-${_wname}-models-upgrade-out.json"
                # Idempotent merge: add missing known models, rebuild aliases, set e2ee.
                # Always runs — jq deduplicates by model id, so re-runs are safe.
                jq --argjson known_models "${_KNOWN_MODELS}" \
                   --argjson e2ee "${MATRIX_E2EE_ENABLED}" '
                    .models.providers["hiclaw-gateway"].models as $existing
                    | ($existing | map(.id)) as $existing_ids
                    | ($known_models | map(select(.id as $id | $existing_ids | index($id) | not))) as $new
                    | .models.providers["hiclaw-gateway"].models = ($existing + $new)
                    | (.models.providers["hiclaw-gateway"].models | map({ ("hiclaw-gateway/" + .id): { "alias": .id } }) | add // {}) as $aliases
                    | .agents.defaults.models = ((.agents.defaults.models // {}) + $aliases)
                    | .channels.matrix.encryption = $e2ee
                ' "${_tmp_in}" > "${_tmp_out}" 2>/dev/null
                if ! diff -q "${_tmp_in}" "${_tmp_out}" > /dev/null 2>&1; then
                    if mc cp "${_tmp_out}" "${_minio_path}" 2>/dev/null; then
                        _new_count=$(jq '.models.providers["hiclaw-gateway"].models | length' "${_tmp_out}" 2>/dev/null)
                        log "Worker ${_wname}: upgraded openclaw.json (models: ${_new_count}, e2ee: ${MATRIX_E2EE_ENABLED})"
                    fi
                fi
                rm -f "${_tmp_in}" "${_tmp_out}"
            fi
        done
    fi
fi

# ============================================================
# Recreate Worker containers as needed after Manager restart.
# Manager IP may change on restart; Workers use ExtraHosts pointing to Manager IP,
# so any worker whose ExtraHosts IP no longer matches must be recreated.
# ============================================================
if container_api_available; then
    REGISTRY_FILE="/root/manager-workspace/workers-registry.json"
    _manager_ip=$(container_get_manager_ip)
    if [ -f "${REGISTRY_FILE}" ]; then
        for _worker_name in $(jq -r '.workers | keys[]' "${REGISTRY_FILE}" 2>/dev/null); do
            [ -z "${_worker_name}" ] && continue

            # Skip remote workers — they are not Manager-managed containers.
            _deployment=$(jq -r --arg w "${_worker_name}" '.workers[$w].deployment // "local"' "${REGISTRY_FILE}" 2>/dev/null)
            if [ "${_deployment}" = "remote" ]; then
                log "Worker ${_worker_name} is remote, skipping container recreate"
                continue
            fi

            _status=$(container_status_worker "${_worker_name}")
            if [ "${_status}" = "running" ]; then
                # Check if ExtraHosts IP still matches current Manager IP.
                # If ExtraHosts is empty the worker uses real DNS — no recreate needed.
                _inspect=$(_api GET "/containers/${WORKER_CONTAINER_PREFIX}${_worker_name}/json" 2>/dev/null)
                _extra_hosts_len=$(echo "${_inspect}" | jq -r '.HostConfig.ExtraHosts | length' 2>/dev/null)
                if [ -z "${_extra_hosts_len}" ] || [ "${_extra_hosts_len}" = "0" ]; then
                    log "Worker running (no ExtraHosts, real DNS): ${_worker_name}, skipping"
                    continue
                fi
                _worker_ip=$(echo "${_inspect}" | jq -r '.HostConfig.ExtraHosts[0]' 2>/dev/null | cut -d: -f2)
                if [ "${_worker_ip}" = "${_manager_ip}" ]; then
                    log "Worker running with current Manager IP (${_manager_ip}): ${_worker_name}, skipping"
                    continue
                fi
                log "Worker running but Manager IP changed (${_worker_ip} -> ${_manager_ip}): ${_worker_name}, recreating..."
            else
                # Container missing or stopped — always recreate.
                log "Worker container ${_status}: ${_worker_name}, recreating..."
            fi
            _creds_file="/data/worker-creds/${_worker_name}.env"
            if [ -f "${_creds_file}" ]; then
                source "${_creds_file}"
                _runtime=$(jq -r --arg w "${_worker_name}" '.workers[$w].runtime // "openclaw"' "${REGISTRY_FILE}" 2>/dev/null)
                _recreated=false
                for _attempt in 1 2 3; do
                    if [ "${_runtime}" = "copaw" ]; then
                        container_create_copaw_worker "${_worker_name}" "${_worker_name}" "${WORKER_MINIO_PASSWORD}" 2>&1 && _recreated=true && break
                    else
                        container_create_worker "${_worker_name}" "${_worker_name}" "${WORKER_MINIO_PASSWORD}" 2>&1 && _recreated=true && break
                    fi
                    log "  Attempt ${_attempt}/3 failed for ${_worker_name}, retrying in $((5 * _attempt))s..."
                    sleep $((5 * _attempt))
                done
                if [ "${_recreated}" = true ]; then
                    log "  Recreated ${_runtime} worker: ${_worker_name}"
                else
                    log "  ERROR: Failed to recreate ${_worker_name} after 3 attempts"
                fi
            else
                log "  WARNING: No credentials found for ${_worker_name} (${_creds_file} missing), skipping"
            fi
        done
    fi
fi

# ============================================================
# Notify workers of builtin updates if upgrade happened
# Builtin files (AGENTS.md, skills) are already synced by upgrade-builtins.sh
#
# Cooldown: skip notification if the last successful notify was within
# NOTIFY_COOLDOWN_SECS (default 3600s / 1 hour). This prevents repeated
# notifications when the Manager crash-loops and re-runs upgrade-builtins
# on every restart (e.g. IMAGE_VERSION=latest always triggers upgrade).
# ============================================================
NOTIFY_COOLDOWN_SECS="${HICLAW_NOTIFY_COOLDOWN_SECS:-3600}"
NOTIFY_TS_FILE="/root/manager-workspace/.last-worker-notify-ts"

if [ -f /root/manager-workspace/.upgrade-pending-worker-notify ]; then
    _now=$(date +%s)
    _last_notify=$(cat "${NOTIFY_TS_FILE}" 2>/dev/null || echo "0")
    _elapsed=$(( _now - _last_notify ))

    if [ "${_elapsed}" -lt "${NOTIFY_COOLDOWN_SECS}" ]; then
        log "Skipping worker builtin notification (last notify ${_elapsed}s ago, cooldown ${NOTIFY_COOLDOWN_SECS}s)"
        rm -f /root/manager-workspace/.upgrade-pending-worker-notify
    else
        log "Notifying workers about builtin updates..."
        REGISTRY_FILE="/root/manager-workspace/workers-registry.json"
        _notify_ok=false
        if [ -f "${REGISTRY_FILE}" ]; then
            for _worker_name in $(jq -r '.workers | keys[]' "${REGISTRY_FILE}" 2>/dev/null); do
                [ -z "${_worker_name}" ] && continue
                _room_id=$(jq -r --arg w "${_worker_name}" '.workers[$w].room_id // empty' "${REGISTRY_FILE}" 2>/dev/null)
                if [ -n "${_room_id}" ]; then
                    _worker_id="@${_worker_name}:${MATRIX_DOMAIN}"
                    _txn_id="upgrade-$(date +%s%N)"
                    _msg="@${_worker_name}:${MATRIX_DOMAIN} Manager upgraded builtin files (AGENTS.md, skills). Please use your file-sync skill to sync the latest config."
                    curl -sf -X PUT \
                        "http://127.0.0.1:6167/_matrix/client/v3/rooms/${_room_id}/send/m.room.message/${_txn_id}" \
                        -H "Authorization: Bearer ${MANAGER_TOKEN}" \
                        -H 'Content-Type: application/json' \
                        -d "{\"msgtype\":\"m.text\",\"body\":\"${_msg}\",\"m.mentions\":{\"user_ids\":[\"${_worker_id}\"]}}" \
                        > /dev/null 2>&1 \
                        && { log "  Notified ${_worker_name}"; _notify_ok=true; } \
                        || log "  WARNING: Failed to notify ${_worker_name}"
                fi
            done
        fi
        # Record timestamp only if at least one notification succeeded
        if [ "${_notify_ok}" = true ]; then
            echo "${_now}" > "${NOTIFY_TS_FILE}"
        fi
        rm -f /root/manager-workspace/.upgrade-pending-worker-notify
    fi
fi

# ============================================================
# Start OpenClaw Manager Agent
# ============================================================
log "Starting Manager Agent (OpenClaw)..."

# HOME is already set to /root/manager-workspace via docker run -e HOME=...
export OPENCLAW_CONFIG_PATH="/root/manager-workspace/openclaw.json"

# Symlink to default OpenClaw config path so CLI commands find the config
mkdir -p "${HOME}/.openclaw"
ln -sf "/root/manager-workspace/openclaw.json" "${HOME}/.openclaw/openclaw.json"

# Ensure host credential symlinks exist under HOME
if [ -d "/host-share" ]; then
    [ -f "/host-share/.gitconfig" ] && ln -sf "/host-share/.gitconfig" "${HOME}/.gitconfig"
fi

log "HOME=${HOME} (manager-workspace, host-mounted)"
cd "${HOME}"

# Clean orphaned session write locks (e.g. from SIGKILL or crash before exit handlers)
# Prevents "session file locked (timeout 10000ms)" when PID was reused
find "${HOME}/.openclaw/agents" -name "*.jsonl.lock" -delete 2>/dev/null || true
log "Cleaned up any orphaned session write locks"

# Clean Matrix crypto storage (SQLite WAL may be corrupted after unclean shutdown)
# Crypto state is re-negotiated on startup; losing it only means re-establishing E2EE sessions
rm -rf "${HOME}/.openclaw/matrix" 2>/dev/null || true
log "Cleaned Matrix crypto storage (will re-establish E2EE sessions)"

exec openclaw gateway run --verbose --force
