#!/bin/bash
# k8s-worker-env.sh — JSON env for K8s Worker Pods (ACK Step 9)
#
# Must match the jq payload built for SAE in create-worker.sh (HICLAW_RUNTIME=aliyun branch):
# same Matrix / AI Gateway / OSS / region fields so Worker behaves like SAE after Steps 1–8.
# RRSA: on ACK, Worker RAM is bound via Worker ServiceAccount + ack-pod-identity-webhook
# (cf. SAE CreateApplication oidc_role_name).
#
# Usage: hiclaw_k8s_worker_env_json <worker_name> <worker_gateway_key> <worker_matrix_token> [runtime] [console_port]

hiclaw_k8s_worker_env_json() {
    local worker_name="$1"
    local worker_key="$2"
    local matrix_token="$3"
    local matrix_domain="${HICLAW_MATRIX_DOMAIN:-}"
    local runtime="${4:-openclaw}"
    local console_port="${5:-}"

    jq -cn \
        --arg worker_name "${worker_name}" \
        --arg worker_key "${worker_key}" \
        --arg matrix_url "${HICLAW_MATRIX_URL:-}" \
        --arg matrix_domain "${matrix_domain}" \
        --arg matrix_token "${matrix_token}" \
        --arg ai_gw_url "${HICLAW_AI_GATEWAY_URL:-}" \
        --arg oss_bucket "${HICLAW_OSS_BUCKET:-hiclaw-cloud-storage}" \
        --arg region "${HICLAW_REGION:-cn-hangzhou}" \
        --arg runtime "${runtime}" \
        --arg console_port "${console_port}" \
        '{
            "HICLAW_WORKER_NAME": $worker_name,
            "HICLAW_WORKER_GATEWAY_KEY": $worker_key,
            "HICLAW_MATRIX_URL": $matrix_url,
            "HICLAW_MATRIX_DOMAIN": $matrix_domain,
            "HICLAW_WORKER_MATRIX_TOKEN": $matrix_token,
            "HICLAW_AI_GATEWAY_URL": $ai_gw_url,
            "HICLAW_OSS_BUCKET": $oss_bucket,
            "HICLAW_REGION": $region,
            "HICLAW_RUNTIME": "aliyun"
        }
        | if $runtime == "copaw" then
            if $console_port != "" then . + { "HICLAW_CONSOLE_PORT": $console_port } else . end
          else
            . + {
                "OPENCLAW_DISABLE_BONJOUR": "1",
                "OPENCLAW_MDNS_HOSTNAME": ("hiclaw-w-" + $worker_name)
            }
          end'
}
