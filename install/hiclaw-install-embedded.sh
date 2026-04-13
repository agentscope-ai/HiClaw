#!/bin/bash
# hiclaw-install-embedded.sh - Install HiClaw in embedded (dual-container) mode
#
# This script starts:
#   1. hiclaw-controller — embedded controller container (infra + controller)
#   2. hiclaw-manager — Manager Agent container (auto-created by controller)
#
# Usage:
#   bash install/hiclaw-install-embedded.sh
#
# Required env vars (or prompted interactively):
#   HICLAW_LLM_API_KEY     — LLM provider API key
#
# Optional env vars:
#   HICLAW_LLM_PROVIDER    — LLM provider (default: qwen)
#   HICLAW_DEFAULT_MODEL   — Default model (default: qwen3.5-plus)
#   HICLAW_ADMIN_USER      — Admin username (default: admin)
#   HICLAW_ADMIN_PASSWORD  — Admin password (default: admin)
#   HICLAW_PORT_GATEWAY    — Higress gateway port (default: 18080)
#   HICLAW_PORT_CONSOLE    — Higress console port (default: 18001)
#   HICLAW_PORT_ELEMENT_WEB — Element Web port (default: 18088)
#   HICLAW_LOCAL_ONLY      — Bind to 127.0.0.1 only (default: 1)
#   HICLAW_YOLO            — Enable yolo mode (default: 0)
#   HICLAW_DATA_DIR        — Persistent data volume name (default: hiclaw-data)
#   HICLAW_WORKSPACE_DIR   — Manager workspace dir (default: ~/hiclaw-manager)
#   HICLAW_HOST_SHARE_DIR  — Host share dir (default: ~)
#   HICLAW_EMBEDDED_IMAGE  — Embedded controller image
#   HICLAW_MANAGER_IMAGE   — Manager agent image
#   HICLAW_WORKER_IMAGE    — Worker image
#   HICLAW_COPAW_WORKER_IMAGE — CoPaw worker image
#   HICLAW_DOCKER_CMD      — Docker command (default: auto-detect docker/podman)
#   HICLAW_MATRIX_E2EE     — Enable Matrix E2EE (default: 0)
#   HICLAW_NON_INTERACTIVE — Skip interactive prompts (default: 0)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ============================================================
# Load existing env file (shell env vars take priority)
# ============================================================
# Reuse saved configuration from a previous install so that secrets
# (registration token, manager password, gateway key, etc.) and
# user-customised values survive across re-installs.

_env_file="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
if [ -f "${_env_file}" ]; then
    while IFS='=' read -r key value; do
        case "${key}" in \#*|"") continue ;; esac
        # strip inline comments and surrounding whitespace
        value="${value%%#*}"
        value="${value#"${value%%[![:space:]]*}"}"
        value="${value%"${value##*[![:space:]]}"}"
        # only import if NOT already set in the current shell environment
        eval "_chk=\"\${${key}+x}\""
        if [ -z "${_chk}" ]; then
            export "${key}=${value}"
        fi
    done < "${_env_file}"
fi
unset _env_file _chk

# ============================================================
# Defaults
# ============================================================

HICLAW_VERSION="${HICLAW_VERSION:-latest}"
HICLAW_REGISTRY="${HICLAW_REGISTRY:-higress-registry.cn-hangzhou.cr.aliyuncs.com}"

HICLAW_LLM_PROVIDER="${HICLAW_LLM_PROVIDER:-qwen}"
HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
HICLAW_ADMIN_USER="${HICLAW_ADMIN_USER:-admin}"
HICLAW_ADMIN_PASSWORD="${HICLAW_ADMIN_PASSWORD:-admin123}"

HICLAW_PORT_GATEWAY="${HICLAW_PORT_GATEWAY:-18080}"
HICLAW_PORT_CONSOLE="${HICLAW_PORT_CONSOLE:-18001}"
HICLAW_PORT_ELEMENT_WEB="${HICLAW_PORT_ELEMENT_WEB:-18088}"
HICLAW_PORT_MANAGER_CONSOLE="${HICLAW_PORT_MANAGER_CONSOLE:-18888}"
HICLAW_LOCAL_ONLY="${HICLAW_LOCAL_ONLY:-1}"
HICLAW_YOLO="${HICLAW_YOLO:-0}"
HICLAW_NON_INTERACTIVE="${HICLAW_NON_INTERACTIVE:-0}"
HICLAW_MATRIX_E2EE="${HICLAW_MATRIX_E2EE:-0}"

HICLAW_DATA_DIR="${HICLAW_DATA_DIR:-hiclaw-data}"
HICLAW_WORKSPACE_DIR="${HICLAW_WORKSPACE_DIR:-${HOME}/hiclaw-manager}"
HICLAW_HOST_SHARE_DIR="${HICLAW_HOST_SHARE_DIR:-${HOME}}"

# Internal port: 8080 (Higress gateway inside the container).
# AI Gateway and FS domains use the internal port because containers
# talk to the gateway over the container network, not via host-mapped ports.
# Matrix domain uses the external port because it is only an identity
# string embedded in Matrix user IDs (@user:domain:port), not an HTTP endpoint.
HICLAW_INTERNAL_GATEWAY_PORT=8080
HICLAW_MATRIX_DOMAIN="${HICLAW_MATRIX_DOMAIN:-matrix-local.hiclaw.io:${HICLAW_PORT_GATEWAY}}"
HICLAW_AI_GATEWAY_DOMAIN="${HICLAW_AI_GATEWAY_DOMAIN:-aigw-local.hiclaw.io:${HICLAW_INTERNAL_GATEWAY_PORT}}"
HICLAW_FS_DOMAIN="${HICLAW_FS_DOMAIN:-fs-local.hiclaw.io:${HICLAW_INTERNAL_GATEWAY_PORT}}"

# Normalize domain variables: append port if no port specified
# (handles legacy env files that stored domains without port)
case "${HICLAW_MATRIX_DOMAIN}" in *:*) ;; *) HICLAW_MATRIX_DOMAIN="${HICLAW_MATRIX_DOMAIN}:${HICLAW_PORT_GATEWAY}" ;; esac
case "${HICLAW_AI_GATEWAY_DOMAIN}" in *:*) ;; *) HICLAW_AI_GATEWAY_DOMAIN="${HICLAW_AI_GATEWAY_DOMAIN}:${HICLAW_INTERNAL_GATEWAY_PORT}" ;; esac
case "${HICLAW_FS_DOMAIN}" in *:*) ;; *) HICLAW_FS_DOMAIN="${HICLAW_FS_DOMAIN}:${HICLAW_INTERNAL_GATEWAY_PORT}" ;; esac

HICLAW_MANAGER_RUNTIME="${HICLAW_MANAGER_RUNTIME:-openclaw}"

EMBEDDED_CTR="hiclaw-controller"
AGENT_CTR="hiclaw-manager"
NETWORK="hiclaw-net"

# ============================================================
# Image resolution
# ============================================================

EMBEDDED_IMAGE="${HICLAW_EMBEDDED_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-embedded:${HICLAW_VERSION}}"
WORKER_IMAGE="${HICLAW_WORKER_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-worker:${HICLAW_VERSION}}"
COPAW_WORKER_IMAGE="${HICLAW_COPAW_WORKER_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-copaw-worker:${HICLAW_VERSION}}"

# Select Manager image based on runtime
if [ "${HICLAW_MANAGER_RUNTIME}" = "copaw" ]; then
    MANAGER_IMAGE="${HICLAW_MANAGER_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-manager-copaw:${HICLAW_VERSION}}"
else
    MANAGER_IMAGE="${HICLAW_MANAGER_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-manager:${HICLAW_VERSION}}"
fi

# ============================================================
# Helpers
# ============================================================

log() { echo "[hiclaw-embedded] $*"; }
err() { echo "[hiclaw-embedded] ERROR: $*" >&2; }

generate_key() {
    openssl rand -hex 32
}

detect_docker() {
    if [ -n "${HICLAW_DOCKER_CMD:-}" ]; then
        echo "${HICLAW_DOCKER_CMD}"
        return
    fi
    if command -v docker &>/dev/null; then
        echo "docker"
    elif command -v podman &>/dev/null; then
        echo "podman"
    else
        err "Neither docker nor podman found in PATH"
        exit 1
    fi
}

detect_socket() {
    local docker_cmd="$1"
    if [ "${docker_cmd}" = "podman" ]; then
        local podman_sock
        podman_sock=$(podman info --format '{{.Host.RemoteSocket.Path}}' 2>/dev/null || true)
        if [ -n "${podman_sock}" ] && [ -S "${podman_sock}" ]; then
            echo "${podman_sock}"
            return
        fi
        for sock in /run/podman/podman.sock /var/run/podman/podman.sock "${HOME}/.local/share/containers/podman/machine/podman.sock"; do
            if [ -S "${sock}" ]; then
                echo "${sock}"
                return
            fi
        done
    fi
    if [ -S /var/run/docker.sock ]; then
        echo "/var/run/docker.sock"
        return
    fi
    err "No container socket found"
    exit 1
}

wait_for_url() {
    local url="$1" ctr="$2" max_wait="${3:-120}" desc="${4:-service}"
    local elapsed=0
    log "Waiting for ${desc} (${url}) ..."
    while [ $elapsed -lt $max_wait ]; do
        if ${DOCKER_CMD} exec "${ctr}" curl -sf "${url}" >/dev/null 2>&1; then
            log "${desc} is ready (${elapsed}s)"
            return 0
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    err "${desc} not ready after ${max_wait}s"
    return 1
}

# ============================================================
# Pre-flight
# ============================================================

DOCKER_CMD=$(detect_docker)
CONTAINER_SOCK=$(detect_socket "${DOCKER_CMD}")
log "Using: ${DOCKER_CMD} (socket: ${CONTAINER_SOCK})"

# Require LLM API key
if [ -z "${HICLAW_LLM_API_KEY:-}" ] && [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
    read -rp "Enter LLM API key: " HICLAW_LLM_API_KEY
fi
if [ -z "${HICLAW_LLM_API_KEY:-}" ]; then
    err "HICLAW_LLM_API_KEY is required"
    exit 1
fi

# Generate secrets
HICLAW_MANAGER_PASSWORD="${HICLAW_MANAGER_PASSWORD:-$(generate_key)}"
HICLAW_REGISTRATION_TOKEN="${HICLAW_REGISTRATION_TOKEN:-$(generate_key)}"
HICLAW_MINIO_USER="${HICLAW_MINIO_USER:-${HICLAW_ADMIN_USER}}"
HICLAW_MINIO_PASSWORD="${HICLAW_MINIO_PASSWORD:-${HICLAW_ADMIN_PASSWORD}}"
HICLAW_MANAGER_GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY:-$(generate_key)}"

# Workspace dir
mkdir -p "${HICLAW_WORKSPACE_DIR}"

# Port prefix
if [ "${HICLAW_LOCAL_ONLY}" = "1" ]; then
    PORT_PREFIX="127.0.0.1:"
else
    PORT_PREFIX=""
fi

# ============================================================
# Cleanup existing containers
# ============================================================

log "Cleaning up existing containers..."
for ctr in "${AGENT_CTR}" "${EMBEDDED_CTR}"; do
    ${DOCKER_CMD} stop "${ctr}" 2>/dev/null || true
    ${DOCKER_CMD} rm -f "${ctr}" 2>/dev/null || true
done

# Clean up worker containers
for w in $(${DOCKER_CMD} ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^hiclaw-worker-' || true); do
    ${DOCKER_CMD} stop "${w}" 2>/dev/null || true
    ${DOCKER_CMD} rm -f "${w}" 2>/dev/null || true
done

# ============================================================
# Network
# ============================================================

${DOCKER_CMD} network inspect "${NETWORK}" >/dev/null 2>&1 || ${DOCKER_CMD} network create "${NETWORK}"

# ============================================================
# Start embedded controller container
# ============================================================

log "Starting embedded controller: ${EMBEDDED_CTR}"

ENV_ARGS=(
    -e "HICLAW_ADMIN_USER=${HICLAW_ADMIN_USER}"
    -e "HICLAW_ADMIN_PASSWORD=${HICLAW_ADMIN_PASSWORD}"
    -e "HICLAW_MANAGER_PASSWORD=${HICLAW_MANAGER_PASSWORD}"
    -e "HICLAW_REGISTRATION_TOKEN=${HICLAW_REGISTRATION_TOKEN}"
    -e "HICLAW_MINIO_USER=${HICLAW_MINIO_USER}"
    -e "HICLAW_MINIO_PASSWORD=${HICLAW_MINIO_PASSWORD}"
    -e "HICLAW_LLM_PROVIDER=${HICLAW_LLM_PROVIDER}"
    -e "HICLAW_LLM_API_KEY=${HICLAW_LLM_API_KEY}"
    -e "HICLAW_DEFAULT_MODEL=${HICLAW_DEFAULT_MODEL}"
    -e "HICLAW_MANAGER_GATEWAY_KEY=${HICLAW_MANAGER_GATEWAY_KEY}"
    -e "HICLAW_MANAGER_RUNTIME=${HICLAW_MANAGER_RUNTIME}"
    -e "HICLAW_MANAGER_IMAGE=${MANAGER_IMAGE}"
    -e "HICLAW_WORKER_IMAGE=${WORKER_IMAGE}"
    -e "HICLAW_COPAW_WORKER_IMAGE=${COPAW_WORKER_IMAGE}"
    -e "HICLAW_MATRIX_DOMAIN=${HICLAW_MATRIX_DOMAIN}"
    -e "HICLAW_ELEMENT_HOMESERVER_URL=http://127.0.0.1:${HICLAW_PORT_GATEWAY}"
    -e "HICLAW_MATRIX_URL=http://127.0.0.1:6167"
    -e "HICLAW_MATRIX_E2EE=${HICLAW_MATRIX_E2EE}"
    -e "HICLAW_MINIO_ENDPOINT=http://127.0.0.1:9000"
    -e "HICLAW_MINIO_BUCKET=hiclaw-storage"
    -e "HICLAW_STORAGE_PREFIX=hiclaw/hiclaw-storage"
    -e "HICLAW_FS_ENDPOINT=http://127.0.0.1:9000"
    -e "HICLAW_AI_GATEWAY_URL=http://${HICLAW_AI_GATEWAY_DOMAIN}"
    -e "HICLAW_CONTROLLER_URL=http://${EMBEDDED_CTR}:8090"
    -e "HICLAW_DOCKER_NETWORK=${NETWORK}"
    -e "HICLAW_WORKSPACE_DIR=${HICLAW_WORKSPACE_DIR}"
    -e "HICLAW_HOST_SHARE_DIR=${HICLAW_HOST_SHARE_DIR}"
    -e "HICLAW_MANAGER_ENABLED=true"
)

# Timezone
if [ -n "${TZ:-}" ]; then
    ENV_ARGS+=(-e "TZ=${TZ}")
elif [ -f /etc/timezone ]; then
    ENV_ARGS+=(-e "TZ=$(cat /etc/timezone)")
fi

# Yolo mode forwarded to agent via ExtraEnv
if [ "${HICLAW_YOLO}" = "1" ]; then
    ENV_ARGS+=(-e "HICLAW_YOLO=1")
fi

# Optional: GitHub token
if [ -n "${HICLAW_GITHUB_TOKEN:-}" ]; then
    ENV_ARGS+=(-e "HICLAW_GITHUB_TOKEN=${HICLAW_GITHUB_TOKEN}")
fi

# Optional: embedding model
if [ -n "${HICLAW_EMBEDDING_MODEL:-}" ]; then
    ENV_ARGS+=(-e "HICLAW_EMBEDDING_MODEL=${HICLAW_EMBEDDING_MODEL}")
fi

# Optional: OpenAI-compatible base URL
if [ -n "${HICLAW_OPENAI_BASE_URL:-}" ]; then
    ENV_ARGS+=(-e "HICLAW_OPENAI_BASE_URL=${HICLAW_OPENAI_BASE_URL}")
fi

# Optional: language
if [ -n "${HICLAW_LANGUAGE:-}" ]; then
    ENV_ARGS+=(-e "HICLAW_LANGUAGE=${HICLAW_LANGUAGE}")
fi

${DOCKER_CMD} run -d \
    --name "${EMBEDDED_CTR}" \
    --network "${NETWORK}" \
    --network-alias matrix-local.hiclaw.io \
    --network-alias aigw-local.hiclaw.io \
    --network-alias fs-local.hiclaw.io \
    "${ENV_ARGS[@]}" \
    -v "${CONTAINER_SOCK}:/var/run/docker.sock" \
    --security-opt label=disable \
    -v "${HICLAW_DATA_DIR}:/data" \
    -v "${HICLAW_WORKSPACE_DIR}:/root/hiclaw-fs/agents/manager" \
    -p "${PORT_PREFIX}${HICLAW_PORT_GATEWAY}:8080" \
    -p "${PORT_PREFIX}${HICLAW_PORT_CONSOLE}:8001" \
    -p "${PORT_PREFIX}${HICLAW_PORT_ELEMENT_WEB}:8088" \
    --restart unless-stopped \
    "${EMBEDDED_IMAGE}"

log "Embedded controller container started: ${EMBEDDED_CTR}"

# ============================================================
# Wait for infrastructure
# ============================================================

wait_for_url "http://127.0.0.1:6167/_tuwunel/server_version" "${EMBEDDED_CTR}" 120 "Tuwunel (Matrix)" || exit 1
wait_for_url "http://127.0.0.1:9000/minio/health/live" "${EMBEDDED_CTR}" 60 "MinIO" || exit 1
wait_for_url "http://127.0.0.1:8080/status" "${EMBEDDED_CTR}" 120 "Higress Gateway" || exit 1

# ============================================================
# Wait for controller + Manager Agent auto-creation
# ============================================================

log "Waiting for hiclaw-controller to initialize and create Manager Agent..."

MAX_WAIT=300
ELAPSED=0
while [ $ELAPSED -lt $MAX_WAIT ]; do
    if ${DOCKER_CMD} ps --format '{{.Names}}' 2>/dev/null | grep -q "^${AGENT_CTR}$"; then
        log "Manager Agent container detected: ${AGENT_CTR} (${ELAPSED}s)"
        break
    fi
    sleep 3
    ELAPSED=$((ELAPSED + 3))
done

if [ $ELAPSED -ge $MAX_WAIT ]; then
    err "Manager Agent container not created after ${MAX_WAIT}s"
    log "Controller logs:"
    ${DOCKER_CMD} exec "${EMBEDDED_CTR}" tail -30 /var/log/hiclaw/hiclaw-controller.log 2>/dev/null || true
    exit 1
fi

# Wait for agent to be running
log "Waiting for Manager Agent to start..."
ELAPSED=0
while [ $ELAPSED -lt 120 ]; do
    STATE=$(${DOCKER_CMD} inspect --format '{{.State.Status}}' "${AGENT_CTR}" 2>/dev/null || echo "missing")
    if [ "${STATE}" = "running" ]; then
        log "Manager Agent is running"
        break
    fi
    sleep 2
    ELAPSED=$((ELAPSED + 2))
done

# Enable yolo mode in agent if requested
if [ "${HICLAW_YOLO}" = "1" ]; then
    sleep 5
    ${DOCKER_CMD} exec "${AGENT_CTR}" touch /root/manager-workspace/yolo-mode 2>/dev/null || true
    log "Yolo mode enabled"
fi

# ============================================================
# Summary
# ============================================================

# ============================================================
# Write env file (consumed by run-all-tests.sh load_env_file)
# ============================================================

ENV_FILE="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
cat > "${ENV_FILE}" <<EOF
# Generated by hiclaw-install-embedded.sh at $(date -u +%Y-%m-%dT%H:%M:%SZ)

# Secrets
HICLAW_ADMIN_USER=${HICLAW_ADMIN_USER}
HICLAW_ADMIN_PASSWORD=${HICLAW_ADMIN_PASSWORD}
HICLAW_MANAGER_PASSWORD=${HICLAW_MANAGER_PASSWORD}
HICLAW_MINIO_USER=${HICLAW_MINIO_USER}
HICLAW_MINIO_PASSWORD=${HICLAW_MINIO_PASSWORD}
HICLAW_REGISTRATION_TOKEN=${HICLAW_REGISTRATION_TOKEN}
HICLAW_MANAGER_GATEWAY_KEY=${HICLAW_MANAGER_GATEWAY_KEY}
HICLAW_LLM_API_KEY=${HICLAW_LLM_API_KEY}

# LLM
HICLAW_LLM_PROVIDER=${HICLAW_LLM_PROVIDER}
HICLAW_DEFAULT_MODEL=${HICLAW_DEFAULT_MODEL}
HICLAW_OPENAI_BASE_URL=${HICLAW_OPENAI_BASE_URL:-}

# Ports
HICLAW_PORT_GATEWAY=${HICLAW_PORT_GATEWAY}
HICLAW_PORT_CONSOLE=${HICLAW_PORT_CONSOLE}
HICLAW_PORT_ELEMENT_WEB=${HICLAW_PORT_ELEMENT_WEB}
HICLAW_PORT_MANAGER_CONSOLE=${HICLAW_PORT_MANAGER_CONSOLE}
HICLAW_LOCAL_ONLY=${HICLAW_LOCAL_ONLY}

# Domains
HICLAW_MATRIX_DOMAIN=${HICLAW_MATRIX_DOMAIN}
HICLAW_AI_GATEWAY_DOMAIN=${HICLAW_AI_GATEWAY_DOMAIN}
HICLAW_FS_DOMAIN=${HICLAW_FS_DOMAIN}

# Storage & workspace
HICLAW_DATA_DIR=${HICLAW_DATA_DIR}
HICLAW_WORKSPACE_DIR=${HICLAW_WORKSPACE_DIR}
HICLAW_HOST_SHARE_DIR=${HICLAW_HOST_SHARE_DIR}

# Runtime
HICLAW_MANAGER_RUNTIME=${HICLAW_MANAGER_RUNTIME}
HICLAW_MATRIX_E2EE=${HICLAW_MATRIX_E2EE}
EOF
chmod 600 "${ENV_FILE}"
log "Env file written: ${ENV_FILE}"

log ""
log "=== HiClaw Embedded Installation Complete ==="
log ""
log "Containers:"
log "  Embedded Controller: ${EMBEDDED_CTR}"
log "  Manager Agent:       ${AGENT_CTR}"
log ""
log "Ports:"
log "  AI Gateway:   ${PORT_PREFIX}${HICLAW_PORT_GATEWAY}"
log "  Console:      ${PORT_PREFIX}${HICLAW_PORT_CONSOLE}"
log "  Element Web:  ${PORT_PREFIX}${HICLAW_PORT_ELEMENT_WEB}"
log ""
log "Open Element Web at: http://localhost:${HICLAW_PORT_ELEMENT_WEB}"
log ""
