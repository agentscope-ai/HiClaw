#!/bin/bash
# generate-worker-config.sh - Generate Worker openclaw.json from template
#
# Usage:
#   generate-worker-config.sh <WORKER_NAME> <MATRIX_TOKEN> <GATEWAY_KEY> [MODEL_ID]
#
# Reads env vars: HICLAW_MATRIX_DOMAIN, HICLAW_AI_GATEWAY_DOMAIN, HICLAW_ADMIN_USER, HICLAW_DEFAULT_MODEL
# Output: /root/hiclaw-fs/agents/<WORKER_NAME>/openclaw.json

set -e
source /opt/hiclaw/scripts/lib/base.sh

WORKER_NAME="$1"
WORKER_MATRIX_TOKEN="$2"
WORKER_GATEWAY_KEY="$3"
MODEL_NAME="${4:-${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}}"
# Strip provider prefix if caller passed "hiclaw-gateway/<model>" by mistake
MODEL_NAME="${MODEL_NAME#hiclaw-gateway/}"

if [ -z "${WORKER_NAME}" ] || [ -z "${WORKER_MATRIX_TOKEN}" ] || [ -z "${WORKER_GATEWAY_KEY}" ]; then
    echo "Usage: generate-worker-config.sh <WORKER_NAME> <MATRIX_TOKEN> <GATEWAY_KEY> [MODEL_ID]"
    exit 1
fi

MATRIX_DOMAIN="${HICLAW_MATRIX_DOMAIN:-matrix-local.hiclaw.io:8080}"
AI_GATEWAY_DOMAIN="${HICLAW_AI_GATEWAY_DOMAIN:-aigw-local.hiclaw.io}"
ADMIN_USER="${HICLAW_ADMIN_USER:-admin}"

# Matrix Domain for user IDs (keep original port like :9080)
# Matrix Server for connection uses internal port 8080
MATRIX_DOMAIN_FOR_ID="${MATRIX_DOMAIN}"
MATRIX_SERVER_PORT="8080"

case "${MODEL_NAME}" in
    gpt-5.3-codex|gpt-5-mini|gpt-5-nano)
        CTX=400000; MAX=128000 ;;
    claude-opus-4-6)
        CTX=1000000; MAX=128000 ;;
    claude-sonnet-4-6)
        CTX=1000000; MAX=64000 ;;
    claude-haiku-4-5)
        CTX=200000; MAX=64000 ;;
    qwen3.5-plus)
        CTX=200000; MAX=64000 ;;
    deepseek-chat|deepseek-reasoner|kimi-k2.5)
        CTX=256000; MAX=128000 ;;
    glm-5|MiniMax-M2.5|MiniMax-M2.5-highspeed)
        CTX=200000; MAX=128000 ;;
    *)
        CTX=150000; MAX=128000 ;;
esac

# Resolve input modalities: only vision-capable models get "image"
case "${MODEL_NAME}" in
    gpt-5.4|gpt-5.3-codex|gpt-5-mini|gpt-5-nano|claude-opus-4-6|claude-sonnet-4-6|claude-haiku-4-5|qwen3.5-plus|kimi-k2.5)
        INPUT='["text", "image"]' ;;
    *)
        INPUT='["text"]' ;;
esac

GATEWAY_AUTH_TOKEN=$(openssl rand -hex 32)

export WORKER_NAME
export WORKER_GATEWAY_AUTH_TOKEN="${GATEWAY_AUTH_TOKEN}"
export WORKER_MATRIX_TOKEN
export WORKER_GATEWAY_KEY
# Matrix Server URL uses internal port 8080 for Docker network
export HICLAW_MATRIX_SERVER="http://${MATRIX_DOMAIN%%:*}:${MATRIX_SERVER_PORT}"
# Matrix Domain for user IDs keeps original port (e.g., :9080)
export HICLAW_MATRIX_DOMAIN="${MATRIX_DOMAIN_FOR_ID}"
export HICLAW_AI_GATEWAY="http://${AI_GATEWAY_DOMAIN}:8080"
export HICLAW_ADMIN_USER="${ADMIN_USER}"
export HICLAW_DEFAULT_MODEL="${MODEL_NAME}"
export MODEL_REASONING=true
export MODEL_CONTEXT_WINDOW="${CTX}"
export MODEL_MAX_TOKENS="${MAX}"
export MODEL_INPUT="${INPUT}"

OUTPUT_DIR="/root/hiclaw-fs/agents/${WORKER_NAME}"
mkdir -p "${OUTPUT_DIR}"

envsubst < /opt/hiclaw/agent/skills/worker-management/references/worker-openclaw.json.tmpl > "${OUTPUT_DIR}/openclaw.json"

log "Generated ${OUTPUT_DIR}/openclaw.json (model=${MODEL_NAME}, ctx=${CTX}, max=${MAX})"
