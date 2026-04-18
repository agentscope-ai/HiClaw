#!/bin/bash
# oss-credentials.sh - STS credential management for mc (MinIO Client)
#
# Two credential paths (checked in priority order):
#
# 1. Controller-mediated STS (cloud mode):
#    HICLAW_CONTROLLER_URL + HICLAW_WORKER_API_KEY → call controller /credentials/sts.
#    The controller obtains STS tokens from its hiclaw-credential-provider sidecar.
#
# 2. No controller creds configured → no-op (local mode, mc alias
#    configured with static credentials against MinIO/self-hosted S3).
#
# STS tokens expire after 1 hour. Credentials are cached and lazy-refreshed.
#
# Usage:
#   source /opt/hiclaw/scripts/lib/oss-credentials.sh
#   ensure_mc_credentials   # call before any mc command

_OSS_CRED_FILE="/tmp/mc-oss-credentials.env"
_OSS_CRED_REFRESH_MARGIN=600  # refresh if less than 10 minutes remaining

# --------------------------------------------------------------------------
# Path 1: STS via Controller
# --------------------------------------------------------------------------

_oss_refresh_sts_via_controller() {
    local _controller_url="${HICLAW_CONTROLLER_URL:-}"
    local resp http_code
    local sts_ak sts_sk sts_token oss_endpoint

    resp=$(curl -s -w "\n%{http_code}" -X POST "${_controller_url}/credentials/sts" \
        -H "Authorization: Bearer ${HICLAW_WORKER_API_KEY}" \
        --connect-timeout 10 --max-time 30 2>&1)

    http_code=$(echo "${resp}" | tail -1)
    resp=$(echo "${resp}" | sed '$d')

    if [ "${http_code}" != "200" ]; then
        echo "[oss-credentials] ERROR: controller STS request failed (HTTP ${http_code})" >&2
        echo "[oss-credentials] Response: ${resp}" >&2
        return 1
    fi

    sts_ak=$(echo "${resp}" | jq -r '.access_key_id')
    sts_sk=$(echo "${resp}" | jq -r '.access_key_secret')
    sts_token=$(echo "${resp}" | jq -r '.security_token')
    oss_endpoint=$(echo "${resp}" | jq -r '.oss_endpoint')

    if [ -z "${sts_ak}" ] || [ "${sts_ak}" = "null" ]; then
        echo "[oss-credentials] ERROR: Failed to parse STS credentials from controller" >&2
        echo "[oss-credentials] Response: ${resp}" >&2
        return 1
    fi

    local expires_at
    expires_at=$(( $(date +%s) + 3600 ))

    cat > "${_OSS_CRED_FILE}" <<EOF
MC_HOST_hiclaw="https://${sts_ak}:${sts_sk}:${sts_token}@${oss_endpoint}"
_OSS_CRED_EXPIRES_AT=${expires_at}
EOF
    chmod 600 "${_OSS_CRED_FILE}"

    echo "[oss-credentials] STS credentials refreshed via controller (AK prefix: ${sts_ak:0:8}..., expires: $(date -d @${expires_at} '+%H:%M:%S' 2>/dev/null || date -r ${expires_at} '+%H:%M:%S' 2>/dev/null || echo ${expires_at}))" >&2
}

# --------------------------------------------------------------------------
# Public API
# --------------------------------------------------------------------------

ensure_mc_credentials() {
    # Cloud mode: Controller URL + worker API key → controller-mediated STS
    if [ -n "${HICLAW_CONTROLLER_URL:-}" ] && [ -n "${HICLAW_WORKER_API_KEY:-}" ]; then
        _oss_ensure_refresh _oss_refresh_sts_via_controller
        return $?
    fi

    # Local mode: mc alias already configured with static credentials
    return 0
}

# Shared lazy-refresh logic: call the given refresh function only if needed.
_oss_ensure_refresh() {
    local refresh_fn="$1"
    local now needs_refresh=false
    now=$(date +%s)

    if [ -f "${_OSS_CRED_FILE}" ]; then
        . "${_OSS_CRED_FILE}"
        if [ -z "${_OSS_CRED_EXPIRES_AT:-}" ] || [ $(( _OSS_CRED_EXPIRES_AT - now )) -lt ${_OSS_CRED_REFRESH_MARGIN} ]; then
            needs_refresh=true
        fi
    else
        needs_refresh=true
    fi

    if [ "${needs_refresh}" = true ]; then
        ${refresh_fn} || return 1
        . "${_OSS_CRED_FILE}"
    fi

    export MC_HOST_hiclaw
}
