#!/bin/bash
# oss-credentials.sh - STS credential management for mc (MinIO Client)
#
# Two credential paths (checked in priority order):
#
# 1. RRSA OIDC (Manager, Controller — any SAE app with oidc_role_name):
#    ALIBABA_CLOUD_OIDC_TOKEN_FILE exists → call STS AssumeRoleWithOIDC directly.
#    Worker inline policy applied when HICLAW_WORKER_NAME is set.
#
# 2. Controller-mediated STS (Workers without RRSA):
#    HICLAW_CONTROLLER_URL + HICLAW_WORKER_API_KEY → call controller /credentials/sts.
#
# 3. Neither → no-op (local mode, mc alias configured with static credentials).
#
# STS tokens expire after 1 hour. Credentials are cached and lazy-refreshed.
#
# Usage:
#   source /opt/hiclaw/scripts/lib/oss-credentials.sh
#   ensure_mc_credentials   # call before any mc command

_OSS_CRED_FILE="/tmp/mc-oss-credentials.env"
_OSS_CRED_REFRESH_MARGIN=600  # refresh if less than 10 minutes remaining

# --------------------------------------------------------------------------
# Path 1: Direct STS via RRSA OIDC
# --------------------------------------------------------------------------

# Build an inline STS policy restricting OSS access to the worker's own prefix.
# Only used when HICLAW_WORKER_NAME is set (worker context).
_oss_build_worker_policy() {
    local worker="$1"
    local bucket="${HICLAW_FS_BUCKET:-hiclaw-cloud-storage}"
    cat <<POLICY
{"Version":"1","Statement":[{"Effect":"Allow","Action":["oss:ListObjects"],"Resource":["acs:oss:*:*:${bucket}"],"Condition":{"StringLike":{"oss:Prefix":["agents/${worker}/*","shared/*"]}}},{"Effect":"Allow","Action":["oss:GetObject","oss:PutObject","oss:DeleteObject"],"Resource":["acs:oss:*:*:${bucket}/agents/${worker}/*","acs:oss:*:*:${bucket}/shared/*"]}]}
POLICY
}

_oss_refresh_sts_direct() {
    local oidc_token region sts_resp http_code
    local sts_ak sts_sk sts_token expires_at

    oidc_token=$(cat "${ALIBABA_CLOUD_OIDC_TOKEN_FILE}")
    region="${HICLAW_REGION:-cn-hangzhou}"

    local timestamp nonce
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    nonce=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')

    # Build inline policy arg for workers (restricts STS token to own prefix)
    local policy_args=()
    if [ -n "${HICLAW_WORKER_NAME:-}" ]; then
        local policy
        policy=$(_oss_build_worker_policy "${HICLAW_WORKER_NAME}")
        policy_args=(--data-urlencode "Policy=${policy}")
        echo "[oss-credentials] Applying worker inline policy for '${HICLAW_WORKER_NAME}'" >&2
    fi

    sts_resp=$(curl -s -w "\n%{http_code}" -X POST "https://sts-vpc.${region}.aliyuncs.com" \
        -d "Action=AssumeRoleWithOIDC" \
        -d "Format=JSON" \
        -d "Version=2015-04-01" \
        --data-urlencode "Timestamp=${timestamp}" \
        -d "SignatureNonce=${nonce}" \
        --data-urlencode "RoleArn=${ALIBABA_CLOUD_ROLE_ARN}" \
        --data-urlencode "OIDCProviderArn=${ALIBABA_CLOUD_OIDC_PROVIDER_ARN}" \
        --data-urlencode "OIDCToken=${oidc_token}" \
        -d "RoleSessionName=hiclaw-oss-session" \
        -d "DurationSeconds=3600" \
        "${policy_args[@]}" \
        --connect-timeout 10 --max-time 30 2>&1)

    http_code=$(echo "${sts_resp}" | tail -1)
    sts_resp=$(echo "${sts_resp}" | sed '$d')

    if [ "${http_code}" != "200" ]; then
        echo "[oss-credentials] ERROR: STS request failed (HTTP ${http_code})" >&2
        echo "[oss-credentials] Response: ${sts_resp}" >&2
        return 1
    fi

    sts_ak=$(echo "${sts_resp}" | jq -r '.Credentials.AccessKeyId')
    sts_sk=$(echo "${sts_resp}" | jq -r '.Credentials.AccessKeySecret')
    sts_token=$(echo "${sts_resp}" | jq -r '.Credentials.SecurityToken')

    if [ -z "${sts_ak}" ] || [ "${sts_ak}" = "null" ]; then
        echo "[oss-credentials] ERROR: Failed to parse STS credentials" >&2
        echo "[oss-credentials] Response: ${sts_resp}" >&2
        return 1
    fi

    expires_at=$(( $(date +%s) + 3600 ))

    cat > "${_OSS_CRED_FILE}" <<EOF
MC_HOST_hiclaw="https://${sts_ak}:${sts_sk}:${sts_token}@oss-${region}-internal.aliyuncs.com"
_OSS_CRED_EXPIRES_AT=${expires_at}
EOF
    chmod 600 "${_OSS_CRED_FILE}"

    echo "[oss-credentials] STS credentials refreshed via RRSA (AK prefix: ${sts_ak:0:8}..., expires: $(date -d @${expires_at} '+%H:%M:%S' 2>/dev/null || date -r ${expires_at} '+%H:%M:%S' 2>/dev/null || echo ${expires_at}))" >&2
}

# --------------------------------------------------------------------------
# Path 2: STS via Controller (workers without RRSA)
# --------------------------------------------------------------------------

_oss_refresh_sts_via_controller() {
    local _controller_url="${HICLAW_CONTROLLER_URL:-}"
    local resp http_code
    local sts_ak sts_sk sts_token oss_endpoint oss_bucket

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
    # Priority 1: RRSA OIDC token file exists → direct STS call
    if [ -n "${ALIBABA_CLOUD_OIDC_TOKEN_FILE:-}" ] && [ -f "${ALIBABA_CLOUD_OIDC_TOKEN_FILE}" ]; then
        _oss_ensure_refresh _oss_refresh_sts_direct
        return $?
    fi

    # Priority 2: Controller URL + worker API key → controller-mediated STS
    if [ -n "${HICLAW_CONTROLLER_URL:-}" ] && [ -n "${HICLAW_WORKER_API_KEY:-}" ]; then
        _oss_ensure_refresh _oss_refresh_sts_via_controller
        return $?
    fi

    # Priority 3: local mode — mc alias configured with static credentials
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
