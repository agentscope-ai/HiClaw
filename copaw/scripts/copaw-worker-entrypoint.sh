#!/bin/bash
# copaw-worker-entrypoint.sh - CoPaw Worker Agent container startup
# Reads config from environment variables and launches copaw-worker
# or lite-copaw-worker.
#
# Mode selection:
#   - HICLAW_CONSOLE_PORT set   → standard mode (copaw-worker, PyPI CoPaw venv)
#   - HICLAW_CONSOLE_PORT unset → lite mode (lite-copaw-worker, lite CoPaw venv)
#
# Environment variables (set by controller during worker creation):
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

log() {
    echo "[hiclaw-copaw-worker $(date '+%Y-%m-%d %H:%M:%S')] $1"
}

# Set timezone from TZ env var
if [ -n "${TZ}" ] && [ -f "/usr/share/zoneinfo/${TZ}" ]; then
    ln -sf "/usr/share/zoneinfo/${TZ}" /etc/localtime
    echo "${TZ}" > /etc/timezone
    log "Timezone set to ${TZ}"
fi

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
    FS_BUCKET="${HICLAW_FS_BUCKET:-hiclaw-cloud-storage}"
    log "  OSS bucket: ${FS_BUCKET}"
else
    FS_ENDPOINT="${HICLAW_FS_ENDPOINT:?HICLAW_FS_ENDPOINT is required}"
    FS_ACCESS_KEY="${HICLAW_FS_ACCESS_KEY:?HICLAW_FS_ACCESS_KEY is required}"
    FS_SECRET_KEY="${HICLAW_FS_SECRET_KEY:?HICLAW_FS_SECRET_KEY is required}"
    FS_BUCKET="${HICLAW_FS_BUCKET:-hiclaw-storage}"
fi

# Set up skills CLI symlink: ~/.agents/skills -> worker's skills directory
# This makes `skills add -g` install skills into the worker's MinIO-synced skills/ dir
WORKER_SKILLS_DIR="${INSTALL_DIR}/${WORKER_NAME}/skills"
mkdir -p "${WORKER_SKILLS_DIR}"
mkdir -p "${HOME}/.agents"
ln -sfn "${WORKER_SKILLS_DIR}" "${HOME}/.agents/skills"

# Create /root/hiclaw-fs symlink so scripts using absolute paths work in CoPaw
# (OpenClaw workers use /root/hiclaw-fs natively; CoPaw stores synced files under INSTALL_DIR)
ln -sfn "${INSTALL_DIR}/${WORKER_NAME}" /root/hiclaw-fs 2>/dev/null || true

# Background readiness reporter — report ready to controller when CoPaw bridge completes
_start_readiness_reporter() {
    [ -z "${HICLAW_CONTROLLER_URL:-}" ] && return 0

    (
        # Phase 1: Wait for CoPaw config to be ready (with timeout)
        TIMEOUT=120; ELAPSED=0
        CONFIG_FILE="${INSTALL_DIR}/${WORKER_NAME}/.copaw/config.json"
        while [ "${ELAPSED}" -lt "${TIMEOUT}" ]; do
            if [ -f "${CONFIG_FILE}" ] && grep -q '"channels"' "${CONFIG_FILE}" 2>/dev/null; then
                break
            fi
            sleep 5; ELAPSED=$((ELAPSED + 5))
        done

        if [ "${ELAPSED}" -ge "${TIMEOUT}" ]; then
            log "WARNING: readiness reporter timed out waiting for config after ${TIMEOUT}s"
            exit 1
        fi

        # Report ready to controller via hiclaw CLI
        hiclaw worker report-ready
    ) &
    log "Background readiness reporter started (PID: $!)"
}

if [ -n "${CONSOLE_PORT}" ]; then
    # ---------- Standard mode: copaw-worker (PyPI CoPaw venv, with console) ----------
    VENV="/opt/venv/standard"
    log "Starting copaw-worker: ${WORKER_NAME}"
    log "  FS endpoint: ${FS_ENDPOINT}"
    log "  Install dir: ${INSTALL_DIR}"
    log "  Console port: ${CONSOLE_PORT}"
    log "  CoPaw: standard (${VENV})"

    _start_readiness_reporter

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

    _start_readiness_reporter

    exec "${VENV}/bin/copaw-worker" \
        --name "${WORKER_NAME}" \
        --fs "${FS_ENDPOINT}" \
        --fs-key "${FS_ACCESS_KEY}" \
        --fs-secret "${FS_SECRET_KEY}" \
        --fs-bucket "${FS_BUCKET}" \
        --install-dir "${INSTALL_DIR}"
fi
