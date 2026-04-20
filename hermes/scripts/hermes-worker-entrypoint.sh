#!/bin/bash
# hermes-worker-entrypoint.sh - Hermes Worker Agent container startup
# Reads config from environment variables and launches hermes-worker.
#
# Environment variables (set by controller during worker creation):
#   HICLAW_WORKER_NAME   - Worker name (required)
#   HICLAW_FS_ENDPOINT   - MinIO endpoint (required in local mode)
#   HICLAW_FS_ACCESS_KEY - MinIO access key (required in local mode)
#   HICLAW_FS_SECRET_KEY - MinIO secret key (required in local mode)
#   HICLAW_RUNTIME       - "aliyun" for cloud mode (uses RRSA/STS via hiclaw-env.sh)
#   TZ                   - Timezone (optional)

set -e

# Source shared environment bootstrap (provides ensure_mc_credentials in cloud mode)
source /opt/hiclaw/scripts/lib/hiclaw-env.sh 2>/dev/null || true

WORKER_NAME="${HICLAW_WORKER_NAME:?HICLAW_WORKER_NAME is required}"
INSTALL_DIR="/root/.hiclaw-worker"

log() {
    echo "[hiclaw-hermes-worker $(date '+%Y-%m-%d %H:%M:%S')] $1"
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
log "  FS bucket: ${FS_BUCKET}"

# Set up skills CLI symlink: ~/.agents/skills -> worker's skills directory
WORKER_SKILLS_DIR="${INSTALL_DIR}/${WORKER_NAME}/skills"
mkdir -p "${WORKER_SKILLS_DIR}"
mkdir -p "${HOME}/.agents"
ln -sfn "${WORKER_SKILLS_DIR}" "${HOME}/.agents/skills"

# /root/hiclaw-fs symlink so absolute paths used by Manager-side scripts also
# work inside the hermes worker container.
ln -sfn "${INSTALL_DIR}/${WORKER_NAME}" /root/hiclaw-fs 2>/dev/null || true

# Background readiness reporter — report ready once the bridge has produced
# the gateway's config.yaml (i.e. the worker can actually serve traffic).
_start_readiness_reporter() {
    [ -z "${HICLAW_CONTROLLER_URL:-}" ] && return 0

    (
        TIMEOUT=120; ELAPSED=0
        CONFIG_FILE="${INSTALL_DIR}/${WORKER_NAME}/.hermes/config.yaml"
        while [ "${ELAPSED}" -lt "${TIMEOUT}" ]; do
            if [ -f "${CONFIG_FILE}" ] && grep -q '^matrix:' "${CONFIG_FILE}" 2>/dev/null; then
                break
            fi
            sleep 5; ELAPSED=$((ELAPSED + 5))
        done

        if [ "${ELAPSED}" -ge "${TIMEOUT}" ]; then
            log "WARNING: readiness reporter timed out waiting for config after ${TIMEOUT}s"
            exit 1
        fi

        hiclaw worker report-ready
    ) &
    log "Background readiness reporter started (PID: $!)"
}

VENV="/opt/venv/hermes"
log "Starting hermes-worker: ${WORKER_NAME}"
log "  FS endpoint: ${FS_ENDPOINT}"
log "  Install dir: ${INSTALL_DIR}"
log "  Hermes venv: ${VENV}"

# Hermes-agent reads its workspace from HERMES_HOME at process start.
export HERMES_HOME="${INSTALL_DIR}/${WORKER_NAME}/.hermes"
mkdir -p "${HERMES_HOME}"

# ── Hermes CMS Plugin Configuration ──────────────────────────────────────────
# Pass observability env through to hermes-agent. Hermes uses standard OTel
# environment variables, so no per-app bootstrap file is required.
CMS_TRACES_ENABLED="$(echo "${HICLAW_CMS_TRACES_ENABLED:-false}" | tr '[:upper:]' '[:lower:]')"
if [ "${CMS_TRACES_ENABLED}" = "true" ]; then
    export OTEL_EXPORTER_OTLP_ENDPOINT="${HICLAW_CMS_ENDPOINT}"
    export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
    export OTEL_EXPORTER_OTLP_HEADERS="x-arms-license-key=${HICLAW_CMS_LICENSE_KEY},x-arms-project=${HICLAW_CMS_PROJECT},x-cms-workspace=${HICLAW_CMS_WORKSPACE}"
    export OTEL_SERVICE_NAME="${HICLAW_CMS_SERVICE_NAME:-hiclaw-worker-${WORKER_NAME}}"
    export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT="true"
    log "OTel exporter configured (endpoint=${HICLAW_CMS_ENDPOINT})"
fi

CMD_ARGS=(
    --name "${WORKER_NAME}"
    --fs "${FS_ENDPOINT}"
    --fs-key "${FS_ACCESS_KEY}"
    --fs-secret "${FS_SECRET_KEY}"
    --fs-bucket "${FS_BUCKET}"
    --install-dir "${INSTALL_DIR}"
)

_start_readiness_reporter

exec "${VENV}/bin/hermes-worker" "${CMD_ARGS[@]}"
