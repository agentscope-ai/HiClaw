#!/bin/bash
# copaw-worker-entrypoint.sh - CoPaw Worker Agent container startup
# Reads config from environment variables and launches copaw-worker
# or lite-copaw-worker.
#
# Mode selection:
#   - HICLAW_CONSOLE_PORT set   → standard mode (copaw-worker, PyPI CoPaw venv)
#   - HICLAW_CONSOLE_PORT unset → lite mode (lite-copaw-worker, lite CoPaw venv)
#
# Environment variables (set by container_create_worker in container-api.sh):
#   HICLAW_WORKER_NAME   - Worker name (required)
#   HICLAW_FS_ENDPOINT   - MinIO endpoint (required in local mode)
#   HICLAW_FS_ACCESS_KEY - MinIO access key (required in local mode)
#   HICLAW_FS_SECRET_KEY - MinIO secret key (required in local mode)
#   HICLAW_CONSOLE_PORT  - CoPaw web console port (triggers standard mode, costs ~500MB RAM)
#   HICLAW_RUNTIME       - "aliyun" for cloud mode (uses RRSA/STS via hiclaw-env.sh)
#   TZ                   - Timezone (optional)

set -e

# Source shared environment bootstrap (provides ensure_mc_credentials in cloud mode)
source /opt/hiclaw/scripts/lib/hiclaw-env.sh 2>/dev/null || true

WORKER_NAME="${HICLAW_WORKER_NAME:?HICLAW_WORKER_NAME is required}"
INSTALL_DIR="/root/.copaw-worker"
CONSOLE_PORT="${HICLAW_CONSOLE_PORT:-}"
AGENTLOOP_FS_MOUNT="${HICLAW_COPAW_AGENTLOOP_FS_MOUNT:-/tmp/alibabacloud}"
AGENTLOOP_FS_STORE="${HICLAW_COPAW_AGENTLOOP_FS_STORE:-}"

log() {
    echo "[hiclaw-copaw-worker $(date '+%Y-%m-%d %H:%M:%S')] $1"
}

# Set timezone from TZ env var
if [ -n "${TZ}" ] && [ -f "/usr/share/zoneinfo/${TZ}" ]; then
    ln -sf "/usr/share/zoneinfo/${TZ}" /etc/localtime
    echo "${TZ}" > /etc/timezone
    log "Timezone set to ${TZ}"
fi

_agentloop_fs_enabled() {
    local value
    value="$(echo "${HICLAW_COPAW_AGENTLOOP_FS_MEMORY_ENABLED:-false}" | tr '[:upper:]' '[:lower:]')"
    { [ "${value}" = "1" ] || [ "${value}" = "true" ] || [ "${value}" = "yes" ]; } \
        && [ -n "${AGENTLOOP_FS_STORE}" ]
}

_export_if_unset() {
    local target="$1"
    local value="$2"
    if [ -n "${value}" ] && [ -z "${!target:-}" ]; then
        export "${target}=${value}"
    fi
}

_export_agentloop_fs_credentials() {
    _export_if_unset "ALIBABA_CLOUD_ACCESS_KEY_ID" "${HICLAW_COPAW_AGENTLOOP_FS_ACCESS_KEY_ID:-}"
    _export_if_unset "ALIBABA_CLOUD_ACCESS_KEY_SECRET" "${HICLAW_COPAW_AGENTLOOP_FS_ACCESS_KEY_SECRET:-}"
    _export_if_unset "ALIBABA_CLOUD_SECURITY_TOKEN" "${HICLAW_COPAW_AGENTLOOP_FS_SECURITY_TOKEN:-}"
    _export_if_unset "ALIBABA_CLOUD_ROLE_ARN" "${HICLAW_COPAW_AGENTLOOP_FS_ROLE_ARN:-}"
    _export_if_unset "ALIBABA_CLOUD_OIDC_PROVIDER_ARN" "${HICLAW_COPAW_AGENTLOOP_FS_OIDC_PROVIDER_ARN:-}"
    _export_if_unset "ALIBABA_CLOUD_OIDC_TOKEN_FILE" "${HICLAW_COPAW_AGENTLOOP_FS_OIDC_TOKEN_FILE:-}"
    _export_if_unset "ALIBABA_CLOUD_ROLE_SESSION_NAME" "${HICLAW_COPAW_AGENTLOOP_FS_ROLE_SESSION_NAME:-}"
    _export_if_unset "ALIBABA_CLOUD_REGION_ID" "${HICLAW_COPAW_AGENTLOOP_FS_REGION_ID:-${HICLAW_REGION:-}}"
    _export_if_unset "CMS_WORKSPACE" "${HICLAW_COPAW_AGENTLOOP_FS_CMS_WORKSPACE:-}"
    _export_if_unset "CMS_ENDPOINT" "${HICLAW_COPAW_AGENTLOOP_FS_CMS_ENDPOINT:-}"
}

_start_agentloop_fs() {
    _agentloop_fs_enabled || return 0

    if ! command -v alibabacloud-agent-fs >/dev/null 2>&1; then
        log "WARNING: AgentLoop FS enabled but alibabacloud-agent-fs is not installed"
        return 0
    fi

    _export_agentloop_fs_credentials

    mkdir -p "${AGENTLOOP_FS_MOUNT}"
    log "Starting AgentLoop FS at ${AGENTLOOP_FS_MOUNT} (store: ${AGENTLOOP_FS_STORE})"

    local log_file="/tmp/${WORKER_NAME}-agentloop-fs.log"
    alibabacloud-agent-fs "${AGENTLOOP_FS_MOUNT}" >"${log_file}" 2>&1 &
    local fs_pid=$!

    for _ in $(seq 1 10); do
        if [ -f "${AGENTLOOP_FS_MOUNT}/_help.txt" ]; then
            log "AgentLoop FS is ready (PID: ${fs_pid})"
            return 0
        fi
        if ! kill -0 "${fs_pid}" 2>/dev/null; then
            break
        fi
        sleep 1
    done

    log "WARNING: AgentLoop FS mount did not become ready; CoPaw will continue with local memory fallback"
    if [ -f "${log_file}" ]; then
        tail -n 20 "${log_file}" 2>/dev/null | sed 's/^/[agentloop-fs] /'
    fi
    return 0
}

# ── Credential setup ─────────────────────────────────────────────────────────
# Cloud mode: RRSA/STS credentials via MC_HOST_hiclaw (set by ensure_mc_credentials).
# FileSync._ensure_alias() detects MC_HOST_hiclaw and skips mc alias set.
# Local mode: explicit FS endpoint/key/secret passed via CLI args.
if [ "${HICLAW_RUNTIME:-}" = "aliyun" ]; then
    log "Cloud mode: configuring OSS credentials via RRSA..."
    ensure_mc_credentials || { log "ERROR: Failed to obtain OSS credentials"; exit 1; }
    # CLI requires --fs/--fs-key/--fs-secret but they are unused when MC_HOST_hiclaw is set
    FS_ENDPOINT="https://oss-placeholder.aliyuncs.com"
    FS_ACCESS_KEY="rrsa"
    FS_SECRET_KEY="rrsa"
    FS_BUCKET="${HICLAW_OSS_BUCKET:-hiclaw-cloud-storage}"
    log "  OSS bucket: ${FS_BUCKET}"
else
    FS_ENDPOINT="${HICLAW_FS_ENDPOINT:?HICLAW_FS_ENDPOINT is required}"
    FS_ACCESS_KEY="${HICLAW_FS_ACCESS_KEY:?HICLAW_FS_ACCESS_KEY is required}"
    FS_SECRET_KEY="${HICLAW_FS_SECRET_KEY:?HICLAW_FS_SECRET_KEY is required}"
    FS_BUCKET="hiclaw-storage"
fi

# Set up skills CLI symlink: ~/.agents/skills -> worker's skills directory
# This makes `skills add -g` install skills into the worker's MinIO-synced skills/ dir
WORKER_SKILLS_DIR="${INSTALL_DIR}/${WORKER_NAME}/skills"
mkdir -p "${WORKER_SKILLS_DIR}"
mkdir -p "${HOME}/.agents"
ln -sfn "${WORKER_SKILLS_DIR}" "${HOME}/.agents/skills"

_start_agentloop_fs

if [ -n "${CONSOLE_PORT}" ]; then
    # ---------- Standard mode: copaw-worker (PyPI CoPaw venv, with console) ----------
    VENV="/opt/venv/standard"
    log "Starting copaw-worker: ${WORKER_NAME}"
    log "  FS endpoint: ${FS_ENDPOINT}"
    log "  Install dir: ${INSTALL_DIR}"
    log "  Console port: ${CONSOLE_PORT}"
    log "  CoPaw: standard (${VENV})"

    exec "${VENV}/bin/copaw-worker" \
        --name "${WORKER_NAME}" \
        --fs "${FS_ENDPOINT}" \
        --fs-key "${FS_ACCESS_KEY}" \
        --fs-secret "${FS_SECRET_KEY}" \
        --fs-bucket "${FS_BUCKET}" \
        --install-dir "${INSTALL_DIR}" \
        --console-port "${CONSOLE_PORT}"
else
    # ---------- Lite mode: lite CoPaw venv, headless ----------
    VENV="/opt/venv/lite"
    log "Starting copaw-worker: ${WORKER_NAME}"
    log "  FS endpoint: ${FS_ENDPOINT}"
    log "  Install dir: ${INSTALL_DIR}"
    log "  CoPaw: lite (${VENV})"

    exec "${VENV}/bin/copaw-worker" \
        --name "${WORKER_NAME}" \
        --fs "${FS_ENDPOINT}" \
        --fs-key "${FS_ACCESS_KEY}" \
        --fs-secret "${FS_SECRET_KEY}" \
        --fs-bucket "${FS_BUCKET}" \
        --install-dir "${INSTALL_DIR}"
fi
