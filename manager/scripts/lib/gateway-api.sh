#!/bin/bash
# gateway-api.sh - Unified gateway consumer/route/MCP authorization abstraction
#
# Dispatches to Higress Console REST API (local) or AI Gateway API (cloud).
# Follows the same pattern as worker_backend_* in container-api.sh.
#
# Provides:
#   gateway_ensure_session()                  — ensure Higress cookie (local) / no-op (cloud)
#   gateway_create_consumer(name, key)        — create consumer, returns JSON {status, api_key, consumer_id}
#   gateway_authorize_routes(consumer_name)   — authorize all AI routes
#   gateway_authorize_mcp(consumer_name, csv) — authorize MCP servers
#
# Prerequisites:
#   - source hiclaw-env.sh (for HICLAW_RUNTIME)
#   - HICLAW_ADMIN_USER, HICLAW_ADMIN_PASSWORD (for Higress login)
#   - HIGRESS_COOKIE_FILE (set by start-manager-agent.sh or gateway_ensure_session)
#
# Usage:
#   source /opt/hiclaw/scripts/lib/gateway-api.sh

# ── Load cloud providers (additive) ──────────────────────────────────────────
for _gw_provider_file in /opt/hiclaw/scripts/lib/cloud/*.sh; do
    [ -f "${_gw_provider_file}" ] && source "${_gw_provider_file}"
done
unset _gw_provider_file

# ── Backend detection ─────────────────────────────────────────────────────────

_detect_gateway_backend() {
    if [ "${HICLAW_RUNTIME:-}" = "aliyun" ]; then
        echo "aliyun"
    else
        echo "higress"
    fi
}

# ── Session management ────────────────────────────────────────────────────────

# Ensure a valid Higress Console session cookie exists.
# In cloud mode this is a no-op (no local Higress).
# Sets HIGRESS_COOKIE_FILE as a side effect.
gateway_ensure_session() {
    local backend
    backend=$(_detect_gateway_backend)
    [ "${backend}" != "higress" ] && return 0

    # Already have a valid cookie
    if [ -n "${HIGRESS_COOKIE_FILE:-}" ] && [ -s "${HIGRESS_COOKIE_FILE:-}" ]; then
        return 0
    fi

    HIGRESS_COOKIE_FILE="/tmp/higress-session-cookie-gateway"
    local admin_user="${HICLAW_ADMIN_USER:-admin}"
    local admin_password="${HICLAW_ADMIN_PASSWORD:-admin}"

    curl -sf -o /dev/null -X POST http://127.0.0.1:8001/session/login \
        -H 'Content-Type: application/json' \
        -c "${HIGRESS_COOKIE_FILE}" \
        -d '{"username":"'"${admin_user}"'","password":"'"${admin_password}"'"}' 2>/dev/null \
        || { echo "[gateway-api] ERROR: Failed to login to Higress Console" >&2; return 1; }

    export HIGRESS_COOKIE_FILE
}

# ── Consumer creation ─────────────────────────────────────────────────────────

# gateway_create_consumer <consumer_name> <credential_key>
# Returns JSON: {"status": "created"|"exists", "api_key": "...", "consumer_id": "..."}
gateway_create_consumer() {
    local consumer_name="$1"
    local credential_key="$2"
    local backend
    backend=$(_detect_gateway_backend)

    case "${backend}" in
        aliyun)
            _gateway_cloud_create_consumer "${consumer_name}" "${credential_key}"
            ;;
        higress)
            _gateway_higress_create_consumer "${consumer_name}" "${credential_key}"
            ;;
    esac
}

_gateway_cloud_create_consumer() {
    local consumer_name="$1"
    local credential_key="$2"

    local resp
    resp=$(cloud_create_consumer "${consumer_name}" 2>/dev/null) || true
    local status
    status=$(echo "${resp}" | jq -r '.status // "error"' 2>/dev/null)

    if [ "${status}" = "created" ] || [ "${status}" = "exists" ]; then
        local api_key consumer_id
        api_key=$(echo "${resp}" | jq -r '.api_key // empty' 2>/dev/null)
        consumer_id=$(echo "${resp}" | jq -r '.consumer_id // empty' 2>/dev/null)
        jq -cn --arg s "${status}" \
               --arg k "${api_key:-${credential_key}}" \
               --arg id "${consumer_id}" \
            '{status: $s, api_key: $k, consumer_id: $id}'
    else
        echo "[gateway-api] ERROR: Cloud consumer creation failed: ${resp}" >&2
        return 1
    fi
}

_gateway_higress_create_consumer() {
    local consumer_name="$1"
    local credential_key="$2"

    curl -sf -X POST http://127.0.0.1:8001/v1/consumers \
        -b "${HIGRESS_COOKIE_FILE}" \
        -H 'Content-Type: application/json' \
        -d '{
            "name": "'"${consumer_name}"'",
            "credentials": [{
                "type": "key-auth",
                "source": "BEARER",
                "values": ["'"${credential_key}"'"]
            }]
        }' > /dev/null 2>&1 \
        || { echo "[gateway-api] ERROR: Failed to create Higress consumer ${consumer_name}" >&2; return 1; }

    jq -cn --arg s "created" --arg k "${credential_key}" \
        '{status: $s, api_key: $k, consumer_id: ""}'
}

# ── Route authorization ───────────────────────────────────────────────────────

# gateway_authorize_routes <consumer_name>
# Cloud: binds consumer to model API via cloud_bind_consumer (if env vars set)
# Local: iterates all AI routes and adds consumer to allowedConsumers
gateway_authorize_routes() {
    local consumer_name="$1"
    local backend
    backend=$(_detect_gateway_backend)

    case "${backend}" in
        aliyun)
            _gateway_cloud_authorize_routes "${consumer_name}"
            ;;
        higress)
            _gateway_higress_authorize_routes "${consumer_name}"
            ;;
    esac
}

_gateway_cloud_authorize_routes() {
    local consumer_name="$1"

    # consumer_id is passed via GATEWAY_CONSUMER_ID (set by caller after gateway_create_consumer)
    local consumer_id="${GATEWAY_CONSUMER_ID:-}"
    if [ -n "${consumer_id}" ] && [ -n "${HICLAW_GW_MODEL_API_ID:-}" ] && [ -n "${HICLAW_GW_ENV_ID:-}" ]; then
        local bind_result
        bind_result=$(cloud_bind_consumer "${consumer_id}" "${HICLAW_GW_MODEL_API_ID}" "${HICLAW_GW_ENV_ID}" 2>/dev/null) || true
    else
        local skip_reason=""
        [ -z "${consumer_id}" ] && skip_reason="consumer_id empty"
        [ -z "${HICLAW_GW_MODEL_API_ID:-}" ] && skip_reason="${skip_reason:+${skip_reason}, }HICLAW_GW_MODEL_API_ID not set"
        [ -z "${HICLAW_GW_ENV_ID:-}" ] && skip_reason="${skip_reason:+${skip_reason}, }HICLAW_GW_ENV_ID not set"
        echo "[gateway-api] Skipping cloud route binding (${skip_reason})" >&2
    fi
}

_gateway_higress_authorize_routes() {
    local consumer_name="$1"

    local ai_routes
    ai_routes=$(curl -sf http://127.0.0.1:8001/v1/ai/routes \
        -b "${HIGRESS_COOKIE_FILE}" 2>/dev/null) \
        || { echo "[gateway-api] ERROR: Failed to list AI routes" >&2; return 1; }

    local route_names
    route_names=$(echo "${ai_routes}" | jq -r '.data[]?.name // empty' 2>/dev/null || true)
    for route_name in ${route_names}; do
        [ -z "${route_name}" ] && continue
        local route_resp route
        route_resp=$(curl -sf "http://127.0.0.1:8001/v1/ai/routes/${route_name}" \
            -b "${HIGRESS_COOKIE_FILE}" 2>/dev/null) || continue
        route=$(echo "${route_resp}" | jq '.data // .' 2>/dev/null)

        local already
        already=$(echo "${route}" | jq -r '.authConfig.allowedConsumers[]? // empty' 2>/dev/null | grep -c "^${consumer_name}$" || true)
        if [ "${already}" -gt 0 ]; then
            continue
        fi

        local updated
        updated=$(echo "${route}" | jq --arg c "${consumer_name}" '.authConfig.allowedConsumers += [$c]')
        curl -sf -X PUT "http://127.0.0.1:8001/v1/ai/routes/${route_name}" \
            -b "${HIGRESS_COOKIE_FILE}" \
            -H 'Content-Type: application/json' \
            -d "${updated}" > /dev/null 2>&1 \
            || echo "[gateway-api] WARNING: Failed to update route ${route_name}" >&2
    done
}

# ── MCP server authorization ─────────────────────────────────────────────────

# gateway_authorize_mcp <consumer_name> <mcp_servers_csv>
# Cloud: no-op (MCP servers managed via AI Gateway console)
# Local: iterates MCP servers and adds consumer to allowedConsumers
# Sets TARGET_MCP_LIST as a side effect (resolved list of MCP server names)
gateway_authorize_mcp() {
    local consumer_name="$1"
    local mcp_servers_csv="${2:-}"
    local backend
    backend=$(_detect_gateway_backend)

    case "${backend}" in
        aliyun)
            # Cloud: MCP authorization is managed via AI Gateway console
            TARGET_MCP_LIST="${mcp_servers_csv}"
            ;;
        higress)
            _gateway_higress_authorize_mcp "${consumer_name}" "${mcp_servers_csv}"
            ;;
    esac
}

_gateway_higress_authorize_mcp() {
    local consumer_name="$1"
    local mcp_servers_csv="${2:-}"

    local all_mcp_raw all_mcp
    all_mcp_raw=$(curl -sf http://127.0.0.1:8001/v1/mcpServer \
        -b "${HIGRESS_COOKIE_FILE}" 2>/dev/null) || true
    all_mcp=$(echo "${all_mcp_raw}" | jq '.data // .' 2>/dev/null || echo "${all_mcp_raw}")

    # Resolve target list: use provided CSV or default to all existing MCP servers
    if [ -n "${mcp_servers_csv}" ]; then
        TARGET_MCP_LIST="${mcp_servers_csv}"
    else
        TARGET_MCP_LIST=$(echo "${all_mcp}" | jq -r '.[].name // empty' 2>/dev/null | tr '\n' ',' || true)
        TARGET_MCP_LIST="${TARGET_MCP_LIST%,}"
    fi

    if [ -z "${TARGET_MCP_LIST}" ]; then
        return 0
    fi

    local mcp_arr mcp_name
    IFS=',' read -ra mcp_arr <<< "${TARGET_MCP_LIST}"
    for mcp_name in "${mcp_arr[@]}"; do
        mcp_name=$(echo "${mcp_name}" | tr -d ' ')
        [ -z "${mcp_name}" ] && continue

        local existing_consumers consumer_list ec
        existing_consumers=$(echo "${all_mcp}" | jq -r --arg n "${mcp_name}" \
            '.[] | select(.name == $n) | .consumerAuthInfo.allowedConsumers // [] | .[]' 2>/dev/null || true)
        consumer_list="[\"manager\""
        for ec in ${existing_consumers}; do
            [ "${ec}" = "manager" ] && continue
            [ "${ec}" = "${consumer_name}" ] && continue
            consumer_list="${consumer_list},\"${ec}\""
        done
        consumer_list="${consumer_list},\"${consumer_name}\"]"

        curl -sf -X PUT http://127.0.0.1:8001/v1/mcpServer/consumers \
            -b "${HIGRESS_COOKIE_FILE}" \
            -H 'Content-Type: application/json' \
            -d '{"mcpServerName":"'"${mcp_name}"'","consumers":'"${consumer_list}"'}' > /dev/null 2>&1 \
            || echo "[gateway-api] WARNING: Failed to authorize MCP server ${mcp_name}" >&2
    done
}
