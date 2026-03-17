#!/bin/bash
# update-manager-model.sh - Hot-update the Manager Agent's model
#
# Patches ~/manager-workspace/openclaw.json in-place.
# OpenClaw detects the file change (~300ms) and reloads config automatically.
#
# Usage:
#   update-manager-model.sh <MODEL_ID> [--context-window <SIZE>] [--no-reasoning]
#
# Example:
#   update-manager-model.sh claude-sonnet-4-6
#   update-manager-model.sh my-custom-model --context-window 300000
#   update-manager-model.sh deepseek-chat --no-reasoning

set -e
source /opt/hiclaw/scripts/lib/base.sh

MODEL_NAME="${1:-}"
if [ -z "${MODEL_NAME}" ]; then
    echo "Usage: $0 <MODEL_ID> [--context-window <SIZE>] [--no-reasoning]"
    echo "Example: $0 claude-sonnet-4-6"
    echo "         $0 my-custom-model --context-window 300000"
    echo "         $0 deepseek-chat --no-reasoning"
    exit 1
fi
shift
# Strip provider prefix if caller passed "hiclaw-gateway/<model>" by mistake
MODEL_NAME="${MODEL_NAME#hiclaw-gateway/}"

CTX_OVERRIDE=""
REASONING="true"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --context-window)
            CTX_OVERRIDE="$2"
            shift 2
            ;;
        --no-reasoning)
            REASONING="false"
            shift
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

CONFIG_FILE="${HOME}/manager-workspace/openclaw.json"
if [ ! -f "${CONFIG_FILE}" ]; then
    # Fallback: openclaw.json may live directly under HOME when HOME=manager-workspace
    CONFIG_FILE="${HOME}/openclaw.json"
fi
if [ ! -f "${CONFIG_FILE}" ]; then
    echo "ERROR: Manager openclaw.json not found (tried ${HOME}/manager-workspace/openclaw.json and ${HOME}/openclaw.json)"
    exit 1
fi

# Resolve context window and max tokens
case "${MODEL_NAME}" in
    gpt-5.4)
        CTX=1050000; MAX=128000 ;;
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
    glm-5|MiniMax-M2.5)
        CTX=200000; MAX=128000 ;;
    *)
        CTX=150000; MAX=128000 ;;
esac

# Allow explicit context-window override (for unknown models)
if [ -n "${CTX_OVERRIDE:-}" ]; then
    CTX="${CTX_OVERRIDE}"
fi

# Resolve input modalities: only vision-capable models get "image"
case "${MODEL_NAME}" in
    gpt-5.4|gpt-5.3-codex|gpt-5-mini|gpt-5-nano|claude-opus-4-6|claude-sonnet-4-6|claude-haiku-4-5|qwen3.5-plus|kimi-k2.5)
        INPUT='["text", "image"]' ;;
    *)
        INPUT='["text"]' ;;
esac

log "Updating Manager model: ${MODEL_NAME} (ctx=${CTX}, max=${MAX}, reasoning=${REASONING}, input=${INPUT})"

# ── Pre-flight: verify the model is reachable via AI Gateway ──────────────────
GATEWAY_URL="http://${HICLAW_AI_GATEWAY_DOMAIN:-aigw-local.hiclaw.io}:8080/v1/chat/completions"
GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY:-}"
if [ -z "${GATEWAY_KEY}" ] && [ -f "/data/hiclaw-secrets.env" ]; then
    source /data/hiclaw-secrets.env
    GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY:-}"
fi

log "Testing model reachability: ${GATEWAY_URL} (model=${MODEL_NAME})..."
HTTP_CODE=$(curl -s -o /tmp/model-test-resp.json -w '%{http_code}' \
    -X POST "${GATEWAY_URL}" \
    -H "Authorization: Bearer ${GATEWAY_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"${MODEL_NAME}\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":1}" \
    --connect-timeout 10 --max-time 30 2>/dev/null) || HTTP_CODE="000"

if [ "${HTTP_CODE}" != "200" ]; then
    RESP_BODY=$(cat /tmp/model-test-resp.json 2>/dev/null | head -c 300 || true)
    echo "ERROR: Model test failed (HTTP ${HTTP_CODE}): ${RESP_BODY}"
    echo ""
    echo "The model '${MODEL_NAME}' is not reachable via the AI Gateway."
    if [ "${HICLAW_RUNTIME:-}" = "aliyun" ]; then
        echo "Please check the Alibaba Cloud AI Gateway console to confirm the model route is configured."
    else
        echo "Please check the Higress Console to confirm the AI route is configured for this model:"
        echo "  http://<manager-host>:8001  →  AI Routes → verify provider and model mapping"
    fi
    exit 1
fi
log "Model test passed (HTTP 200)"
rm -f /tmp/model-test-resp.json
# ─────────────────────────────────────────────────────────────────────────────

# Check if the model already exists in the models array
MODEL_EXISTS=$(jq --arg model "${MODEL_NAME}" \
    '[.models.providers["hiclaw-gateway"].models[] | select(.id == $model)] | length' \
    "${CONFIG_FILE}" 2>/dev/null)

TMP=$(mktemp)
if [ "${MODEL_EXISTS}" -gt 0 ]; then
    # Known model: switch the primary model pointer and update reasoning
    jq --arg model "${MODEL_NAME}" \
       --argjson reasoning "${REASONING}" \
       '(.models.providers["hiclaw-gateway"].models[] | select(.id == $model)).reasoning = $reasoning
        | .agents.defaults.model.primary = ("hiclaw-gateway/" + $model)
        | .agents.defaults.models["hiclaw-gateway/" + $model] = { "alias": $model }' \
       "${CONFIG_FILE}" > "${TMP}" && mv "${TMP}" "${CONFIG_FILE}"

    log "Done. Model is now: ${MODEL_NAME}"
else
    # New model: add to models array and switch primary
    jq --arg model "${MODEL_NAME}" \
       --argjson ctx "${CTX}" \
       --argjson max "${MAX}" \
       --argjson reasoning "${REASONING}" \
       --argjson input "${INPUT}" \
       '.models.providers["hiclaw-gateway"].models += [{
           "id": $model,
           "name": $model,
           "reasoning": $reasoning,
           "contextWindow": $ctx,
           "maxTokens": $max,
           "input": $input
         }]
        | .agents.defaults.model.primary = ("hiclaw-gateway/" + $model)
        | .agents.defaults.models["hiclaw-gateway/" + $model] = { "alias": $model }' \
       "${CONFIG_FILE}" > "${TMP}" && mv "${TMP}" "${CONFIG_FILE}"

    log "Done. Model '${MODEL_NAME}' has been added to the models list."
fi

echo ""
echo "RESTART_REQUIRED: Run 'openclaw gateway restart' to apply the model switch."
