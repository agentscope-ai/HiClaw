#!/bin/bash
# copaw-agentloop-fs.sh - Shared helpers for optional CoPaw AgentLoop FS memory integration

_copaw_agentloop_fs_bool() {
    local value
    value="$(echo "${1:-false}" | tr '[:upper:]' '[:lower:]')"
    [ "${value}" = "1" ] || [ "${value}" = "true" ] || [ "${value}" = "yes" ]
}

copaw_agentloop_fs_enabled() {
    _copaw_agentloop_fs_bool "${HICLAW_COPAW_AGENTLOOP_FS_MEMORY_ENABLED:-false}" || return 1
    [ -n "${HICLAW_COPAW_AGENTLOOP_FS_STORE:-}" ] || return 1
}

copaw_agentloop_fs_mount_path() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_MOUNT:-/tmp/alibabacloud}"
}

copaw_agentloop_fs_store() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_STORE:-}"
}

copaw_agentloop_fs_access_key_id() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_ACCESS_KEY_ID:-}"
}

copaw_agentloop_fs_access_key_secret() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_ACCESS_KEY_SECRET:-}"
}

copaw_agentloop_fs_security_token() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_SECURITY_TOKEN:-}"
}

copaw_agentloop_fs_cms_workspace() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_CMS_WORKSPACE:-${HICLAW_CMS_WORKSPACE:-}}"
}

copaw_agentloop_fs_cms_endpoint() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_CMS_ENDPOINT:-${CMS_ENDPOINT:-cms.cn-hangzhou.aliyuncs.com}}"
}

copaw_agentloop_fs_role_arn() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_ROLE_ARN:-}"
}

copaw_agentloop_fs_oidc_provider_arn() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_OIDC_PROVIDER_ARN:-}"
}

copaw_agentloop_fs_oidc_token_file() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_OIDC_TOKEN_FILE:-}"
}

copaw_agentloop_fs_role_session_name() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_ROLE_SESSION_NAME:-}"
}

copaw_agentloop_fs_region_id() {
    printf '%s\n' "${HICLAW_COPAW_AGENTLOOP_FS_REGION_ID:-${HICLAW_REGION:-cn-hangzhou}}"
}

copaw_agentloop_fs_disable_memory_manager() {
    _copaw_agentloop_fs_bool "${HICLAW_COPAW_AGENTLOOP_FS_DISABLE_MEMORY_MANAGER:-false}"
}

copaw_agentloop_fs_static_credentials_configured() {
    [ -n "$(copaw_agentloop_fs_access_key_id)" ] || return 1
    [ -n "$(copaw_agentloop_fs_access_key_secret)" ] || return 1
    [ -n "$(copaw_agentloop_fs_cms_workspace)" ] || return 1
}

copaw_agentloop_fs_oidc_overrides_configured() {
    [ -n "$(copaw_agentloop_fs_role_arn)" ] || return 1
    [ -n "$(copaw_agentloop_fs_oidc_provider_arn)" ] || return 1
    [ -n "$(copaw_agentloop_fs_oidc_token_file)" ] || return 1
    [ -n "$(copaw_agentloop_fs_cms_workspace)" ] || return 1
}

copaw_agentloop_fs_signature() {
    local enabled store mount disable_mm
    if copaw_agentloop_fs_enabled; then
        enabled="true"
    else
        enabled="false"
    fi
    store="$(copaw_agentloop_fs_store)"
    mount="$(copaw_agentloop_fs_mount_path)"
    if copaw_agentloop_fs_disable_memory_manager; then
        disable_mm="true"
    else
        disable_mm="false"
    fi
    printf 'enabled=%s;store=%s;mount=%s;disable_memory_manager=%s\n' \
        "${enabled}" "${store}" "${mount}" "${disable_mm}"
}

copaw_agentloop_fs_env_lines() {
    copaw_agentloop_fs_enabled || return 0

    printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_MEMORY_ENABLED=true"
    printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_MOUNT=$(copaw_agentloop_fs_mount_path)"
    printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_STORE=$(copaw_agentloop_fs_store)"
    printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_CMS_ENDPOINT=$(copaw_agentloop_fs_cms_endpoint)"
    printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_REGION_ID=$(copaw_agentloop_fs_region_id)"

    local value

    value="$(copaw_agentloop_fs_access_key_id)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_ACCESS_KEY_ID=${value}"

    value="$(copaw_agentloop_fs_access_key_secret)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_ACCESS_KEY_SECRET=${value}"

    value="$(copaw_agentloop_fs_security_token)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_SECURITY_TOKEN=${value}"

    value="$(copaw_agentloop_fs_cms_workspace)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_CMS_WORKSPACE=${value}"

    value="$(copaw_agentloop_fs_role_arn)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_ROLE_ARN=${value}"

    value="$(copaw_agentloop_fs_oidc_provider_arn)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_OIDC_PROVIDER_ARN=${value}"

    value="$(copaw_agentloop_fs_oidc_token_file)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_OIDC_TOKEN_FILE=${value}"

    value="$(copaw_agentloop_fs_role_session_name)"
    [ -n "${value}" ] && printf '%s\n' "HICLAW_COPAW_AGENTLOOP_FS_ROLE_SESSION_NAME=${value}"

    if copaw_agentloop_fs_disable_memory_manager; then
        printf '%s\n' "ENABLE_MEMORY_MANAGER=false"
    fi
}

copaw_agentloop_fs_render_skill_dir() {
    local target_dir="$1"
    local template="/opt/hiclaw/agent/copaw-worker-agent/skills/alibabacloud-agent-fs/SKILL.md.tmpl"
    mkdir -p "${target_dir}" || return 1
    HICLAW_COPAW_AGENTLOOP_FS_MOUNT_RENDERED="$(copaw_agentloop_fs_mount_path)"
    HICLAW_COPAW_AGENTLOOP_FS_STORE_RENDERED="$(copaw_agentloop_fs_store)"
    export HICLAW_COPAW_AGENTLOOP_FS_MOUNT_RENDERED HICLAW_COPAW_AGENTLOOP_FS_STORE_RENDERED
    envsubst < "${template}" > "${target_dir}/SKILL.md"
}

copaw_agentloop_fs_render_agents_section() {
    local target_file="$1"
    local template="/opt/hiclaw/agent/copaw-worker-agent/references/agentloop-fs-memory-section.md.tmpl"
    HICLAW_COPAW_AGENTLOOP_FS_MOUNT_RENDERED="$(copaw_agentloop_fs_mount_path)"
    HICLAW_COPAW_AGENTLOOP_FS_STORE_RENDERED="$(copaw_agentloop_fs_store)"
    export HICLAW_COPAW_AGENTLOOP_FS_MOUNT_RENDERED HICLAW_COPAW_AGENTLOOP_FS_STORE_RENDERED
    envsubst < "${template}" > "${target_file}"
}
