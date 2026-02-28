#!/bin/bash
# hiclaw-install.sh - One-click installation for HiClaw Manager and Worker
#
# Usage:
#   ./hiclaw-install.sh                  # Interactive installation (choose Quick Start or Manual)
#   ./hiclaw-install.sh manager          # Same as above (explicit)
#   ./hiclaw-install.sh worker --name <name> ...  # Worker installation
#
# Onboarding Modes:
#   Quick Start  - Fast installation with all default values (recommended)
#   Manual       - Customize each option step by step
#
# Environment variables (for automation):
#   HICLAW_NON_INTERACTIVE    Skip all prompts, use defaults  (default: 0)
#   HICLAW_LLM_PROVIDER      LLM provider       (default: alibaba-cloud)
#   HICLAW_DEFAULT_MODEL      Default model       (default: qwen3.5-plus)
#   HICLAW_LLM_API_KEY        LLM API key         (required)
#   HICLAW_ADMIN_USER         Admin username       (default: admin)
#   HICLAW_ADMIN_PASSWORD     Admin password       (auto-generated if not set, min 8 chars)
#   HICLAW_MATRIX_DOMAIN      Matrix domain        (default: matrix-local.hiclaw.io:18080)
#   HICLAW_MOUNT_SOCKET       Mount container runtime socket (default: 1)
#   HICLAW_DATA_DIR           Host directory for persistent data (default: docker volume)
#   HICLAW_WORKSPACE_DIR      Host directory for manager workspace (default: ~/hiclaw-manager)
#   HICLAW_VERSION            Image tag            (default: latest)
#   HICLAW_REGISTRY           Image registry       (default: auto-detected by timezone)
#   HICLAW_INSTALL_MANAGER_IMAGE  Override manager image (e.g., local build)
#   HICLAW_INSTALL_WORKER_IMAGE   Override worker image  (e.g., local build)
#   HICLAW_PORT_GATEWAY       Host port for Higress gateway (default: 18080)
#   HICLAW_PORT_CONSOLE       Host port for Higress console (default: 18001)

set -e

HICLAW_VERSION="${HICLAW_VERSION:-latest}"
HICLAW_NON_INTERACTIVE="${HICLAW_NON_INTERACTIVE:-0}"
HICLAW_MOUNT_SOCKET="${HICLAW_MOUNT_SOCKET:-1}"

# ============================================================
# Utility functions (needed early for timezone detection)
# ============================================================

log() {
    echo -e "\033[36m[HiClaw]\033[0m $1"
}

error() {
    echo -e "\033[31m[HiClaw ERROR]\033[0m $1" >&2
    exit 1
}

# ============================================================
# Timezone detection (compatible with Linux and macOS)
# ============================================================

detect_timezone() {
    local tz=""
    
    # Try /etc/timezone (Debian/Ubuntu)
    if [ -f /etc/timezone ]; then
        tz=$(cat /etc/timezone 2>/dev/null | tr -d '[:space:]')
    fi
    
    # Try /etc/localtime symlink (macOS and some Linux)
    if [ -z "${tz}" ] && [ -L /etc/localtime ]; then
        tz=$(ls -l /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||')
    fi
    
    # Try timedatectl (systemd)
    if [ -z "${tz}" ]; then
        tz=$(timedatectl show --value -p Timezone 2>/dev/null)
    fi
    
    # If still not detected, warn and prompt user
    if [ -z "${tz}" ]; then
        echo ""
        echo -e "\033[33m[HiClaw WARNING]\033[0m Could not detect timezone automatically."
        echo -e "\033[33m[HiClaw]\033[0m Please enter your timezone (e.g., Asia/Shanghai, America/New_York)."
        echo ""
        if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
            tz="Asia/Shanghai"
            log "Using default timezone: ${tz}"
        else
            read -p "Timezone [Asia/Shanghai]: " tz
            tz="${tz:-Asia/Shanghai}"
        fi
    fi
    
    echo "${tz}"
}

# Detect timezone once at startup (used by registry selection and container TZ)
HICLAW_TIMEZONE="${HICLAW_TIMEZONE:-$(detect_timezone)}"

# ============================================================
# Registry selection based on timezone
# ============================================================

detect_registry() {
    local tz="${HICLAW_TIMEZONE}"

    case "${tz}" in
        America/*)
            echo "higress-registry.us-west-1.cr.aliyuncs.com"
            ;;
        Asia/Singapore|Asia/Bangkok|Asia/Jakarta|Asia/Makassar|Asia/Jayapura|\
        Asia/Kuala_Lumpur|Asia/Ho_Chi_Minh|Asia/Manila|Asia/Yangon|\
        Asia/Vientiane|Asia/Phnom_Penh|Asia/Pontianak|Asia/Ujung_Pandang)
            echo "higress-registry.ap-southeast-7.cr.aliyuncs.com"
            ;;
        *)
            echo "higress-registry.cn-hangzhou.cr.aliyuncs.com"
            ;;
    esac
}

HICLAW_REGISTRY="${HICLAW_REGISTRY:-$(detect_registry)}"
MANAGER_IMAGE="${HICLAW_INSTALL_MANAGER_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-manager:${HICLAW_VERSION}}"
WORKER_IMAGE="${HICLAW_INSTALL_WORKER_IMAGE:-${HICLAW_REGISTRY}/higress/hiclaw-worker:${HICLAW_VERSION}}"

# ============================================================
# Wait for Manager agent to be ready
# Uses `openclaw gateway health` inside the container to confirm the gateway is running
# ============================================================

wait_manager_ready() {
    local timeout="${HICLAW_READY_TIMEOUT:-300}"
    local elapsed=0
    local container="${1:-hiclaw-manager}"
    
    log "Waiting for Manager agent to be ready (timeout: ${timeout}s)..."
    
    # Wait for OpenClaw gateway to be healthy inside the container
    while [ "${elapsed}" -lt "${timeout}" ]; do
        if docker exec "${container}" openclaw gateway health --json 2>/dev/null | grep -q '"ok"' 2>/dev/null; then
            log "Manager agent is ready!"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
        printf "\r\033[36m[HiClaw]\033[0m Waiting... (%ds/%ds)" "${elapsed}" "${timeout}"
    done
    
    echo ""
    error "Manager agent did not become ready within ${timeout}s. Check: docker logs ${container}"
}

# ============================================================
# Create OpenAI-compatible provider via Higress Console API
# ============================================================

create_openai_compat_provider() {
    local base_url="${HICLAW_OPENAI_BASE_URL}"
    local api_key="${HICLAW_LLM_API_KEY}"
    local console_port="${HICLAW_PORT_CONSOLE:-18001}"
    local console_url="http://localhost:${console_port}"
    
    if [ -z "${base_url}" ] || [ -z "${api_key}" ]; then
        log "WARNING: OpenAI Base URL or API Key not set, skipping provider creation"
        return 1
    fi
    
    # Parse base URL to extract domain and port
    local domain=""
    local port="443"
    local protocol="https"
    
    # Remove protocol prefix
    local url_without_proto="${base_url#https://}"
    url_without_proto="${url_without_proto#http://}"
    
    # Detect protocol
    if [[ "${base_url}" == http://* ]]; then
        protocol="http"
        port="80"
    fi
    
    # Extract domain (first part before /)
    domain="${url_without_proto%%/*}"
    
    # Check for explicit port in domain
    if [[ "${domain}" == *:* ]]; then
        port="${domain##*:}"
        domain="${domain%:*}"
    fi
    
    log "Creating OpenAI-compatible provider..."
    log "  Domain: ${domain}"
    log "  Port: ${port}"
    log "  Protocol: ${protocol}"
    
    # Create DNS service source
    local service_name="openai-compat"
    local create_service_resp
    create_service_resp=$(curl -sf -X POST "${console_url}/v1/service-sources" \
        -H 'Accept: application/json' \
        -H 'Content-Type: application/json' \
        -d "{\"type\":\"dns\",\"name\":\"${service_name}\",\"port\":\"${port}\",\"protocol\":\"${protocol}\",\"proxyName\":\"\",\"domain\":\"${domain}\"}" 2>/dev/null) || {
        log "WARNING: Failed to create DNS service source (may already exist)"
    }
    
    # Wait a moment for service to be created
    sleep 2
    
    # Create AI provider
    local provider_name="openai-compat"
    local create_provider_resp
    create_provider_resp=$(curl -sf -X POST "${console_url}/v1/ai/providers" \
        -H 'Accept: application/json' \
        -H 'Content-Type: application/json' \
        -d "{\"type\":\"openai\",\"name\":\"${provider_name}\",\"tokens\":[\"${api_key}\"],\"version\":0,\"protocol\":\"openai/v1\",\"tokenFailoverConfig\":{\"enabled\":false},\"rawConfigs\":{\"openaiCustomUrl\":\"${base_url}\",\"openaiCustomServiceName\":\"${service_name}.dns\",\"openaiCustomServicePort\":${port}}}" 2>/dev/null) || {
        log "WARNING: Failed to create AI provider (may already exist)"
        return 1
    }
    
    log "OpenAI-compatible provider created successfully"
    return 0
}

# ============================================================
# Send welcome message to Manager
# ============================================================

send_welcome_message() {
    local container="hiclaw-manager"
    local admin_user="${HICLAW_ADMIN_USER:-admin}"
    local admin_password="${HICLAW_ADMIN_PASSWORD}"
    local matrix_domain="${HICLAW_MATRIX_DOMAIN}"
    local matrix_url="http://127.0.0.1:6167"
    local manager_user="manager"
    local manager_full_id="@${manager_user}:${matrix_domain}"
    local timezone="${HICLAW_TIMEZONE}"

    # Helper: run curl inside the manager container to reach Matrix directly
    mcurl() { docker exec "${container}" curl "$@"; }

    # Login to get admin access token
    log "Logging in as ${admin_user} to send welcome message..."
    local login_resp
    login_resp=$(mcurl -sf -X POST "${matrix_url}/_matrix/client/v3/login" \
        -H 'Content-Type: application/json' \
        -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"${admin_user}\"},\"password\":\"${admin_password}\"}" 2>/dev/null)

    local access_token
    access_token=$(echo "${login_resp}" | jq -r '.access_token // empty')
    if [ -z "${access_token}" ]; then
        log "WARNING: Failed to login as ${admin_user}, skipping welcome message"
        return 1
    fi

    # Find or create DM room with manager
    log "Finding DM room with Manager..."
    local rooms
    rooms=$(mcurl -sf "${matrix_url}/_matrix/client/v3/joined_rooms" \
        -H "Authorization: Bearer ${access_token}" 2>/dev/null | jq -r '.joined_rooms[]' 2>/dev/null) || true

    local room_id=""
    for rid in ${rooms}; do
        local members
        members=$(mcurl -sf "${matrix_url}/_matrix/client/v3/rooms/${rid}/members" \
            -H "Authorization: Bearer ${access_token}" 2>/dev/null | jq -r '.chunk[].state_key' 2>/dev/null) || continue
        local member_count
        member_count=$(echo "${members}" | wc -l | xargs)
        if [ "${member_count}" = "2" ] && echo "${members}" | grep -q "@${manager_user}:"; then
            room_id="${rid}"
            break
        fi
    done

    if [ -z "${room_id}" ]; then
        log "Creating DM room with Manager..."
        local create_resp
        create_resp=$(mcurl -sf -X POST "${matrix_url}/_matrix/client/v3/createRoom" \
            -H "Authorization: Bearer ${access_token}" \
            -H 'Content-Type: application/json' \
            -d "{\"is_direct\":true,\"invite\":[\"${manager_full_id}\"],\"preset\":\"trusted_private_chat\"}" 2>/dev/null)
        room_id=$(echo "${create_resp}" | jq -r '.room_id // empty')
    fi

    if [ -z "${room_id}" ]; then
        log "WARNING: Could not find or create DM room with Manager"
        return 1
    fi

    # Wait for Manager to join the room
    log "Waiting for Manager to join the room..."
    local wait_elapsed=0
    local wait_timeout=60
    while [ "${wait_elapsed}" -lt "${wait_timeout}" ]; do
        local members
        members=$(mcurl -sf "${matrix_url}/_matrix/client/v3/rooms/${room_id}/members" \
            -H "Authorization: Bearer ${access_token}" 2>/dev/null | jq -r '.chunk[].state_key' 2>/dev/null) || true
        if echo "${members}" | grep -q "${manager_full_id}"; then
            break
        fi
        sleep 2
        wait_elapsed=$((wait_elapsed + 2))
    done

    # Send welcome message
    log "Sending welcome message to Manager..."
    local welcome_msg
    welcome_msg="Hello Manager! This is an automated message from the HiClaw installation script.

You have just completed the installation and initialization. As the Manager agent, please:

1. Output a warm welcome message introducing your capabilities to the human admin
2. Based on the current timezone (${timezone}), identify the likely country/region of the admin
3. Ask the admin in their likely local language (if detectable, otherwise use English) how you can help them
4. Remember the admin's preferred language for all future interactions with them, with workers, and for instructions you give to workers in project rooms

The human admin will start chatting with you shortly. Please wait for their response before proceeding with any tasks."

    local txn_id="welcome-$(date +%s%N)"
    local payload
    payload=$(jq -nc --arg body "${welcome_msg}" '{"msgtype":"m.text","body":$body}')
    mcurl -sf -X PUT "${matrix_url}/_matrix/client/v3/rooms/${room_id}/send/m.room.message/${txn_id}" \
        -H "Authorization: Bearer ${access_token}" \
        -H 'Content-Type: application/json' \
        -d "${payload}" > /dev/null 2>&1 || {
        log "WARNING: Failed to send welcome message"
        return 1
    }

    log "Welcome message sent to Manager"
    return 0
}

# Prompt for a value interactively, but skip if env var is already set.
# In non-interactive mode, uses default or errors if required and no default.
# Usage: prompt VAR_NAME "Prompt text" "default" [true=secret]
prompt() {
    local var_name="$1"
    local prompt_text="$2"
    local default_value="$3"
    local is_secret="${4:-false}"

    # If the variable is already set in the environment, use it silently
    local current_value="${!var_name}"
    if [ -n "${current_value}" ]; then
        log "  ${var_name} = (pre-set via env)"
        return
    fi

    # Non-interactive: use default or error
    if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
        if [ -n "${default_value}" ]; then
            eval "export ${var_name}='${default_value}'"
            log "  ${var_name} = ${default_value} (default)"
            return
        else
            error "${var_name} is required (set via environment variable in non-interactive mode)"
        fi
    fi

    if [ -n "${default_value}" ]; then
        prompt_text="${prompt_text} [${default_value}]"
    fi

    if [ "${is_secret}" = "true" ]; then
        read -s -p "${prompt_text}: " value
        echo
    else
        read -p "${prompt_text}: " value
    fi

    value="${value:-${default_value}}"
    if [ -z "${value}" ]; then
        error "${var_name} is required"
    fi

    eval "export ${var_name}='${value}'"
}

# Prompt for an optional value (empty string is acceptable)
# Skips prompt if variable is already defined in environment (even if empty)
# In non-interactive mode, defaults to empty string.
prompt_optional() {
    local var_name="$1"
    local prompt_text="$2"
    local is_secret="${3:-false}"

    # Check if variable is defined (even if set to empty string)
    if [ -n "${!var_name+x}" ]; then
        log "  ${var_name} = (pre-set via env)"
        return
    fi

    # Non-interactive: skip, leave unset
    if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
        eval "export ${var_name}=''"
        return
    fi

    if [ "${is_secret}" = "true" ]; then
        read -s -p "${prompt_text}: " value
        echo
    else
        read -p "${prompt_text}: " value
    fi

    eval "export ${var_name}='${value}'"
}

generate_key() {
    openssl rand -hex 32
}

# Detect container runtime socket on the host
detect_socket() {
    if [ -S "/run/podman/podman.sock" ]; then
        echo "/run/podman/podman.sock"
    elif [ -S "/var/run/docker.sock" ]; then
        echo "/var/run/docker.sock"
    fi
}

# ============================================================
# Manager Installation (Interactive)
# ============================================================

install_manager() {
    log "=== HiClaw Manager Installation ==="
    log "Registry: ${HICLAW_REGISTRY}"
    log ""

    # Onboarding mode selection (skip if already in non-interactive mode)
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
        log "--- Onboarding Mode ---"
        echo ""
        echo "Choose your installation mode:"
        echo "  1) Quick Start  - Fast installation with Alibaba Cloud (recommended)"
        echo "  2) Manual       - Choose LLM provider and customize options"
        echo ""
        read -p "Enter choice [1/2]: " ONBOARDING_CHOICE
        ONBOARDING_CHOICE="${ONBOARDING_CHOICE:-1}"
        
        case "${ONBOARDING_CHOICE}" in
            1|quick|quickstart)
                log "Quick Start mode selected - using Alibaba Cloud Bailian"
                HICLAW_LLM_PROVIDER="qwen"
                HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
                HICLAW_QUICKSTART=1
                ;;
            2|manual)
                log "Manual mode selected - you will choose LLM provider and customize options"
                ;;
            *)
                log "Invalid choice, defaulting to Quick Start mode"
                HICLAW_LLM_PROVIDER="qwen"
                HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
                HICLAW_QUICKSTART=1
                ;;
        esac
        log ""
    fi

    # Check if Manager is already installed (by env file existence)
    local existing_env="${HICLAW_ENV_FILE:-./hiclaw-manager.env}"
    if [ -f "${existing_env}" ]; then
        log "Existing Manager installation detected (env file: ${existing_env})"
        
        # Check for running containers
        local running_manager=""
        local running_workers=""
        local existing_workers=""
        if docker ps --format '{{.Names}}' | grep -q "^hiclaw-manager$"; then
            running_manager="hiclaw-manager"
        fi
        running_workers=$(docker ps --format '{{.Names}}' | grep "^hiclaw-worker-" || true)
        existing_workers=$(docker ps -a --format '{{.Names}}' | grep "^hiclaw-worker-" || true)
        
        # Non-interactive mode: default to upgrade without rebuilding workers
        if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
            log "Non-interactive mode: performing in-place upgrade..."
            UPGRADE_CHOICE="upgrade"
            REBUILD_WORKERS="no"
        else
            echo ""
            echo "Choose an action:"
            echo "  1) In-place upgrade (keep data, workspace, env file)"
            echo "  2) Clean reinstall (remove all data, start fresh)"
            echo "  3) Cancel"
            echo ""
            read -p "Enter choice [1/2/3]: " UPGRADE_CHOICE
            UPGRADE_CHOICE="${UPGRADE_CHOICE:-1}"
        fi

        case "${UPGRADE_CHOICE}" in
            1|upgrade)
                log "Performing in-place upgrade..."
                
                # Warn about running containers
                if [ -n "${running_manager}" ] || [ -n "${running_workers}" ]; then
                    echo ""
                    echo -e "\033[33m⚠️  Manager container will be stopped and recreated.\033[0m"
                    if [ -n "${existing_workers}" ]; then
                        echo -e "\033[33m⚠️  Worker containers will also be recreated (to update Manager IP in hosts).\033[0m"
                    fi
                    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
                        echo ""
                        read -p "Continue? [y/N]: " CONFIRM_STOP
                        if [ "${CONFIRM_STOP}" != "y" ] && [ "${CONFIRM_STOP}" != "Y" ]; then
                            log "Installation cancelled."
                            exit 0
                        fi
                    fi
                fi

                # Stop and remove manager container
                if [ -n "${running_manager}" ] || docker ps -a --format '{{.Names}}' | grep -q "^hiclaw-manager$"; then
                    log "Stopping and removing existing manager container..."
                    docker stop hiclaw-manager 2>/dev/null || true
                    docker rm hiclaw-manager 2>/dev/null || true
                fi

                # Stop and remove worker containers (Manager IP changes on restart,
                # so workers must be recreated to get updated /etc/hosts entries)
                if [ -n "${existing_workers}" ]; then
                    log "Stopping and removing existing worker containers..."
                    for w in ${existing_workers}; do
                        docker stop "${w}" 2>/dev/null || true
                        docker rm "${w}" 2>/dev/null || true
                        log "  Removed: ${w}"
                    done
                fi
                # Continue with installation using existing config
                ;;
            2|reinstall)
                log "Performing clean reinstall..."
                
                # Get existing workspace directory from env file
                local existing_workspace=""
                if [ -f "${existing_env}" ]; then
                    existing_workspace=$(grep '^HICLAW_WORKSPACE_DIR=' "${existing_env}" 2>/dev/null | cut -d= -f2-)
                fi
                if [ -z "${existing_workspace}" ]; then
                    existing_workspace="${HOME}/hiclaw-manager"
                fi
                
                # Warn about running containers
                echo ""
                echo -e "\033[33m⚠️  The following running containers will be stopped:\033[0m"
                [ -n "${running_manager}" ] && echo -e "\033[33m   - ${running_manager} (manager)\033[0m"
                for w in ${running_workers}; do
                    echo -e "\033[33m   - ${w} (worker)\033[0m"
                done
                echo ""
                echo -e "\033[31m⚠️  WARNING: This will DELETE the following:\033[0m"
                echo -e "\033[31m   - Docker volume: hiclaw-data\033[0m"
                echo -e "\033[31m   - Env file: ${existing_env}\033[0m"
                echo -e "\033[31m   - Manager workspace: ${existing_workspace}\033[0m"
                echo -e "\033[31m   - All worker containers\033[0m"
                echo ""
                echo -e "\033[31mTo confirm deletion, please type the workspace path:\033[0m"
                echo -e "\033[31m  ${existing_workspace}\033[0m"
                echo ""
                read -p "Type the path to confirm (or press Ctrl+C to cancel): " CONFIRM_PATH
                
                if [ "${CONFIRM_PATH}" != "${existing_workspace}" ]; then
                    error "Path mismatch. Aborting reinstall. Input: '${CONFIRM_PATH}', Expected: '${existing_workspace}'"
                fi
                
                log "Confirmed. Cleaning up..."
                
                # Stop and remove manager container
                docker stop hiclaw-manager 2>/dev/null || true
                docker rm hiclaw-manager 2>/dev/null || true
                
                # Stop and remove all worker containers
                for w in $(docker ps -a --format '{{.Names}}' | grep "^hiclaw-worker-" || true); do
                    docker stop "${w}" 2>/dev/null || true
                    docker rm "${w}" 2>/dev/null || true
                    log "  Removed worker: ${w}"
                done
                
                # Remove Docker volume
                if docker volume ls -q | grep -q "^hiclaw-data$"; then
                    log "Removing Docker volume: hiclaw-data"
                    docker volume rm hiclaw-data 2>/dev/null || log "  Warning: Could not remove volume (may have references)"
                fi
                
                # Remove workspace directory
                if [ -d "${existing_workspace}" ]; then
                    log "Removing workspace directory: ${existing_workspace}"
                    rm -rf "${existing_workspace}" || error "Failed to remove workspace directory"
                fi
                
                # Remove env file
                if [ -f "${existing_env}" ]; then
                    log "Removing env file: ${existing_env}"
                    rm -f "${existing_env}"
                fi
                
                log "Cleanup complete. Starting fresh installation..."
                # Clear any loaded environment variables to start fresh
                unset HICLAW_WORKSPACE_DIR
                ;;
            3|cancel|*)
                log "Installation cancelled."
                exit 0
                ;;
        esac
    fi

    # Load existing env file as fallback (shell env vars take priority)
    if [ -f "${existing_env}" ]; then
        log "Loading existing config from ${existing_env} (shell env vars take priority)..."
        while IFS='=' read -r key value; do
            # Skip comments and empty lines
            [[ "${key}" =~ ^[[:space:]]*# ]] && continue
            [[ -z "${key}" ]] && continue
            # Strip inline comments and surrounding whitespace from value
            value="${value%%#*}"
            value="${value#"${value%%[![:space:]]*}"}"
            value="${value%"${value##*[![:space:]]}"}"
            # Only set if not already set in the shell environment
            if [ -z "${!key+x}" ]; then
                export "${key}=${value}"
            fi
        done < "${existing_env}"
    fi

    # LLM Configuration
    log "--- LLM Configuration ---"
    
    if [ "${HICLAW_QUICKSTART}" = "1" ]; then
        # Quick Start mode: use Alibaba Cloud Bailian
        log "  Provider: qwen (Alibaba Cloud Bailian)"
        log "  Model: ${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
        log ""
        log "  💡 Get your Alibaba Cloud Bailian API Key from:"
        log "     https://www.aliyun.com/product/bailian"
        log ""
        prompt HICLAW_LLM_API_KEY "LLM API Key" "" "true"
    elif [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
        # Non-interactive mode: use defaults
        HICLAW_LLM_PROVIDER="${HICLAW_LLM_PROVIDER:-qwen}"
        HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
        log "  Provider: ${HICLAW_LLM_PROVIDER} (default)"
        log "  Model: ${HICLAW_DEFAULT_MODEL} (default)"
        prompt HICLAW_LLM_API_KEY "LLM API Key" "" "true"
    else
        # Manual mode: only support Alibaba Cloud or OpenAI-compatible
        echo ""
        echo "Available LLM Providers:"
        echo "  1) alibaba-cloud  - Alibaba Cloud Bailian (recommended for Chinese users)"
        echo "  2) openai-compat  - OpenAI-compatible API (OpenAI, DeepSeek, etc.)"
        echo ""
        read -p "Select provider [1/2]: " PROVIDER_CHOICE
        PROVIDER_CHOICE="${PROVIDER_CHOICE:-1}"
        
        case "${PROVIDER_CHOICE}" in
            1|alibaba-cloud)
                HICLAW_LLM_PROVIDER="qwen"
                HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
                log "  Provider: ${HICLAW_LLM_PROVIDER} (Alibaba Cloud Bailian)"
                log "  Model: ${HICLAW_DEFAULT_MODEL}"
                log ""
                log "  💡 Get your Alibaba Cloud Bailian API Key from:"
                log "     https://www.aliyun.com/product/bailian"
                log ""
                prompt HICLAW_LLM_API_KEY "LLM API Key" "" "true"
                ;;
            2|openai-compat)
                HICLAW_LLM_PROVIDER="openai-compat"
                log "  Provider: ${HICLAW_LLM_PROVIDER} (OpenAI-compatible)"
                echo ""
                read -p "Base URL (e.g., https://api.openai.com/v1): " HICLAW_OPENAI_BASE_URL
                read -p "Default Model ID [gpt-4o]: " HICLAW_DEFAULT_MODEL
                HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-gpt-4o}"
                log "  Base URL: ${HICLAW_OPENAI_BASE_URL}"
                log "  Model: ${HICLAW_DEFAULT_MODEL}"
                log ""
                prompt HICLAW_LLM_API_KEY "LLM API Key" "" "true"
                ;;
            *)
                log "Invalid choice, defaulting to Alibaba Cloud"
                HICLAW_LLM_PROVIDER="qwen"
                HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
                log "  Provider: ${HICLAW_LLM_PROVIDER}"
                log "  Model: ${HICLAW_DEFAULT_MODEL}"
                log ""
                log "  💡 Get your Alibaba Cloud Bailian API Key from:"
                log "     https://www.aliyun.com/product/bailian"
                log ""
                prompt HICLAW_LLM_API_KEY "LLM API Key" "" "true"
                ;;
        esac
    fi

    log ""

    # Admin Credentials (password auto-generated if not provided)
    log "--- Admin Credentials ---"
    prompt HICLAW_ADMIN_USER "Admin Username" "admin"
    if [ -z "${HICLAW_ADMIN_PASSWORD}" ]; then
        prompt_optional HICLAW_ADMIN_PASSWORD "Admin Password (leave empty to auto-generate, min 8 chars)" "true"
        if [ -z "${HICLAW_ADMIN_PASSWORD}" ]; then
            HICLAW_ADMIN_PASSWORD="admin$(openssl rand -hex 6)"
            log "  Auto-generated admin password"
        fi
    else
        log "  HICLAW_ADMIN_PASSWORD = (pre-set via env)"
    fi

    # Validate password length (MinIO requires at least 8 characters)
    if [ ${#HICLAW_ADMIN_PASSWORD} -lt 8 ]; then
        error "Admin password must be at least 8 characters (MinIO requirement). Current length: ${#HICLAW_ADMIN_PASSWORD}"
    fi

    log ""

    # Port Configuration (must come before Domain so MATRIX_DOMAIN default uses the correct port)
    log "--- Port Configuration (press Enter for defaults) ---"
    prompt HICLAW_PORT_GATEWAY "Host port for gateway (8080 inside container)" "18080"
    prompt HICLAW_PORT_CONSOLE "Host port for Higress console (8001 inside container)" "18001"

    log ""

    # Domain Configuration
    log "--- Domain Configuration (press Enter for defaults) ---"
    prompt HICLAW_MATRIX_DOMAIN "Matrix Domain" "matrix-local.hiclaw.io:${HICLAW_PORT_GATEWAY}"
    prompt HICLAW_MATRIX_CLIENT_DOMAIN "Element Web Domain" "matrix-client-local.hiclaw.io"
    prompt HICLAW_AI_GATEWAY_DOMAIN "AI Gateway Domain" "aigw-local.hiclaw.io"
    prompt HICLAW_FS_DOMAIN "File System Domain" "fs-local.hiclaw.io"

    log ""

    # Optional: GitHub PAT
    log "--- GitHub Integration (optional, press Enter to skip) ---"
    prompt_optional HICLAW_GITHUB_TOKEN "GitHub Personal Access Token (optional)" "true"

    log ""

    # Data persistence
    log "--- Data Persistence ---"
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ] && [ -z "${HICLAW_DATA_DIR+x}" ]; then
        read -p "External data directory (leave empty for Docker volume): " HICLAW_DATA_DIR
        export HICLAW_DATA_DIR
    fi
    if [ -n "${HICLAW_DATA_DIR}" ]; then
        HICLAW_DATA_DIR="$(cd "${HICLAW_DATA_DIR}" 2>/dev/null && pwd || echo "${HICLAW_DATA_DIR}")"
        mkdir -p "${HICLAW_DATA_DIR}"
        log "  Data directory: ${HICLAW_DATA_DIR}"
    else
        log "  Using Docker volume: hiclaw-data"
    fi

    # Manager workspace directory (skills, memory, state — host-editable)
    log "--- Manager Workspace ---"
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ] && [ -z "${HICLAW_WORKSPACE_DIR+x}" ]; then
        read -p "Manager workspace directory [${HOME}/hiclaw-manager]: " HICLAW_WORKSPACE_DIR
        HICLAW_WORKSPACE_DIR="${HICLAW_WORKSPACE_DIR:-${HOME}/hiclaw-manager}"
        export HICLAW_WORKSPACE_DIR
    elif [ -z "${HICLAW_WORKSPACE_DIR+x}" ]; then
        HICLAW_WORKSPACE_DIR="${HOME}/hiclaw-manager"
        export HICLAW_WORKSPACE_DIR
    fi
    HICLAW_WORKSPACE_DIR="$(cd "${HICLAW_WORKSPACE_DIR}" 2>/dev/null && pwd || echo "${HICLAW_WORKSPACE_DIR}")"
    mkdir -p "${HICLAW_WORKSPACE_DIR}"
    log "  Manager workspace: ${HICLAW_WORKSPACE_DIR}"

    log ""

    # Generate secrets (only if not already set)
    log "Generating secrets..."
    HICLAW_MANAGER_PASSWORD="${HICLAW_MANAGER_PASSWORD:-$(generate_key)}"
    HICLAW_REGISTRATION_TOKEN="${HICLAW_REGISTRATION_TOKEN:-$(generate_key)}"
    HICLAW_MINIO_USER="${HICLAW_MINIO_USER:-${HICLAW_ADMIN_USER}}"
    HICLAW_MINIO_PASSWORD="${HICLAW_MINIO_PASSWORD:-${HICLAW_ADMIN_PASSWORD}}"
    HICLAW_MANAGER_GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY:-$(generate_key)}"

    # Write .env file
    ENV_FILE="${HICLAW_ENV_FILE:-./hiclaw-manager.env}"
    cat > "${ENV_FILE}" << EOF
# HiClaw Manager Configuration
# Generated by hiclaw-install.sh on $(date)

# LLM
HICLAW_LLM_PROVIDER=${HICLAW_LLM_PROVIDER}
HICLAW_DEFAULT_MODEL=${HICLAW_DEFAULT_MODEL}
HICLAW_LLM_API_KEY=${HICLAW_LLM_API_KEY}
HICLAW_OPENAI_BASE_URL=${HICLAW_OPENAI_BASE_URL:-}

# Admin
HICLAW_ADMIN_USER=${HICLAW_ADMIN_USER}
HICLAW_ADMIN_PASSWORD=${HICLAW_ADMIN_PASSWORD}

# Ports
HICLAW_PORT_GATEWAY=${HICLAW_PORT_GATEWAY}
HICLAW_PORT_CONSOLE=${HICLAW_PORT_CONSOLE}

# Matrix
HICLAW_MATRIX_DOMAIN=${HICLAW_MATRIX_DOMAIN}
HICLAW_MATRIX_CLIENT_DOMAIN=${HICLAW_MATRIX_CLIENT_DOMAIN}

# Gateway
HICLAW_AI_GATEWAY_DOMAIN=${HICLAW_AI_GATEWAY_DOMAIN}
HICLAW_MANAGER_GATEWAY_KEY=${HICLAW_MANAGER_GATEWAY_KEY}

# File System
HICLAW_FS_DOMAIN=${HICLAW_FS_DOMAIN}
HICLAW_MINIO_USER=${HICLAW_MINIO_USER}
HICLAW_MINIO_PASSWORD=${HICLAW_MINIO_PASSWORD}

# Internal
HICLAW_MANAGER_PASSWORD=${HICLAW_MANAGER_PASSWORD}
HICLAW_REGISTRATION_TOKEN=${HICLAW_REGISTRATION_TOKEN}

# GitHub (optional)
HICLAW_GITHUB_TOKEN=${HICLAW_GITHUB_TOKEN:-}

# Worker image (for direct container creation)
HICLAW_WORKER_IMAGE=${WORKER_IMAGE}

# Higress WASM plugin image registry (auto-selected by timezone)
HIGRESS_ADMIN_WASM_PLUGIN_IMAGE_REGISTRY=${HICLAW_REGISTRY}

# Data persistence
HICLAW_DATA_DIR=${HICLAW_DATA_DIR:-}
# Manager workspace (skills, memory, state — host-editable)
HICLAW_WORKSPACE_DIR=${HICLAW_WORKSPACE_DIR:-}
# Host directory sharing
HICLAW_HOST_SHARE_DIR=${HICLAW_HOST_SHARE_DIR:-}
EOF

    chmod 600 "${ENV_FILE}"
    log "Configuration saved to ${ENV_FILE}"

    # Detect container runtime socket
    SOCKET_MOUNT_ARGS=""
    if [ "${HICLAW_MOUNT_SOCKET}" = "1" ]; then
        CONTAINER_SOCK=$(detect_socket)
        if [ -n "${CONTAINER_SOCK}" ]; then
            log "Container runtime socket: ${CONTAINER_SOCK} (direct Worker creation enabled)"
            SOCKET_MOUNT_ARGS="-v ${CONTAINER_SOCK}:/var/run/docker.sock --security-opt label=disable"
        else
            log "No container runtime socket found (Worker creation will output commands)"
        fi
    fi

    # Remove existing container if present
    if docker ps -a --format '{{.Names}}' | grep -q "^hiclaw-manager$"; then
        log "Removing existing hiclaw-manager container..."
        docker stop hiclaw-manager 2>/dev/null || true
        docker rm hiclaw-manager 2>/dev/null || true
    fi

    # Data mount: external directory or Docker volume
    if [ -n "${HICLAW_DATA_DIR}" ]; then
        DATA_MOUNT_ARGS="-v ${HICLAW_DATA_DIR}:/data"
    else
        DATA_MOUNT_ARGS="-v hiclaw-data:/data"
    fi

    # Manager workspace mount (always a host directory, defaulting to ~/hiclaw-manager)
    WORKSPACE_MOUNT_ARGS="-v ${HICLAW_WORKSPACE_DIR}:/root/manager-workspace"

    # Pass host timezone to container so date/time commands reflect local time
    TZ_ARGS="-e TZ=${HICLAW_TIMEZONE}"

    # Host directory mount: for file sharing with agents (defaults to user's home)
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ] && [ -z "${HICLAW_HOST_SHARE_DIR}" ]; then
        read -p "Host directory to share with agents (default: $HOME): " HICLAW_HOST_SHARE_DIR
        HICLAW_HOST_SHARE_DIR="${HICLAW_HOST_SHARE_DIR:-$HOME}"
        export HICLAW_HOST_SHARE_DIR
    elif [ -z "${HICLAW_HOST_SHARE_DIR}" ]; then
        HICLAW_HOST_SHARE_DIR="$HOME"
        export HICLAW_HOST_SHARE_DIR
    fi

    if [ -d "${HICLAW_HOST_SHARE_DIR}" ]; then
        HOST_SHARE_MOUNT_ARGS="-v ${HICLAW_HOST_SHARE_DIR}:/host-share"
        log "Sharing host directory: ${HICLAW_HOST_SHARE_DIR} -> /host-share in container"
    else
        log "WARNING: Host directory ${HICLAW_HOST_SHARE_DIR} does not exist, using without validation"
        HOST_SHARE_MOUNT_ARGS="-v ${HICLAW_HOST_SHARE_DIR}:/host-share"
    fi

    # YOLO mode: pass through if set in environment (enables autonomous decisions)
    YOLO_ARGS=""
    if [ "${HICLAW_YOLO:-}" = "1" ]; then
        YOLO_ARGS="-e HICLAW_YOLO=1"
        log "YOLO mode enabled (autonomous decisions, no interactive prompts)"
    fi

    # Pull images (worker image must be ready before manager creates workers)
    log "Pulling Manager image: ${MANAGER_IMAGE}"
    docker pull "${MANAGER_IMAGE}"
    log "Pulling Worker image: ${WORKER_IMAGE}"
    docker pull "${WORKER_IMAGE}"

    # Run Manager container
    log "Starting Manager container..."
    docker run -d \
        --name hiclaw-manager \
        --env-file "${ENV_FILE}" \
        -e HOST_ORIGINAL_HOME="${HICLAW_HOST_SHARE_DIR}" \
        ${YOLO_ARGS} \
        ${TZ_ARGS} \
        ${SOCKET_MOUNT_ARGS} \
        -p "${HICLAW_PORT_GATEWAY}:8080" \
        -p "${HICLAW_PORT_CONSOLE}:8001" \
        ${DATA_MOUNT_ARGS} \
        ${WORKSPACE_MOUNT_ARGS} \
        ${HOST_SHARE_MOUNT_ARGS} \
        --restart unless-stopped \
        "${MANAGER_IMAGE}"

    # Wait for Manager agent to be ready
    wait_manager_ready "hiclaw-manager"

    # Create OpenAI-compatible provider if needed
    if [ "${HICLAW_LLM_PROVIDER}" = "openai-compat" ]; then
        create_openai_compat_provider
    fi

    # Send welcome message to Manager
    send_welcome_message

    log ""
    log "=== HiClaw Manager Started! ==="
    log ""
    log "The following domains are configured to resolve to 127.0.0.1:"
    log "  ${HICLAW_MATRIX_DOMAIN%%:*} ${HICLAW_MATRIX_CLIENT_DOMAIN} ${HICLAW_AI_GATEWAY_DOMAIN} ${HICLAW_FS_DOMAIN}"
    log ""
    local element_url="http://${HICLAW_MATRIX_CLIENT_DOMAIN}:${HICLAW_PORT_GATEWAY}/#/login"
    echo -e "\033[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
    echo -e "\033[33m  ★ Open the following URL in your browser to start:                           ★\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[1;36m    ${element_url}\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[33m  Login with:                                                                    \033[0m"
    echo -e "\033[33m    Username: \033[1;32m${HICLAW_ADMIN_USER}\033[0m"
    echo -e "\033[33m    Password: \033[1;32m${HICLAW_ADMIN_PASSWORD}\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[33m  After login, start chatting with the Manager!                                  \033[0m"
    echo -e "\033[33m    Tell it: \"Create a Worker named alice for frontend dev\"                      \033[0m"
    echo -e "\033[33m    The Manager will handle everything automatically.                            \033[0m"
    echo -e "\033[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
    log ""
    log "--- Other Consoles ---"
    log "  Higress Console: http://localhost:${HICLAW_PORT_CONSOLE} (Username: ${HICLAW_ADMIN_USER} / Password: ${HICLAW_ADMIN_PASSWORD})"
    log ""
    log "--- Switch LLM Providers ---"
    log "  You can switch to other LLM providers (OpenAI, Anthropic, etc.) via Higress Console."
    log "  For detailed instructions, see:"
    log "  https://higress.ai/en/docs/ai/scene-guide/multi-proxy#console-configuration"
    log ""
    log "Tip: You can also ask the Manager to configure LLM providers for you in the chat."
    log ""
    log "Configuration file: ${ENV_FILE}"
    if [ -n "${HICLAW_DATA_DIR}" ]; then
        log "Data directory:     ${HICLAW_DATA_DIR}"
    else
        log "Data volume:        hiclaw-data (use HICLAW_DATA_DIR to persist externally)"
    fi
    log "Manager workspace:  ${HICLAW_WORKSPACE_DIR}"
}

# ============================================================
# Worker Installation (One-Click)
# ============================================================

install_worker() {
    local WORKER_NAME=""
    local FS=""
    local FS_KEY=""
    local FS_SECRET=""
    local RESET=false

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --name)       WORKER_NAME="$2"; shift 2 ;;
            --fs)         FS="$2"; shift 2 ;;
            --fs-key)     FS_KEY="$2"; shift 2 ;;
            --fs-secret)  FS_SECRET="$2"; shift 2 ;;
            --reset)      RESET=true; shift ;;
            *)            error "Unknown option: $1" ;;
        esac
    done

    # Validate required params
    [ -z "${WORKER_NAME}" ] && error "--name is required"
    [ -z "${FS}" ] && error "--fs is required"
    [ -z "${FS_KEY}" ] && error "--fs-key is required"
    [ -z "${FS_SECRET}" ] && error "--fs-secret is required"

    local CONTAINER_NAME="hiclaw-worker-${WORKER_NAME}"

    # Handle reset
    if [ "${RESET}" = true ]; then
        log "Resetting Worker: ${WORKER_NAME}..."
        docker stop "${CONTAINER_NAME}" 2>/dev/null || true
        docker rm "${CONTAINER_NAME}" 2>/dev/null || true
    fi

    # Check for existing container
    if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        error "Container '${CONTAINER_NAME}' already exists. Use --reset to recreate."
    fi

    log "Starting Worker: ${WORKER_NAME}..."
    docker run -d \
        --name "${CONTAINER_NAME}" \
        -e "HICLAW_WORKER_NAME=${WORKER_NAME}" \
        -e "HICLAW_FS_ENDPOINT=${FS}" \
        -e "HICLAW_FS_ACCESS_KEY=${FS_KEY}" \
        -e "HICLAW_FS_SECRET_KEY=${FS_SECRET}" \
        --restart unless-stopped \
        "${WORKER_IMAGE}"

    log ""
    log "=== Worker ${WORKER_NAME} Started! ==="
    log "Container: ${CONTAINER_NAME}"
    log "View logs: docker logs -f ${CONTAINER_NAME}"
}

# ============================================================
# Main
# ============================================================

case "${1:-}" in
    manager|"")
        # Default to manager installation if no argument or explicit "manager"
        install_manager
        ;;
    worker)
        shift
        install_worker "$@"
        ;;
    *)
        echo "Usage: $0 [manager|worker [options]]"
        echo ""
        echo "Commands:"
        echo "  manager              Interactive Manager installation (default)"
        echo "                       Choose Quick Start (all defaults) or Manual mode"
        echo "  worker               Worker installation (requires --name and connection params)"
        echo ""
        echo "Quick Start (fastest):"
        echo "  $0"
        echo "  # Then select '1' for Quick Start mode"
        echo ""
        echo "Non-interactive (for automation):"
        echo "  HICLAW_NON_INTERACTIVE=1 HICLAW_LLM_API_KEY=sk-xxx $0"
        echo ""
        echo "Worker Options:"
        echo "  --name <name>        Worker name (required)"
        echo "  --fs <url>           MinIO endpoint URL (required)"
        echo "  --fs-key <key>       MinIO access key (required)"
        echo "  --fs-secret <secret> MinIO secret key (required)"
        echo "  --reset              Remove existing Worker container before creating"
        exit 1
        ;;
esac
