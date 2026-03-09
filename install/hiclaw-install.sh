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
#   HICLAW_DATA_DIR           Docker volume name for persistent data (default: hiclaw-data)
#   HICLAW_WORKSPACE_DIR      Host directory for manager workspace (default: ~/hiclaw-manager)
#   HICLAW_VERSION            Image tag            (default: latest)
#   HICLAW_REGISTRY           Image registry       (default: auto-detected by timezone)
#   HICLAW_INSTALL_MANAGER_IMAGE  Override manager image (e.g., local build)
#   HICLAW_INSTALL_WORKER_IMAGE   Override worker image  (e.g., local build)
#   HICLAW_PORT_GATEWAY       Host port for Higress gateway (default: 18080)
#   HICLAW_PORT_CONSOLE       Host port for Higress console (default: 18001)
#   HICLAW_PORT_ELEMENT_WEB   Host port for Element Web direct access (default: 18088)

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
# Language detection based on timezone
# ============================================================

detect_language() {
    local tz="${HICLAW_TIMEZONE}"
    case "${tz}" in
        Asia/Shanghai|Asia/Chongqing|Asia/Harbin|Asia/Urumqi|\
        Asia/Taipei|Asia/Hong_Kong|Asia/Macau)
            echo "zh"
            ;;
        *)
            echo "en"
            ;;
    esac
}

# Language priority: env var > existing env file > timezone detection
if [ -z "${HICLAW_LANGUAGE}" ]; then
    # Check existing env file for saved language preference (upgrade scenario)
    _env_file="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
    # Migrate from legacy location (current directory) if needed
    if [ ! -f "${_env_file}" ] && [ -f "./hiclaw-manager.env" ]; then
        mv "./hiclaw-manager.env" "${_env_file}" 2>/dev/null || true
    fi
    if [ -f "${_env_file}" ]; then
        _saved_lang=$(grep '^HICLAW_LANGUAGE=' "${_env_file}" 2>/dev/null | cut -d= -f2-)
        if [ -n "${_saved_lang}" ]; then
            HICLAW_LANGUAGE="${_saved_lang}"
        fi
    fi
    # Fall back to timezone-based detection
    if [ -z "${HICLAW_LANGUAGE}" ]; then
        HICLAW_LANGUAGE="$(detect_language)"
    fi
    unset _env_file _saved_lang
fi
export HICLAW_LANGUAGE

# ============================================================
# Centralized message dictionary and msg() function
# Compatible with bash 3.2+ (macOS default) — uses case instead of declare -A
# ============================================================

# msg() function: look up message by key, with printf-style argument substitution
# Falls back to English if the current language translation is missing.
msg() {
    local key="$1"
    shift
    local lang="${HICLAW_LANGUAGE:-en}"
    local text=""
    case "${key}.${lang}" in
        # --- Timezone detection messages ---
        "tz.warning.title.zh") text="无法自动检测时区。" ;;
        "tz.warning.title.en") text="Could not detect timezone automatically." ;;
        "tz.warning.prompt.zh") text="请输入您的时区（例如 Asia/Shanghai、America/New_York）。" ;;
        "tz.warning.prompt.en") text="Please enter your timezone (e.g., Asia/Shanghai, America/New_York)." ;;
        "tz.default.zh") text="使用默认时区: %s" ;;
        "tz.default.en") text="Using default timezone: %s" ;;
        "tz.input_prompt.zh") text="时区" ;;
        "tz.input_prompt.en") text="Timezone" ;;
        # --- Installation title and info ---
        "install.title.zh") text="=== HiClaw Manager 安装 ===" ;;
        "install.title.en") text="=== HiClaw Manager Installation ===" ;;
        "install.registry.zh") text="镜像仓库: %s" ;;
        "install.registry.en") text="Registry: %s" ;;
        "install.dir.zh") text="安装目录: %s" ;;
        "install.dir.en") text="Installation directory: %s" ;;
        "install.dir_hint.zh") text="  （env 文件 'hiclaw-manager.env' 将保存到 HOME 目录。）" ;;
        "install.dir_hint.en") text="  (The env file 'hiclaw-manager.env' will be saved to your HOME directory.)" ;;
        "install.dir_hint2.zh") text="  （请从您希望管理此安装的目录运行此脚本。）" ;;
        "install.dir_hint2.en") text="  (Run this script from the directory where you want to manage this installation.)" ;;
        # --- Onboarding mode ---
        "install.mode.title.zh") text="--- Onboarding 模式 ---" ;;
        "install.mode.title.en") text="--- Onboarding Mode ---" ;;
        "install.mode.choose.zh") text="选择安装模式:" ;;
        "install.mode.choose.en") text="Choose your installation mode:" ;;
        "install.mode.quickstart.zh") text="  1) 快速开始  - 使用阿里云百炼快速安装（推荐）" ;;
        "install.mode.quickstart.en") text="  1) Quick Start  - Fast installation with Alibaba Cloud (recommended)" ;;
        "install.mode.manual.zh") text="  2) 手动配置  - 选择 LLM 提供商并自定义选项" ;;
        "install.mode.manual.en") text="  2) Manual       - Choose LLM provider and customize options" ;;
        "install.mode.prompt.zh") text="请选择 [1/2]" ;;
        "install.mode.prompt.en") text="Enter choice [1/2]" ;;
        "install.mode.quickstart_selected.zh") text="已选择快速开始模式 - 使用阿里云百炼" ;;
        "install.mode.quickstart_selected.en") text="Quick Start mode selected - using Alibaba Cloud Bailian" ;;
        "install.mode.manual_selected.zh") text="已选择手动配置模式 - 您将选择 LLM 提供商并自定义选项" ;;
        "install.mode.manual_selected.en") text="Manual mode selected - you will choose LLM provider and customize options" ;;
        "install.mode.invalid.zh") text="无效选择，默认使用快速开始模式" ;;
        "install.mode.invalid.en") text="Invalid choice, defaulting to Quick Start mode" ;;
        # --- Existing installation detected ---
        "install.existing.detected.zh") text="检测到已有 Manager 安装（env 文件: %s）" ;;
        "install.existing.detected.en") text="Existing Manager installation detected (env file: %s)" ;;
        "install.existing.choose.zh") text="选择操作:" ;;
        "install.existing.choose.en") text="Choose an action:" ;;
        "install.existing.upgrade.zh") text="  1) 就地升级（保留数据、工作空间、env 文件）" ;;
        "install.existing.upgrade.en") text="  1) In-place upgrade (keep data, workspace, env file)" ;;
        "install.existing.reinstall.zh") text="  2) 全新重装（删除所有数据，重新开始）" ;;
        "install.existing.reinstall.en") text="  2) Clean reinstall (remove all data, start fresh)" ;;
        "install.existing.cancel.zh") text="  3) 取消" ;;
        "install.existing.cancel.en") text="  3) Cancel" ;;
        "install.existing.prompt.zh") text="请选择 [1/2/3]" ;;
        "install.existing.prompt.en") text="Enter choice [1/2/3]" ;;
        "install.existing.upgrade_noninteractive.zh") text="非交互模式: 执行就地升级..." ;;
        "install.existing.upgrade_noninteractive.en") text="Non-interactive mode: performing in-place upgrade..." ;;
        "install.existing.upgrading.zh") text="执行就地升级..." ;;
        "install.existing.upgrading.en") text="Performing in-place upgrade..." ;;
        "install.existing.warn_manager_stop.zh") text="⚠️  Manager 容器将被停止并重新创建。" ;;
        "install.existing.warn_manager_stop.en") text="⚠️  Manager container will be stopped and recreated." ;;
        "install.existing.warn_worker_recreate.zh") text="⚠️  Worker 容器也将被重新创建（以更新 Manager IP）。" ;;
        "install.existing.warn_worker_recreate.en") text="⚠️  Worker containers will also be recreated (to update Manager IP in hosts)." ;;
        "install.existing.continue_prompt.zh") text="继续？[y/N]" ;;
        "install.existing.continue_prompt.en") text="Continue? [y/N]" ;;
        "install.existing.cancelled.zh") text="安装已取消。" ;;
        "install.existing.cancelled.en") text="Installation cancelled." ;;
        "install.existing.stopping_manager.zh") text="停止并移除现有 manager 容器..." ;;
        "install.existing.stopping_manager.en") text="Stopping and removing existing manager container..." ;;
        "install.existing.stopping_workers.zh") text="停止并移除现有 worker 容器..." ;;
        "install.existing.stopping_workers.en") text="Stopping and removing existing worker containers..." ;;
        "install.existing.removed.zh") text="  已移除: %s" ;;
        "install.existing.removed.en") text="  Removed: %s" ;;
        # --- Clean reinstall messages ---
        "install.reinstall.performing.zh") text="执行全新重装..." ;;
        "install.reinstall.performing.en") text="Performing clean reinstall..." ;;
        "install.reinstall.warn_stop.zh") text="⚠️  以下运行中的容器将被停止:" ;;
        "install.reinstall.warn_stop.en") text="⚠️  The following running containers will be stopped:" ;;
        "install.reinstall.warn_delete.zh") text="⚠️  警告: 以下内容将被删除:" ;;
        "install.reinstall.warn_delete.en") text="⚠️  WARNING: This will DELETE the following:" ;;
        "install.reinstall.warn_volume.zh") text="   - Docker 卷: hiclaw-data" ;;
        "install.reinstall.warn_volume.en") text="   - Docker volume: hiclaw-data" ;;
        "install.reinstall.warn_env.zh") text="   - Env 文件: %s" ;;
        "install.reinstall.warn_env.en") text="   - Env file: %s" ;;
        "install.reinstall.warn_workspace.zh") text="   - Manager 工作空间: %s" ;;
        "install.reinstall.warn_workspace.en") text="   - Manager workspace: %s" ;;
        "install.reinstall.warn_workers.zh") text="   - 所有 worker 容器" ;;
        "install.reinstall.warn_workers.en") text="   - All worker containers" ;;
        "install.reinstall.confirm_type.zh") text="请输入工作空间路径以确认删除（或按 Ctrl+C 取消）:" ;;
        "install.reinstall.confirm_type.en") text="To confirm deletion, please type the workspace path:" ;;
        "install.reinstall.confirm_path.zh") text="输入路径以确认（或按 Ctrl+C 取消）" ;;
        "install.reinstall.confirm_path.en") text="Type the path to confirm (or press Ctrl+C to cancel)" ;;
        "install.reinstall.path_mismatch.zh") text="路径不匹配。中止重装。输入: '%s'，期望: '%s'" ;;
        "install.reinstall.path_mismatch.en") text="Path mismatch. Aborting reinstall. Input: '%s', Expected: '%s'" ;;
        "install.reinstall.confirmed.zh") text="已确认。正在清理..." ;;
        "install.reinstall.confirmed.en") text="Confirmed. Cleaning up..." ;;
        "install.reinstall.removed_worker.zh") text="  已移除 worker: %s" ;;
        "install.reinstall.removed_worker.en") text="  Removed worker: %s" ;;
        "install.reinstall.removing_volume.zh") text="正在移除 Docker 卷: hiclaw-data" ;;
        "install.reinstall.removing_volume.en") text="Removing Docker volume: hiclaw-data" ;;
        "install.reinstall.warn_volume_fail.zh") text="  警告: 无法移除卷（可能有引用）" ;;
        "install.reinstall.warn_volume_fail.en") text="  Warning: Could not remove volume (may have references)" ;;
        "install.reinstall.removing_workspace.zh") text="正在移除工作空间目录: %s" ;;
        "install.reinstall.removing_workspace.en") text="Removing workspace directory: %s" ;;
        "install.reinstall.removing_env.zh") text="正在移除 env 文件: %s" ;;
        "install.reinstall.removing_env.en") text="Removing env file: %s" ;;
        "install.reinstall.cleanup_done.zh") text="清理完成。开始全新安装..." ;;
        "install.reinstall.cleanup_done.en") text="Cleanup complete. Starting fresh installation..." ;;
        "install.reinstall.failed_rm_workspace.zh") text="无法移除工作空间目录" ;;
        "install.reinstall.failed_rm_workspace.en") text="Failed to remove workspace directory" ;;
        # --- Loading existing config ---
        "install.loading_config.zh") text="从 %s 加载已有配置（shell 环境变量优先）..." ;;
        "install.loading_config.en") text="Loading existing config from %s (shell env vars take priority)..." ;;
        # --- LLM Configuration ---
        "llm.title.zh") text="--- LLM 配置 ---" ;;
        "llm.title.en") text="--- LLM Configuration ---" ;;
        "llm.provider.label.zh") text="  提供商: %s" ;;
        "llm.provider.label.en") text="  Provider: %s" ;;
        "llm.model.label.zh") text="  模型: %s" ;;
        "llm.model.label.en") text="  Model: %s" ;;
        "llm.provider.qwen.zh") text="  提供商: qwen（阿里云百炼）" ;;
        "llm.provider.qwen.en") text="  Provider: qwen (Alibaba Cloud Bailian)" ;;
        "llm.provider.qwen_default.zh") text="  提供商: %s（默认）" ;;
        "llm.provider.qwen_default.en") text="  Provider: %s (default)" ;;
        "llm.model.default.zh") text="  模型: %s（默认）" ;;
        "llm.model.default.en") text="  Model: %s (default)" ;;
        "llm.apikey_hint.zh") text="  💡 获取阿里云百炼 API Key:" ;;
        "llm.apikey_hint.en") text="  💡 Get your Alibaba Cloud Bailian API Key from:" ;;
        "llm.apikey_url.zh") text="     https://www.aliyun.com/product/bailian" ;;
        "llm.apikey_url.en") text="     https://www.aliyun.com/product/bailian" ;;
        "llm.apikey_prompt.zh") text="LLM API Key" ;;
        "llm.apikey_prompt.en") text="LLM API Key" ;;
        "llm.providers_title.zh") text="可用 LLM 提供商:" ;;
        "llm.providers_title.en") text="Available LLM Providers:" ;;
        "llm.provider.alibaba.zh") text="  1) 阿里云百炼  - 推荐中国用户使用" ;;
        "llm.provider.alibaba.en") text="  1) Alibaba Cloud Bailian  - Recommended for Chinese users" ;;
        "llm.provider.openai_compat.zh") text="  2) OpenAI 兼容 API  - 自定义 Base URL（OpenAI、DeepSeek 等）" ;;
        "llm.provider.openai_compat.en") text="  2) OpenAI-compatible API  - Custom Base URL (OpenAI, DeepSeek, etc.)" ;;
        "llm.provider.select.zh") text="选择提供商 [1/2]" ;;
        "llm.provider.select.en") text="Select provider [1/2]" ;;
        "llm.alibaba.models_title.zh") text="选择百炼模型系列:" ;;
        "llm.alibaba.models_title.en") text="Select Bailian model series:" ;;
        "llm.alibaba.model.codingplan.zh") text="  1) CodingPlan  - 专为编程任务优化（推荐）" ;;
        "llm.alibaba.model.codingplan.en") text="  1) CodingPlan  - Optimized for coding tasks (recommended)" ;;
        "llm.alibaba.model.qwen.zh") text="  2) 百炼通用接口" ;;
        "llm.alibaba.model.qwen.en") text="  2) qwen general  - General purpose LLM" ;;
        "llm.alibaba.model.select.zh") text="选择模型系列 [1/2]" ;;
        "llm.alibaba.model.select.en") text="Select model series [1/2]" ;;
        "llm.codingplan.models_title.zh") text="选择 CodingPlan 默认模型:" ;;
        "llm.codingplan.models_title.en") text="Select CodingPlan default model:" ;;
        "llm.codingplan.model.qwen35plus.zh") text="  1) qwen3.5-plus  - 通义千问 3.5（推荐）" ;;
        "llm.codingplan.model.qwen35plus.en") text="  1) qwen3.5-plus  - Qwen 3.5 (recommended)" ;;
        "llm.codingplan.model.glm5.zh") text="  2) glm-5  - 智谱 GLM-5" ;;
        "llm.codingplan.model.glm5.en") text="  2) glm-5  - Zhipu GLM-5" ;;
        "llm.codingplan.model.kimi.zh") text="  3) kimi-k2.5  - Moonshot Kimi K2.5" ;;
        "llm.codingplan.model.kimi.en") text="  3) kimi-k2.5  - Moonshot Kimi K2.5" ;;
        "llm.codingplan.model.minimax.zh") text="  4) MiniMax-M2.5  - MiniMax M2.5" ;;
        "llm.codingplan.model.minimax.en") text="  4) MiniMax-M2.5  - MiniMax M2.5" ;;
        "llm.codingplan.model.select.zh") text="选择模型 [1/2/3/4]" ;;
        "llm.codingplan.model.select.en") text="Select model [1/2/3/4]" ;;
        "llm.provider.selected_codingplan.zh") text="  提供商: 阿里云百炼 CodingPlan" ;;
        "llm.provider.selected_codingplan.en") text="  Provider: Alibaba Cloud Bailian CodingPlan" ;;
        "llm.provider.selected_qwen.zh") text="  提供商: 阿里云百炼" ;;
        "llm.provider.selected_qwen.en") text="  Provider: Alibaba Cloud Bailian" ;;
        "llm.provider.selected_openai.zh") text="  提供商: %s（OpenAI 兼容）" ;;
        "llm.provider.selected_openai.en") text="  Provider: %s (OpenAI-compatible)" ;;
        "llm.provider.invalid.zh") text="无效选择，默认使用阿里云百炼 CodingPlan" ;;
        "llm.provider.invalid.en") text="Invalid choice, defaulting to Alibaba Cloud Bailian CodingPlan" ;;
        "llm.openai.base_url_prompt.zh") text="Base URL（例如 https://api.openai.com/v1）" ;;
        "llm.openai.base_url_prompt.en") text="Base URL (e.g., https://api.openai.com/v1)" ;;
        "llm.openai.model_prompt.zh") text="默认模型 ID [gpt-4o]" ;;
        "llm.openai.model_prompt.en") text="Default Model ID [gpt-4o]" ;;
        "llm.openai.base_url_label.zh") text="  Base URL: %s" ;;
        "llm.openai.base_url_label.en") text="  Base URL: %s" ;;
        # --- Admin Credentials ---
        "admin.title.zh") text="--- 管理员凭据 ---" ;;
        "admin.title.en") text="--- Admin Credentials ---" ;;
        "admin.username_prompt.zh") text="管理员用户名" ;;
        "admin.username_prompt.en") text="Admin Username" ;;
        "admin.password_prompt.zh") text="管理员密码（留空自动生成，最少 8 位）" ;;
        "admin.password_prompt.en") text="Admin Password (leave empty to auto-generate, min 8 chars)" ;;
        "admin.password_generated.zh") text="  已自动生成管理员密码" ;;
        "admin.password_generated.en") text="  Auto-generated admin password" ;;
        "admin.password_too_short.zh") text="管理员密码至少需要 8 个字符（MinIO 要求）。当前长度: %s" ;;
        "admin.password_too_short.en") text="Admin password must be at least 8 characters (MinIO requirement). Current length: %s" ;;
        # --- Port Configuration ---
        "port.title.zh") text="--- 端口配置（按回车使用默认值）---" ;;
        "port.title.en") text="--- Port Configuration (press Enter for defaults) ---" ;;
        "port.gateway_prompt.zh") text="网关主机端口（容器内 8080）" ;;
        "port.gateway_prompt.en") text="Host port for gateway (8080 inside container)" ;;
        "port.console_prompt.zh") text="Higress 控制台主机端口（容器内 8001）" ;;
        "port.console_prompt.en") text="Host port for Higress console (8001 inside container)" ;;
        "port.element_prompt.zh") text="Element Web 直接访问主机端口（容器内 8088）" ;;
        "port.element_prompt.en") text="Host port for Element Web direct access (8088 inside container)" ;;
        # --- Local-only binding ---
        "port.local_only.title.zh") text="--- 网络访问模式 ---" ;;
        "port.local_only.title.en") text="--- Network Access Mode ---" ;;
        "port.local_only.prompt.zh") text="是否仅允许本机访问（端口绑定到 127.0.0.1）？" ;;
        "port.local_only.prompt.en") text="Bind ports to localhost only (127.0.0.1)?" ;;
        "port.local_only.hint_yes.zh") text="  仅本机使用，无需开放外部端口（推荐）" ;;
        "port.local_only.hint_yes.en") text="  Local use only, no external port exposure (recommended)" ;;
        "port.local_only.hint_no.zh") text="  允许外部访问（局域网 / 公网）" ;;
        "port.local_only.hint_no.en") text="  Allow external access (LAN / public network)" ;;
        "port.local_only.choice.zh") text="请选择 [1/2]" ;;
        "port.local_only.choice.en") text="Enter choice [1/2]" ;;
        "port.local_only.selected_local.zh") text="端口已绑定到 127.0.0.1（仅本机访问）" ;;
        "port.local_only.selected_local.en") text="Ports bound to 127.0.0.1 (localhost only)" ;;
        "port.local_only.selected_external.zh") text="端口已绑定到所有网络接口（0.0.0.0）" ;;
        "port.local_only.selected_external.en") text="Ports bound to all interfaces (0.0.0.0)" ;;
        "port.local_only.https_hint.zh") text="⚠️  建议在 Higress 控制台配置 TLS 证书并启用 HTTPS，避免明文传输。" ;;
        "port.local_only.https_hint.en") text="⚠️  It is recommended to configure TLS certificates and enable HTTPS in the Higress Console to avoid plaintext transmission." ;;
        "port.local_only.https_docs.zh") text="" ;;
        "port.local_only.https_docs.en") text="" ;;
        # --- Domain Configuration ---
        "domain.title.zh") text="--- 域名配置（按回车使用默认值）---" ;;
        "domain.title.en") text="--- Domain Configuration (press Enter for defaults) ---" ;;
        "domain.matrix_prompt.zh") text="Matrix 域名" ;;
        "domain.matrix_prompt.en") text="Matrix Domain" ;;
        "domain.element_prompt.zh") text="Element Web 域名" ;;
        "domain.element_prompt.en") text="Element Web Domain" ;;
        "domain.gateway_prompt.zh") text="AI 网关域名" ;;
        "domain.gateway_prompt.en") text="AI Gateway Domain" ;;
        "domain.fs_prompt.zh") text="文件系统域名" ;;
        "domain.fs_prompt.en") text="File System Domain" ;;
        # --- GitHub Integration ---
        "github.title.zh") text="--- GitHub 集成（可选，按回车跳过）---" ;;
        "github.title.en") text="--- GitHub Integration (optional, press Enter to skip) ---" ;;
        "github.token_prompt.zh") text="GitHub 个人访问令牌（可选）" ;;
        "github.token_prompt.en") text="GitHub Personal Access Token (optional)" ;;
        # --- Skills Registry ---
        "skills.title.zh") text="--- Skills 注册中心（可选，按回车使用默认 https://skills.sh）---" ;;
        "skills.title.en") text="--- Skills Registry (optional, press Enter for default https://skills.sh) ---" ;;
        "skills.url_prompt.zh") text="Skills 注册中心 URL（留空使用默认 https://skills.sh）" ;;
        "skills.url_prompt.en") text="Skills Registry URL (leave empty for default https://skills.sh)" ;;
        # --- Data Persistence ---
        "data.title.zh") text="--- 数据持久化 ---" ;;
        "data.title.en") text="--- Data Persistence ---" ;;
        "data.volume_prompt.zh") text="Docker 卷名称 [hiclaw-data]" ;;
        "data.volume_prompt.en") text="Docker volume name for persistent data [hiclaw-data]" ;;
        "data.volume_using.zh") text="  使用 Docker 卷: %s" ;;
        "data.volume_using.en") text="  Using Docker volume: %s" ;;
        # --- Manager Workspace ---
        "workspace.title.zh") text="--- Manager 工作空间 ---" ;;
        "workspace.title.en") text="--- Manager Workspace ---" ;;
        "workspace.dir_prompt.zh") text="Manager 工作空间目录 [%s]" ;;
        "workspace.dir_prompt.en") text="Manager workspace directory [%s]" ;;
        "workspace.dir_label.zh") text="  Manager 工作空间: %s" ;;
        "workspace.dir_label.en") text="  Manager workspace: %s" ;;
        # --- Host directory sharing ---
        "host_share.prompt.zh") text="与 Agent 共享的主机目录（默认: %s）" ;;
        "host_share.prompt.en") text="Host directory to share with agents (default: %s)" ;;
        "host_share.sharing.zh") text="共享主机目录: %s -> 容器内 /host-share" ;;
        "host_share.sharing.en") text="Sharing host directory: %s -> /host-share in container" ;;
        "host_share.not_exist.zh") text="警告: 主机目录 %s 不存在，跳过验证继续使用" ;;
        "host_share.not_exist.en") text="WARNING: Host directory %s does not exist, using without validation" ;;
        # --- Secrets and config ---
        "install.generating_secrets.zh") text="正在生成密钥..." ;;
        "install.generating_secrets.en") text="Generating secrets..." ;;
        "install.config_saved.zh") text="配置已保存到 %s" ;;
        "install.config_saved.en") text="Configuration saved to %s" ;;
        # --- Container runtime socket ---
        "install.socket_detected.zh") text="容器运行时 socket: %s（已启用直接创建 Worker）" ;;
        "install.socket_detected.en") text="Container runtime socket: %s (direct Worker creation enabled)" ;;
        "install.socket_not_found.zh") text="未找到容器运行时 socket（Worker 创建将输出命令）" ;;
        "install.socket_not_found.en") text="No container runtime socket found (Worker creation will output commands)" ;;
        # --- Container management ---
        "install.removing_existing.zh") text="正在移除现有 hiclaw-manager 容器..." ;;
        "install.removing_existing.en") text="Removing existing hiclaw-manager container..." ;;
        # --- YOLO mode ---
        "install.yolo.zh") text="YOLO 模式已启用（自主决策，无交互提示）" ;;
        "install.yolo.en") text="YOLO mode enabled (autonomous decisions, no interactive prompts)" ;;
        # --- Image pulling ---
        "install.image.exists.zh") text="Manager 镜像已存在: %s" ;;
        "install.image.exists.en") text="Manager image already exists locally: %s" ;;
        "install.image.pulling_manager.zh") text="正在拉取 Manager 镜像: %s" ;;
        "install.image.pulling_manager.en") text="Pulling Manager image: %s" ;;
        "install.image.worker_exists.zh") text="Worker 镜像已存在: %s" ;;
        "install.image.worker_exists.en") text="Worker image already exists locally: %s" ;;
        "install.image.pulling_worker.zh") text="正在拉取 Worker 镜像: %s" ;;
        "install.image.pulling_worker.en") text="Pulling Worker image: %s" ;;
        # --- Starting container ---
        "install.starting_manager.zh") text="正在启动 Manager 容器..." ;;
        "install.starting_manager.en") text="Starting Manager container..." ;;
        # --- Wait for Manager ready ---
        "install.wait_ready.zh") text="等待 Manager Agent 就绪（超时: %ss）..." ;;
        "install.wait_ready.en") text="Waiting for Manager agent to be ready (timeout: %ss)..." ;;
        "install.wait_ready.ok.zh") text="Manager Agent 已就绪！" ;;
        "install.wait_ready.ok.en") text="Manager agent is ready!" ;;
        "install.wait_ready.waiting.zh") text="等待中... (%ds/%ds)" ;;
        "install.wait_ready.waiting.en") text="Waiting... (%ds/%ds)" ;;
        "install.wait_ready.timeout.zh") text="Manager Agent 在 %ss 内未就绪。请检查: docker logs %s" ;;
        "install.wait_ready.timeout.en") text="Manager agent did not become ready within %ss. Check: docker logs %s" ;;
        # --- Wait for Matrix ready ---
        "install.wait_matrix.zh") text="等待 Matrix 服务就绪（超时: %ss）..." ;;
        "install.wait_matrix.en") text="Waiting for Matrix server to be ready (timeout: %ss)..." ;;
        "install.wait_matrix.ok.zh") text="Matrix 服务已就绪！" ;;
        "install.wait_matrix.ok.en") text="Matrix server is ready!" ;;
        "install.wait_matrix.waiting.zh") text="等待 Matrix 中... (%ds/%ds)" ;;
        "install.wait_matrix.waiting.en") text="Waiting for Matrix... (%ds/%ds)" ;;
        "install.wait_matrix.timeout.zh") text="Matrix 服务在 %ss 内未就绪。请检查: docker logs %s" ;;
        "install.wait_matrix.timeout.en") text="Matrix server did not become ready within %ss. Check: docker logs %s" ;;
        # --- OpenAI-compatible connectivity test ---
        "llm.openai.test.testing.zh") text="正在测试 API 联通性..." ;;
        "llm.openai.test.testing.en") text="Testing API connectivity..." ;;
        "llm.openai.test.ok.zh") text="✅ API 联通性测试通过" ;;
        "llm.openai.test.ok.en") text="✅ API connectivity test passed" ;;
        "llm.openai.test.fail.zh") text="⚠️  API 联通性测试失败（HTTP %s）。响应内容:\n%s\n请根据以上错误信息联系您的模型服务商解决。" ;;
        "llm.openai.test.fail.en") text="⚠️  API connectivity test failed (HTTP %s). Response body:\n%s\nPlease contact your model provider to resolve the issue." ;;
        "llm.openai.test.fail.codingplan.zh") text="⚠️  提示: 请确认您的 API Key 已开通阿里云百炼 CodingPlan 服务。开通地址: https://www.aliyun.com/benefit/scene/codingplan" ;;
        "llm.openai.test.fail.codingplan.en") text="⚠️  Hint: Please verify that your API Key has CodingPlan service enabled on Alibaba Cloud Bailian. Enable at: https://www.aliyun.com/benefit/scene/codingplan" ;;
        "llm.openai.test.no_curl.zh") text="⚠️  未找到 curl，跳过 API 联通性测试" ;;
        "llm.openai.test.no_curl.en") text="⚠️  curl not found, skipping API connectivity test" ;;
        "llm.openai.test.confirm.zh") text="是否仍要继续安装？[y/N] " ;;
        "llm.openai.test.confirm.en") text="Continue with installation anyway? [y/N] " ;;
        "llm.openai.test.aborted.zh") text="安装已中止。" ;;
        "llm.openai.test.aborted.en") text="Installation aborted." ;;
        # --- OpenAI-compatible provider creation ---
        "install.openai_compat.missing.zh") text="警告: OpenAI Base URL 或 API Key 未设置，跳过提供商创建" ;;
        "install.openai_compat.missing.en") text="WARNING: OpenAI Base URL or API Key not set, skipping provider creation" ;;
        "install.openai_compat.creating.zh") text="正在创建 OpenAI 兼容提供商..." ;;
        "install.openai_compat.creating.en") text="Creating OpenAI-compatible provider..." ;;
        "install.openai_compat.domain.zh") text="  域名: %s" ;;
        "install.openai_compat.domain.en") text="  Domain: %s" ;;
        "install.openai_compat.port.zh") text="  端口: %s" ;;
        "install.openai_compat.port.en") text="  Port: %s" ;;
        "install.openai_compat.protocol.zh") text="  协议: %s" ;;
        "install.openai_compat.protocol.en") text="  Protocol: %s" ;;
        "install.openai_compat.service_fail.zh") text="警告: 创建 DNS 服务源失败（可能已存在）" ;;
        "install.openai_compat.service_fail.en") text="WARNING: Failed to create DNS service source (may already exist)" ;;
        "install.openai_compat.provider_fail.zh") text="警告: 创建 AI 提供商失败（可能已存在）" ;;
        "install.openai_compat.provider_fail.en") text="WARNING: Failed to create AI provider (may already exist)" ;;
        "install.openai_compat.success.zh") text="OpenAI 兼容提供商创建成功" ;;
        "install.openai_compat.success.en") text="OpenAI-compatible provider created successfully" ;;
        # --- Welcome message ---
        "install.welcome_msg.soul_configured.zh") text="Soul 已配置（找到 soul-configured 标记），跳过 onboarding 消息" ;;
        "install.welcome_msg.soul_configured.en") text="Soul already configured (soul-configured marker found), skipping onboarding message" ;;
        "install.welcome_msg.logging_in.zh") text="正在以 %s 身份登录以发送欢迎消息..." ;;
        "install.welcome_msg.logging_in.en") text="Logging in as %s to send welcome message..." ;;
        "install.welcome_msg.login_failed.zh") text="警告: 以 %s 身份登录失败，跳过欢迎消息" ;;
        "install.welcome_msg.login_failed.en") text="WARNING: Failed to login as %s, skipping welcome message" ;;
        "install.welcome_msg.finding_room.zh") text="正在查找与 Manager 的 DM 房间..." ;;
        "install.welcome_msg.finding_room.en") text="Finding DM room with Manager..." ;;
        "install.welcome_msg.creating_room.zh") text="正在创建与 Manager 的 DM 房间..." ;;
        "install.welcome_msg.creating_room.en") text="Creating DM room with Manager..." ;;
        "install.welcome_msg.no_room.zh") text="警告: 无法找到或创建与 Manager 的 DM 房间" ;;
        "install.welcome_msg.no_room.en") text="WARNING: Could not find or create DM room with Manager" ;;
        "install.welcome_msg.waiting_join.zh") text="等待 Manager 加入房间..." ;;
        "install.welcome_msg.waiting_join.en") text="Waiting for Manager to join the room..." ;;
        "install.welcome_msg.sending.zh") text="正在向 Manager 发送欢迎消息..." ;;
        "install.welcome_msg.sending.en") text="Sending welcome message to Manager..." ;;
        "install.welcome_msg.send_failed.zh") text="警告: 发送欢迎消息失败" ;;
        "install.welcome_msg.send_failed.en") text="WARNING: Failed to send welcome message" ;;
        "install.welcome_msg.sent.zh") text="欢迎消息已发送给 Manager" ;;
        "install.welcome_msg.sent.en") text="Welcome message sent to Manager" ;;
        # --- Final output panel ---
        "success.title.zh") text="=== HiClaw Manager 已启动！===" ;;
        "success.title.en") text="=== HiClaw Manager Started! ===" ;;
        "success.domains_configured.zh") text="以下域名已配置解析到 127.0.0.1:" ;;
        "success.domains_configured.en") text="The following domains are configured to resolve to 127.0.0.1:" ;;
        "success.open_url.zh") text="  ★ 在浏览器中打开以下 URL 开始使用:                           ★" ;;
        "success.open_url.en") text="  ★ Open the following URL in your browser to start:                           ★" ;;
        "success.login_with.zh") text="  登录信息:" ;;
        "success.login_with.en") text="  Login with:" ;;
        "success.username.zh") text="    用户名: %s" ;;
        "success.username.en") text="    Username: %s" ;;
        "success.password.zh") text="    密码: %s" ;;
        "success.password.en") text="    Password: %s" ;;
        "success.after_login.zh") text="  登录后，开始与 Manager 聊天！" ;;
        "success.after_login.en") text="  After login, start chatting with the Manager!" ;;
        "success.tell_it.zh") text="    告诉它: \"创建一个名为 alice 的前端开发 Worker\"" ;;
        "success.tell_it.en") text="    Tell it: \"Create a Worker named alice for frontend dev\"" ;;
        "success.manager_auto.zh") text="    Manager 会自动处理一切。" ;;
        "success.manager_auto.en") text="    The Manager will handle everything automatically." ;;
        "success.mobile_title.zh") text="  📱 移动端访问（FluffyChat / Element Mobile）:" ;;
        "success.mobile_title.en") text="  📱 Mobile access (FluffyChat / Element Mobile):" ;;
        "success.mobile_step1.zh") text="    1. 在手机上下载 FluffyChat 或 Element" ;;
        "success.mobile_step1.en") text="    1. Download FluffyChat or Element on your phone" ;;
        "success.mobile_step2.zh") text="    2. 设置 homeserver 为: %s" ;;
        "success.mobile_step2.en") text="    2. Set homeserver to: %s" ;;
        "success.mobile_step2_noip.zh") text="    2. 设置 homeserver 为: http://<本机局域网IP>:%s" ;;
        "success.mobile_step2_noip.en") text="    2. Set homeserver to: http://<this-machine-LAN-IP>:%s" ;;
        "success.mobile_noip_hint.zh") text="       （无法自动检测局域网 IP — 请使用 ifconfig / ip addr 查看）" ;;
        "success.mobile_noip_hint.en") text="       (Could not detect LAN IP automatically — check with: ifconfig / ip addr)" ;;
        "success.mobile_step3.zh") text="    3. 登录信息:" ;;
        "success.mobile_step3.en") text="    3. Login with:" ;;
        "success.mobile_username.zh") text="         用户名: %s" ;;
        "success.mobile_username.en") text="         Username: %s" ;;
        "success.mobile_password.zh") text="         密码: %s" ;;
        "success.mobile_password.en") text="         Password: %s" ;;
        # --- Other consoles and tips ---
        "success.other_consoles.zh") text="--- 其他控制台 ---" ;;
        "success.other_consoles.en") text="--- Other Consoles ---" ;;
        "success.higress_console.zh") text="  Higress 控制台: http://localhost:%s（用户名: %s / 密码: %s）" ;;
        "success.higress_console.en") text="  Higress Console: http://localhost:%s (Username: %s / Password: %s)" ;;
        "success.switch_llm.title.zh") text="--- 切换 LLM 提供商 ---" ;;
        "success.switch_llm.title.en") text="--- Switch LLM Providers ---" ;;
        "success.switch_llm.hint.zh") text="  您可以通过 Higress 控制台切换到其他 LLM 提供商（OpenAI、Anthropic 等）。" ;;
        "success.switch_llm.hint.en") text="  You can switch to other LLM providers (OpenAI, Anthropic, etc.) via Higress Console." ;;
        "success.switch_llm.docs.zh") text="  详细说明请参阅:" ;;
        "success.switch_llm.docs.en") text="  For detailed instructions, see:" ;;
        "success.switch_llm.url.zh") text="  https://higress.ai/en/docs/ai/scene-guide/multi-proxy#console-configuration" ;;
        "success.switch_llm.url.en") text="  https://higress.ai/en/docs/ai/scene-guide/multi-proxy#console-configuration" ;;
        "success.tip.zh") text="提示: 您也可以在聊天中让 Manager 为您配置 LLM 提供商。" ;;
        "success.tip.en") text="Tip: You can also ask the Manager to configure LLM providers for you in the chat." ;;
        "success.config_file.zh") text="配置文件: %s" ;;
        "success.config_file.en") text="Configuration file: %s" ;;
        "success.data_volume.zh") text="数据卷:        %s" ;;
        "success.data_volume.en") text="Data volume:        %s" ;;
        "success.workspace.zh") text="Manager 工作空间:  %s" ;;
        "success.workspace.en") text="Manager workspace:  %s" ;;
        # --- Worker installation ---
        "worker.resetting.zh") text="正在重置 Worker: %s..." ;;
        "worker.resetting.en") text="Resetting Worker: %s..." ;;
        "worker.exists.zh") text="容器 '%s' 已存在。使用 --reset 重新创建。" ;;
        "worker.exists.en") text="Container '%s' already exists. Use --reset to recreate." ;;
        "worker.starting.zh") text="正在启动 Worker: %s..." ;;
        "worker.starting.en") text="Starting Worker: %s..." ;;
        "worker.skills_url.zh") text="  Skills API URL: %s" ;;
        "worker.skills_url.en") text="  Skills API URL: %s" ;;
        "worker.started.zh") text="=== Worker %s 已启动！===" ;;
        "worker.started.en") text="=== Worker %s Started! ===" ;;
        "worker.container.zh") text="容器: %s" ;;
        "worker.container.en") text="Container: %s" ;;
        "worker.view_logs.zh") text="查看日志: docker logs -f %s" ;;
        "worker.view_logs.en") text="View logs: docker logs -f %s" ;;
        # --- Prompt function messages ---
        "prompt.preset.zh") text="  %s = （已通过环境变量预设）" ;;
        "prompt.preset.en") text="  %s = (pre-set via env)" ;;
        "prompt.default.zh") text="  %s = %s（默认）" ;;
        "prompt.default.en") text="  %s = %s (default)" ;;
        "prompt.required.zh") text="%s 是必需的（在非交互模式下通过环境变量设置）" ;;
        "prompt.required.en") text="%s is required (set via environment variable in non-interactive mode)" ;;
        "prompt.required_empty.zh") text="%s 是必需的" ;;
        "prompt.required_empty.en") text="%s is required" ;;
        # --- Language switch prompt (bilingual by design) ---
        "lang.detected.zh") text="检测到语言 / Detected language: 中文" ;;
        "lang.detected.en") text="检测到语言 / Detected language: English" ;;
        "lang.switch_title.zh") text="切换语言 / Switch language:" ;;
        "lang.switch_title.en") text="切换语言 / Switch language:" ;;
        "lang.option_zh.zh") text="  1) 中文" ;;
        "lang.option_zh.en") text="  1) 中文" ;;
        "lang.option_en.zh") text="  2) English" ;;
        "lang.option_en.en") text="  2) English" ;;
        "lang.prompt.zh") text="请选择 / Enter choice" ;;
        "lang.prompt.en") text="请选择 / Enter choice" ;;
        # --- Error messages ---
        "error.name_required.zh") text="--name 是必需的" ;;
        "error.name_required.en") text="--name is required" ;;
        "error.fs_required.zh") text="--fs 是必需的" ;;
        "error.fs_required.en") text="--fs is required" ;;
        "error.fs_key_required.zh") text="--fs-key 是必需的" ;;
        "error.fs_key_required.en") text="--fs-key is required" ;;
        "error.fs_secret_required.zh") text="--fs-secret 是必需的" ;;
        "error.fs_secret_required.en") text="--fs-secret is required" ;;
        "error.unknown_option.zh") text="未知选项: %s" ;;
        "error.unknown_option.en") text="Unknown option: %s" ;;
        "error.docker_not_found.zh") text="未找到 docker 或 podman 命令。请先安装 Docker Desktop 或 Podman Desktop：\n  Docker Desktop: https://www.docker.com/products/docker-desktop/\n  Podman Desktop: https://podman-desktop.io/" ;;
        "error.docker_not_found.en") text="docker or podman command not found. Please install Docker Desktop or Podman Desktop first:\n  Docker Desktop: https://www.docker.com/products/docker-desktop/\n  Podman Desktop: https://podman-desktop.io/" ;;
        "error.docker_not_running.zh") text="Docker 未运行。请先启动 Docker Desktop 或 Podman Desktop。" ;;
        "error.docker_not_running.en") text="Docker is not running. Please start Docker Desktop or Podman Desktop first." ;;
        # --- API Key validation retry ---
        "llm.apikey.retry_prompt.zh") text="请重新输入 API Key（或按 Ctrl+C 取消）：" ;;
        "llm.apikey.retry_prompt.en") text="Please re-enter API Key (or press Ctrl+C to cancel): " ;;
        "llm.apikey.max_retries.zh") text="已达到最大重试次数。" ;;
        "llm.apikey.max_retries.en") text="Maximum retries reached." ;;
        "llm.apikey.invalid.zh") text="❌ API Key 验证失败" ;;
        "llm.apikey.invalid.en") text="❌ API Key validation failed" ;;
        # --- API errors with solutions ---
        "llm.error.401.zh") text="❌ API Key 无效或已过期" ;;
        "llm.error.401.en") text="❌ API Key is invalid or expired" ;;
        "llm.error.401.hint.zh") text="💡 解决方案：请检查您的 API Key 是否正确。您可以在服务商控制台重新生成。" ;;
        "llm.error.401.hint.en") text="💡 Solution: Please verify your API Key is correct. You can regenerate it in your provider's console." ;;
        "llm.error.404.zh") text="❌ API 端点不存在或服务未开通" ;;
        "llm.error.404.en") text="❌ API endpoint not found or service not enabled" ;;
        "llm.error.404.hint.zh") text="💡 解决方案：
        # 在 msg 函数的 case 语句中添加（大约在第 140 行附近）
        "config.show.title.zh") text="=== HiClaw LLM 配置 ===" ;;
        "config.show.title.en") text="=== HiClaw LLM Configuration ===" ;;
        "config.show.provider.zh") text="  提供商: %s" ;;
        "config.show.provider.en") text="  Provider: %s" ;;
        "config.show.model.zh") text="  模型: %s" ;;
        "config.show.model.en") text="  Model: %s" ;;
        "config.show.base_url.zh") text="  Base URL: %s" ;;
        "config.show.base_url.en") text="  Base URL: %s" ;;
        "config.show.api_key_set.zh") text="已设置" ;;
        "config.show.api_key_set.en") text="Set" ;;
        "config.show.api_key_unset.zh") text="未设置" ;;
        "config.show.api_key_unset.en") text="Not set" ;;
        "config.error.no_api_key.zh") text="LLM API Key 未设置" ;;
        "config.error.no_api_key.en") text="LLM API Key not set" ;;
        "config.help.usage.zh") text="用法: %s config <命令>" ;;
        "config.help.usage.en") text="Usage: %s config <command>" ;;
        "config.help.commands.zh") text="命令:" ;;
        "config.help.commands.en") text="Commands:" ;;
        "config.help.fix_llm.zh") text="  --fix-llm    修复 LLM 配置（交互式）" ;;
        "config.help.fix_llm.en") text="  --fix-llm    Fix LLM config (interactive)" ;;
        "config.help.show.zh") text="  --show       显示当前配置" ;;
        "config.help.show.en") text="  --show       Show current config" ;;
        "config.help.verify.zh") text="  --verify     验证配置有效性" ;;
        "config.help.verify.en") text="  --verify     Verify config validity" ;;
        "config.help.help.zh") text="  --help       显示帮助信息" ;;
        "config.help.help.en") text="  --help       Show this help" ;;
        "config.help.examples.zh") text="示例:" ;;
        "config.help.examples.en") text="Examples:" ;;
        "config.help.example_fix.zh") text="  %s config --fix-llm    # 修复 API Key 配置" ;;
        "config.help.example_fix.en") text="  %s config --fix-llm    # Fix API Key config" ;;
        "config.help.example_verify.zh") text="  %s config --verify     # 验证当前配置" ;;
        "config.help.example_verify.en") text="  %s config --verify     # Verify current config" ;;

  1. 确认 API Key 已开通相应服务（如百炼 CodingPlan）
  2. 如使用自定义 Base URL，请确认地址正确
  3. CodingPlan 用户请确保已开通: https://www.aliyun.com/benefit/scene/codingplan" ;;
        "llm.error.404.hint.en") text="💡 Solution:
  1. Confirm your API Key has the required service enabled (e.g., Bailian CodingPlan)
  2. If using custom Base URL, verify the address is correct
  3. CodingPlan users: ensure service is enabled at https://www.aliyun.com/benefit/scene/codingplan" ;;
        "llm.error.403.zh") text="❌ 访问被拒绝（可能是网络或权限问题）" ;;
        "llm.error.403.en") text="❌ Access denied (network or permission issue)" ;;
        "llm.error.403.hint.zh") text="💡 解决方案：
  1. 检查网络是否可访问 API 服务
  2. 确认 API Key 有相应权限
  3. 如在国内使用 OpenAI，可能需要代理" ;;
        "llm.error.403.hint.en") text="💡 Solution:
  1. Check if network can reach the API service
  2. Confirm API Key has required permissions
  3. If using OpenAI in China, you may need a proxy" ;;
        "llm.error.generic.zh") text="❌ API 调用失败 (HTTP %s)" ;;
        "llm.error.generic.en") text="❌ API call failed (HTTP %s)" ;;
        # --- Retry menu ---
        "llm.retry.menu.title.zh") text="请选择下一步操作：" ;;
        "llm.retry.menu.title.en") text="Choose next action:" ;;
        "llm.retry.menu.retry.zh") text="重新输入 API Key" ;;
        "llm.retry.menu.retry.en") text="Re-enter API Key" ;;
        "llm.retry.menu.skip.zh") text="跳过验证，继续安装（不推荐）" ;;
        "llm.retry.menu.skip.en") text="Skip validation, continue install (not recommended)" ;;
        "llm.retry.menu.abort.zh") text="中止安装" ;;
        "llm.retry.menu.abort.en") text="Abort installation" ;;
        "llm.retry.menu.prompt.zh") text="请选择 [1/2/3]" ;;
        "llm.retry.menu.prompt.en") text="Enter choice [1/2/3]" ;;
        # --- Validation skipped ---
        "llm.validation.skipped.zh") text="已跳过 API Key 验证" ;;
        "llm.validation.skipped.en") text="API Key validation skipped" ;;
        "llm.validation.skipped.warning.zh") text="⚠️  警告：Agent 可能无法正常工作。如需修复，请运行: hiclaw config --fix-llm" ;;
        "llm.validation.skipped.warning.en") text="⚠️  Warning: Agent may not work correctly. To fix, run: hiclaw config --fix-llm" ;;
        # --- Config fix command ---
        "config.fix.all.title.zh") text="--- 修复所有配置 ---" ;;
        "config.fix.all.title.en") text="--- Fix All Configuration ---" ;;
        "config.fix.llm.title.zh") text="--- 修复 LLM 配置 ---" ;;
        "config.fix.llm.title.en") text="--- Fix LLM Configuration ---" ;;
        "config.fix.success.zh") text="✅ 配置已更新并保存" ;;
        "config.fix.success.en") text="✅ Configuration updated and saved" ;;
        "config.fix.restart_required.zh") text="⚠️  需要重启 Manager 使配置生效: docker restart hiclaw-manager" ;;
        "config.fix.restart_required.en") text="⚠️  Restart Manager to apply changes: docker restart hiclaw-manager" ;;
        "config.current.provider.zh") text="当前提供商: %s" ;;
        "config.current.provider.en") text="Current provider: %s" ;;
        "config.current.model.zh") text="当前模型: %s" ;;
        "config.current.model.en") text="Current model: %s" ;;
        "backup.created.zh") text="备份已创建：%s" ;;
        "backup.created.en") text="Backup created: %s" ;;
        # --- Fallback: try English for unknown lang ---
        *)
            case "${key}.en" in
                "tz.warning.title.en") text="Could not detect timezone automatically." ;;
                "install.title.en") text="=== HiClaw Manager Installation ===" ;;
                *) text="${key}" ;;
            esac
            ;;
    esac
    if [ $# -gt 0 ]; then
        # shellcheck disable=SC2059
        printf "${text}\n" "$@"
    else
        echo "${text}"
    fi
}

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

    log "$(msg install.wait_ready "${timeout}")"

    # Wait for OpenClaw gateway to be healthy inside the container
    while [ "${elapsed}" -lt "${timeout}" ]; do
        if ${DOCKER_CMD} exec "${container}" openclaw gateway health --json 2>/dev/null | grep -q '"ok"' 2>/dev/null; then
            log "$(msg install.wait_ready.ok)"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
        printf "\r\033[36m[HiClaw]\033[0m $(msg install.wait_ready.waiting "${elapsed}" "${timeout}")"
    done

    echo ""
    error "$(msg install.wait_ready.timeout "${timeout}" "${container}")"
}

wait_matrix_ready() {
    local timeout="${HICLAW_READY_TIMEOUT:-300}"
    local elapsed=0
    local container="${1:-hiclaw-manager}"

    log "$(msg install.wait_matrix "${timeout}")"

    while [ "${elapsed}" -lt "${timeout}" ]; do
        if ${DOCKER_CMD} exec "${container}" curl -sf http://127.0.0.1:6167/_tuwunel/server_version >/dev/null 2>&1; then
            log "$(msg install.wait_matrix.ok)"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
        printf "\r\033[36m[HiClaw]\033[0m $(msg install.wait_matrix.waiting "${elapsed}" "${timeout}")"
    done

    echo ""
    error "$(msg install.wait_matrix.timeout "${timeout}" "${container}")"
}

# ============================================================
# Send welcome message to Manager
# ============================================================

send_welcome_message() {
    local container="hiclaw-manager"

    # Skip if Manager has already completed soul configuration
    if ${DOCKER_CMD} exec "${container}" test -f /root/manager-workspace/soul-configured 2>/dev/null; then
        log "$(msg install.welcome_msg.soul_configured)"
        return 0
    fi

    local admin_user="${HICLAW_ADMIN_USER:-admin}"
    local admin_password="${HICLAW_ADMIN_PASSWORD}"
    local matrix_domain="${HICLAW_MATRIX_DOMAIN}"
    local language="${HICLAW_LANGUAGE}"
    local timezone="${HICLAW_TIMEZONE}"

    # Helper: run curl inside the manager container to reach Matrix directly
    mcurl() { ${DOCKER_CMD} exec "${container}" curl "$@"; }

    # Login to get admin access token
    log "$(msg install.welcome_msg.logging_in "${admin_user}")"

    # Run all Matrix API calls and jq parsing inside the container (jq is only available there).
    # Pass language/timezone via env vars to avoid special-character injection into the script body.
    local inner_script
    inner_script=$(cat <<'INNER_SCRIPT'
MATRIX_URL="http://127.0.0.1:6167"
MANAGER_FULL_ID="@manager:${MATRIX_DOMAIN}"

login_resp=$(curl -sf -X POST "${MATRIX_URL}/_matrix/client/v3/login" \
    -H 'Content-Type: application/json' \
    -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"${ADMIN_USER}\"},\"password\":\"${ADMIN_PASSWORD}\"}" 2>/dev/null) || true
access_token=$(echo "${login_resp}" | jq -r '.access_token // empty' 2>/dev/null)
if [ -z "${access_token}" ]; then
    echo "LOGIN_FAILED: ${login_resp}" >&2; echo "LOGIN_FAILED"; exit 0
fi

room_id=""
rooms=$(curl -sf "${MATRIX_URL}/_matrix/client/v3/joined_rooms" \
    -H "Authorization: Bearer ${access_token}" 2>/dev/null | jq -r '.joined_rooms[]' 2>/dev/null) || true
for rid in ${rooms}; do
    members=$(curl -sf "${MATRIX_URL}/_matrix/client/v3/rooms/${rid}/members" \
        -H "Authorization: Bearer ${access_token}" 2>/dev/null | jq -r '.chunk[].state_key' 2>/dev/null) || continue
    member_count=$(echo "${members}" | wc -l | xargs)
    if [ "${member_count}" = "2" ] && echo "${members}" | grep -q "@manager:"; then
        room_id="${rid}"; break
    fi
done

if [ -z "${room_id}" ]; then
    create_resp=$(curl -sf -X POST "${MATRIX_URL}/_matrix/client/v3/createRoom" \
        -H "Authorization: Bearer ${access_token}" \
        -H 'Content-Type: application/json' \
        -d "{\"is_direct\":true,\"invite\":[\"${MANAGER_FULL_ID}\"],\"preset\":\"trusted_private_chat\"}" 2>/dev/null) || true
    room_id=$(echo "${create_resp}" | jq -r '.room_id // empty' 2>/dev/null)
fi
[ -z "${room_id}" ] && { echo "NO_ROOM" >&2; echo "NO_ROOM"; exit 0; }

# Wait for Manager to join; bail out if it never does (avoids 403 on send)
manager_joined=false
wait_elapsed=0
while [ "${wait_elapsed}" -lt 60 ]; do
    members=$(curl -sf "${MATRIX_URL}/_matrix/client/v3/rooms/${room_id}/members" \
        -H "Authorization: Bearer ${access_token}" 2>/dev/null | jq -r '.chunk[].state_key' 2>/dev/null) || true
    if echo "${members}" | grep -q "${MANAGER_FULL_ID}"; then
        manager_joined=true; break
    fi
    sleep 2; wait_elapsed=$((wait_elapsed + 2))
done
if [ "${manager_joined}" != "true" ]; then
    echo "NO_ROOM" >&2; echo "NO_ROOM"; exit 0
fi

# HICLAW_LANGUAGE and HICLAW_TIMEZONE are passed in via -e flags; use them directly
welcome_msg="This is an automated message from the HiClaw installation script. This is a fresh installation.

--- Installation Context ---
User Language: ${HICLAW_LANGUAGE}  (zh = Chinese, en = English)
User Timezone: ${HICLAW_TIMEZONE}  (IANA timezone identifier)
---

You are an AI agent that manages a team of worker agents. Your identity and personality have not been configured yet — the human admin is about to meet you for the first time.

Please begin the onboarding conversation:

1. Greet the admin warmly and briefly describe what you can do (coordinate workers, manage tasks, run multi-agent projects) — without referring to yourself by any specific title yet
2. The user has selected \"${HICLAW_LANGUAGE}\" as their preferred language during installation. Use this language for your greeting and all subsequent communication.
3. The user's timezone is ${HICLAW_TIMEZONE}. Based on this timezone, you may infer their likely region and suggest additional language options (e.g., Japanese, Korean, German, etc.) that they might prefer for future interactions.
4. Ask them the following questions (one message is fine):
   a. What would they like to call you? (name or title)
   b. What communication style do they prefer? (e.g. formal, casual, concise, detailed)
   c. Any specific behavior guidelines or constraints they want you to follow?
   d. Confirm the default language they want you to use (offer alternatives based on timezone)
5. After they reply, write their preferences to the \"Identity & Personality\" section of ~/SOUL.md — replace the \"(not yet configured)\" placeholder with the configured identity
6. Confirm what you wrote, and ask if they would like to adjust anything
7. Once the admin confirms the identity is set, run: touch ~/soul-configured

The human admin will start chatting shortly."

txn_id="welcome-$(date +%s)"
payload=$(jq -nc --arg body "${welcome_msg}" '{"msgtype":"m.text","body":$body}')
curl -sf -X PUT "${MATRIX_URL}/_matrix/client/v3/rooms/${room_id}/send/m.room.message/${txn_id}" \
    -H "Authorization: Bearer ${access_token}" \
    -H 'Content-Type: application/json' \
    -d "${payload}" > /dev/null 2>&1 || { echo "SEND_FAILED" >&2; echo "SEND_FAILED"; exit 0; }
echo "OK"
INNER_SCRIPT
)

    local result
    # Pass credentials and language/timezone as env vars (-e) so they never touch the script body.
    # Use ${DOCKER_CMD} consistently (supports both docker and podman).
    result=$(${DOCKER_CMD} exec \
        -e ADMIN_USER="${admin_user}" \
        -e ADMIN_PASSWORD="${admin_password}" \
        -e MATRIX_DOMAIN="${matrix_domain}" \
        -e HICLAW_LANGUAGE="${language}" \
        -e HICLAW_TIMEZONE="${timezone}" \
        "${container}" bash -c "${inner_script}")

    case "${result}" in
        *LOGIN_FAILED*)
            log "$(msg install.welcome_msg.login_failed "${admin_user}")"
            return 1 ;;
        *NO_ROOM*)
            log "$(msg install.welcome_msg.no_room)"
            return 1 ;;
        *SEND_FAILED*)
            log "$(msg install.welcome_msg.send_failed)"
            return 1 ;;
        *OK*)
            log "$(msg install.welcome_msg.sent)"
            return 0 ;;
        *)
            log "WARNING: send_welcome_message got unexpected result: ${result}"
            log "$(msg install.welcome_msg.send_failed)"
            return 1 ;;
    esac
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
    eval "local current_value=\"\${${var_name}}\""
    if [ -n "${current_value}" ]; then
        log "$(msg prompt.preset "${var_name}")"
        return
    fi

    # Non-interactive or quickstart: use default or error
    if [ "${HICLAW_NON_INTERACTIVE}" = "1" ] || [ "${HICLAW_QUICKSTART}" = "1" ]; then
        if [ -n "${default_value}" ]; then
            eval "export ${var_name}='${default_value}'"
            log "$(msg prompt.default "${var_name}" "${default_value}")"
            return
        elif [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
            # Only hard-error in fully non-interactive mode, not quickstart
            error "$(msg prompt.required "${var_name}")"
        fi
        # quickstart + no default: fall through to interactive prompt below
    fi

    if [ -n "${default_value}" ]; then
        prompt_text="${prompt_text} [${default_value}]"
    fi

    local value=""
    if [ "${is_secret}" = "true" ]; then
        read -s -p "${prompt_text}: " value
        echo
    else
        read -p "${prompt_text}: " value
    fi

    value="${value:-${default_value}}"
    if [ -z "${value}" ]; then
        error "$(msg prompt.required_empty "${var_name}")"
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
    eval "local _chk=\"\${${var_name}+x}\""
    if [ -n "${_chk}" ]; then
        log "$(msg prompt.preset "${var_name}")"
        return
    fi

    # Non-interactive or quickstart: skip, leave unset
    if [ "${HICLAW_NON_INTERACTIVE}" = "1" ] || [ "${HICLAW_QUICKSTART}" = "1" ]; then
        eval "export ${var_name}=''"
        return
    fi

    local value=""
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

# Detect local LAN IP address (cross-platform: macOS and Linux)
detect_lan_ip() {
    local ip=""

    # macOS: try common Wi-Fi / Ethernet interfaces
    if command -v ipconfig >/dev/null 2>&1; then
        for iface in en0 en1 en2 en3 en4; do
            ip=$(ipconfig getifaddr "${iface}" 2>/dev/null)
            if [ -n "${ip}" ]; then
                echo "${ip}"
                return 0
            fi
        done
    fi

    # Linux: ip route — most reliable
    if command -v ip >/dev/null 2>&1; then
        ip=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
        if [ -n "${ip}" ]; then
            echo "${ip}"
            return 0
        fi
    fi

    # Linux fallback: hostname -I (space-separated list, take first non-loopback)
    if command -v hostname >/dev/null 2>&1; then
        ip=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -v '^127\.' | grep -v '^::' | head -1)
        if [ -n "${ip}" ]; then
            echo "${ip}"
            return 0
        fi
    fi

    # Last resort: ifconfig
    if command -v ifconfig >/dev/null 2>&1; then
        ip=$(ifconfig 2>/dev/null | awk '/inet /{if($2!~/^127\./){print $2; exit}}')
        # Strip "addr:" prefix that some ifconfig versions add
        ip="${ip#addr:}"
        if [ -n "${ip}" ]; then
            echo "${ip}"
            return 0
        fi
    fi

    echo ""
}

# ============================================================
# Manager Installation (Interactive)
# ============================================================

install_manager() {
    log "$(msg install.title)"
    log "$(msg install.registry "${HICLAW_REGISTRY}")"
    log ""
    log "$(msg install.dir "$(pwd)")"
    log "$(msg install.dir_hint)"
    log "$(msg install.dir_hint2)"
    log ""

    # Language switch interaction (skip in non-interactive mode)
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
        # Determine default choice based on current detected language
        local lang_default_choice="2"
        if [ "${HICLAW_LANGUAGE}" = "zh" ]; then
            lang_default_choice="1"
        fi

        log "$(msg lang.detected)"
        log "$(msg lang.switch_title)"
        echo "$(msg lang.option_zh)"
        echo "$(msg lang.option_en)"
        echo ""
        read -p "$(msg lang.prompt) [${lang_default_choice}]: " LANG_CHOICE
        LANG_CHOICE="${LANG_CHOICE:-${lang_default_choice}}"

        case "${LANG_CHOICE}" in
            1)
                HICLAW_LANGUAGE="zh"
                ;;
            2)
                HICLAW_LANGUAGE="en"
                ;;
            *)
                # Invalid input — keep current detected language
                ;;
        esac
        export HICLAW_LANGUAGE
        log ""
    fi

    # Onboarding mode selection (skip if already in non-interactive mode)
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
        log "$(msg install.mode.title)"
        echo ""
        echo "$(msg install.mode.choose)"
        echo "$(msg install.mode.quickstart)"
        echo "$(msg install.mode.manual)"
        echo ""
        read -p "$(msg install.mode.prompt): " ONBOARDING_CHOICE
        ONBOARDING_CHOICE="${ONBOARDING_CHOICE:-1}"

        case "${ONBOARDING_CHOICE}" in
            1|quick|quickstart)
                log "$(msg install.mode.quickstart_selected)"
                HICLAW_QUICKSTART=1
                ;;
            2|manual)
                log "$(msg install.mode.manual_selected)"
                ;;
            *)
                log "$(msg install.mode.invalid)"
                HICLAW_QUICKSTART=1
                ;;
        esac
        log ""
    fi

    # Check if Manager is already installed (by env file existence)
    local existing_env="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
    # Migrate from legacy location (current directory) if needed
    if [ ! -f "${existing_env}" ] && [ -f "./hiclaw-manager.env" ]; then
        log "Migrating hiclaw-manager.env from current directory to ${existing_env}..."
        mv "./hiclaw-manager.env" "${existing_env}"
    fi
    if [ -f "${existing_env}" ]; then
        log "$(msg install.existing.detected "${existing_env}")"

        # Check for running containers
        local running_manager=""
        local running_workers=""
        local existing_workers=""
        if ${DOCKER_CMD} ps --format '{{.Names}}' | grep -q "^hiclaw-manager$"; then
            running_manager="hiclaw-manager"
        fi
        running_workers=$(${DOCKER_CMD} ps --format '{{.Names}}' | grep "^hiclaw-worker-" || true)
        existing_workers=$(${DOCKER_CMD} ps -a --format '{{.Names}}' | grep "^hiclaw-worker-" || true)

        # Non-interactive mode: default to upgrade without rebuilding workers
        if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
            log "$(msg install.existing.upgrade_noninteractive)"
            UPGRADE_CHOICE="upgrade"
            REBUILD_WORKERS="no"
        else
            echo ""
            echo "$(msg install.existing.choose)"
            echo "$(msg install.existing.upgrade)"
            echo "$(msg install.existing.reinstall)"
            echo "$(msg install.existing.cancel)"
            echo ""
            read -p "$(msg install.existing.prompt): " UPGRADE_CHOICE
            UPGRADE_CHOICE="${UPGRADE_CHOICE:-1}"
        fi

        case "${UPGRADE_CHOICE}" in
            1|upgrade)
                log "$(msg install.existing.upgrading)"

                # Warn about running containers
                if [ -n "${running_manager}" ] || [ -n "${running_workers}" ]; then
                    echo ""
                    echo -e "\033[33m$(msg install.existing.warn_manager_stop)\033[0m"
                    if [ -n "${existing_workers}" ]; then
                        echo -e "\033[33m$(msg install.existing.warn_worker_recreate)\033[0m"
                    fi
                    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
                        echo ""
                        read -p "$(msg install.existing.continue_prompt): " CONFIRM_STOP
                        if [ "${CONFIRM_STOP}" != "y" ] && [ "${CONFIRM_STOP}" != "Y" ]; then
                            log "$(msg install.existing.cancelled)"
                            exit 0
                        fi
                    fi
                fi

                # Stop and remove manager container
                if [ -n "${running_manager}" ] || ${DOCKER_CMD} ps -a --format '{{.Names}}' | grep -q "^hiclaw-manager$"; then
                    log "$(msg install.existing.stopping_manager)"
                    ${DOCKER_CMD} stop hiclaw-manager 2>/dev/null || true
                    ${DOCKER_CMD} rm hiclaw-manager 2>/dev/null || true
                fi

                # Stop and remove worker containers (Manager IP changes on restart,
                # so workers must be recreated to get updated /etc/hosts entries)
                if [ -n "${existing_workers}" ]; then
                    log "$(msg install.existing.stopping_workers)"
                    for w in ${existing_workers}; do
                        ${DOCKER_CMD} stop "${w}" 2>/dev/null || true
                        ${DOCKER_CMD} rm "${w}" 2>/dev/null || true
                        log "$(msg install.existing.removed "${w}")"
                    done
                fi
                # Continue with installation using existing config
                ;;
            2|reinstall)
                log "$(msg install.reinstall.performing)"

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
                echo -e "\033[33m$(msg install.reinstall.warn_stop)\033[0m"
                [ -n "${running_manager}" ] && echo -e "\033[33m   - ${running_manager} (manager)\033[0m"
                for w in ${running_workers}; do
                    echo -e "\033[33m   - ${w} (worker)\033[0m"
                done
                echo ""
                echo -e "\033[31m$(msg install.reinstall.warn_delete)\033[0m"
                echo -e "\033[31m$(msg install.reinstall.warn_volume)\033[0m"
                echo -e "\033[31m$(msg install.reinstall.warn_env "${existing_env}")\033[0m"
                echo -e "\033[31m$(msg install.reinstall.warn_workspace "${existing_workspace}")\033[0m"
                echo -e "\033[31m$(msg install.reinstall.warn_workers)\033[0m"
                echo ""
                echo -e "\033[31m$(msg install.reinstall.confirm_type)\033[0m"
                echo -e "\033[31m  ${existing_workspace}\033[0m"
                echo ""
                read -p "$(msg install.reinstall.confirm_path): " CONFIRM_PATH

                if [ "${CONFIRM_PATH}" != "${existing_workspace}" ]; then
                    error "$(msg install.reinstall.path_mismatch "${CONFIRM_PATH}" "${existing_workspace}")"
                fi

                log "$(msg install.reinstall.confirmed)"

                # Stop and remove manager container
                ${DOCKER_CMD} stop hiclaw-manager 2>/dev/null || true
                ${DOCKER_CMD} rm hiclaw-manager 2>/dev/null || true

                # Stop and remove all worker containers
                for w in $(${DOCKER_CMD} ps -a --format '{{.Names}}' | grep "^hiclaw-worker-" || true); do
                    ${DOCKER_CMD} stop "${w}" 2>/dev/null || true
                    ${DOCKER_CMD} rm "${w}" 2>/dev/null || true
                    log "$(msg install.reinstall.removed_worker "${w}")"
                done

                # Remove Docker volume
                if ${DOCKER_CMD} volume ls -q | grep -q "^hiclaw-data$"; then
                    log "$(msg install.reinstall.removing_volume)"
                    ${DOCKER_CMD} volume rm hiclaw-data 2>/dev/null || log "$(msg install.reinstall.warn_volume_fail)"
                fi

                # Remove workspace directory
                if [ -d "${existing_workspace}" ]; then
                    log "$(msg install.reinstall.removing_workspace "${existing_workspace}")"
                    rm -rf "${existing_workspace}" || error "$(msg install.reinstall.failed_rm_workspace)"
                fi

                # Remove env file
                if [ -f "${existing_env}" ]; then
                    log "$(msg install.reinstall.removing_env "${existing_env}")"
                    rm -f "${existing_env}"
                fi

                log "$(msg install.reinstall.cleanup_done)"
                # Clear any loaded environment variables to start fresh
                unset HICLAW_WORKSPACE_DIR
                ;;
            3|cancel|*)
                log "$(msg install.existing.cancelled)"
                exit 0
                ;;
        esac
    fi

    # Load existing env file as fallback (shell env vars take priority)
    if [ -f "${existing_env}" ]; then
        log "$(msg install.loading_config "${existing_env}")"
        while IFS='=' read -r key value; do
            # Skip comments and empty lines
            case "${key}" in
                \#*|"") continue ;;
            esac
            # Strip inline comments and surrounding whitespace from value
            value="${value%%#*}"
            value="${value#"${value%%[![:space:]]*}"}"
            value="${value%"${value##*[![:space:]]}"}"
            # Only set if not already set in the shell environment
            eval "_existing_val=\"\${${key}+x}\""
            if [ -z "${_existing_val}" ]; then
                export "${key}=${value}"
            fi
        done < "${existing_env}"
    fi

    # LLM Configuration
    log "$(msg llm.title)"

    if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
        # Non-interactive mode: use defaults
        HICLAW_LLM_PROVIDER="${HICLAW_LLM_PROVIDER:-qwen}"
        HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
        log "$(msg llm.provider.qwen_default "${HICLAW_LLM_PROVIDER}")"
        log "$(msg llm.model.default "${HICLAW_DEFAULT_MODEL}")"
        prompt HICLAW_LLM_API_KEY "$(msg llm.apikey_prompt)" "" "true"
    else
        # Both Quick Start and Manual mode: show provider selection menu
        # Quick Start defaults to option 1 (Alibaba Cloud → CodingPlan); Manual requires explicit choice
        echo ""
        echo "$(msg llm.providers_title)"
        echo "$(msg llm.provider.alibaba)"
        echo "$(msg llm.provider.openai_compat)"
        echo ""
        if [ "${HICLAW_QUICKSTART}" = "1" ]; then
            read -p "$(msg llm.provider.select) [1]: " PROVIDER_CHOICE
            PROVIDER_CHOICE="${PROVIDER_CHOICE:-1}"
        else
            read -p "$(msg llm.provider.select): " PROVIDER_CHOICE
            PROVIDER_CHOICE="${PROVIDER_CHOICE:-1}"
        fi

        case "${PROVIDER_CHOICE}" in
            1|alibaba-cloud)
                # Sub-menu: CodingPlan or qwen general
                echo ""
                echo "$(msg llm.alibaba.models_title)"
                echo "$(msg llm.alibaba.model.codingplan)"
                echo "$(msg llm.alibaba.model.qwen)"
                echo ""
                if [ "${HICLAW_QUICKSTART}" = "1" ]; then
                    read -p "$(msg llm.alibaba.model.select) [1]: " ALIBABA_MODEL_CHOICE
                    ALIBABA_MODEL_CHOICE="${ALIBABA_MODEL_CHOICE:-1}"
                else
                    read -p "$(msg llm.alibaba.model.select): " ALIBABA_MODEL_CHOICE
                    ALIBABA_MODEL_CHOICE="${ALIBABA_MODEL_CHOICE:-1}"
                fi

                case "${ALIBABA_MODEL_CHOICE}" in
                    2|qwen)
                        HICLAW_LLM_PROVIDER="qwen"
                        HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-qwen3.5-plus}"
                        log "$(msg llm.provider.selected_qwen)"
                        log "$(msg llm.model.label "${HICLAW_DEFAULT_MODEL}")"
                        ;;
                    *)
                        HICLAW_LLM_PROVIDER="openai-compat"
                        HICLAW_OPENAI_BASE_URL="${HICLAW_OPENAI_BASE_URL:-https://coding.dashscope.aliyuncs.com/v1}"

                        # Sub-menu: Select CodingPlan model
                        echo ""
                        echo "$(msg llm.codingplan.models_title)"
                        echo "$(msg llm.codingplan.model.qwen35plus)"
                        echo "$(msg llm.codingplan.model.glm5)"
                        echo "$(msg llm.codingplan.model.kimi)"
                        echo "$(msg llm.codingplan.model.minimax)"
                        echo ""
                        if [ "${HICLAW_QUICKSTART}" = "1" ]; then
                            read -p "$(msg llm.codingplan.model.select) [1]: " CODINGPLAN_MODEL_CHOICE
                            CODINGPLAN_MODEL_CHOICE="${CODINGPLAN_MODEL_CHOICE:-1}"
                        else
                            read -p "$(msg llm.codingplan.model.select): " CODINGPLAN_MODEL_CHOICE
                            CODINGPLAN_MODEL_CHOICE="${CODINGPLAN_MODEL_CHOICE:-1}"
                        fi

                        case "${CODINGPLAN_MODEL_CHOICE}" in
                            1|qwen3.5-plus)
                                HICLAW_DEFAULT_MODEL="qwen3.5-plus"
                                ;;
                            2|glm-5)
                                HICLAW_DEFAULT_MODEL="glm-5"
                                ;;
                            3|kimi-k2.5)
                                HICLAW_DEFAULT_MODEL="kimi-k2.5"
                                ;;
                            4|MiniMax-M2.5)
                                HICLAW_DEFAULT_MODEL="MiniMax-M2.5"
                                ;;
                            *)
                                HICLAW_DEFAULT_MODEL="qwen3.5-plus"
                                ;;
                        esac

                        log "$(msg llm.provider.selected_codingplan)"
                        log "$(msg llm.model.label "${HICLAW_DEFAULT_MODEL}")"
                        ;;
                esac
                log ""
                log "$(msg llm.apikey_hint)"
                log "$(msg llm.apikey_url)"
                log ""
                prompt HICLAW_LLM_API_KEY "$(msg llm.apikey_prompt)" "" "true"
                # Connectivity test
                if [ "${ALIBABA_MODEL_CHOICE}" = "2" ] || [ "${ALIBABA_MODEL_CHOICE}" = "qwen" ]; then
                    test_llm_connectivity "https://dashscope.aliyuncs.com/compatible-mode/v1" "${HICLAW_LLM_API_KEY}" "${HICLAW_DEFAULT_MODEL}"
                else
                    test_llm_connectivity "${HICLAW_OPENAI_BASE_URL}" "${HICLAW_LLM_API_KEY}" "${HICLAW_DEFAULT_MODEL}" "$(msg llm.openai.test.fail.codingplan)"
                fi
                ;;
            2|openai-compat)
                HICLAW_LLM_PROVIDER="openai-compat"
                log "$(msg llm.provider.selected_openai "${HICLAW_LLM_PROVIDER}")"
                echo ""
                read -p "$(msg llm.openai.base_url_prompt): " HICLAW_OPENAI_BASE_URL
                read -p "$(msg llm.openai.model_prompt): " HICLAW_DEFAULT_MODEL
                HICLAW_DEFAULT_MODEL="${HICLAW_DEFAULT_MODEL:-gpt-4o}"
                log "$(msg llm.openai.base_url_label "${HICLAW_OPENAI_BASE_URL}")"
                log "$(msg llm.model.label "${HICLAW_DEFAULT_MODEL}")"
                log ""
                prompt HICLAW_LLM_API_KEY "$(msg llm.apikey_prompt)" "" "true"
                test_llm_connectivity "${HICLAW_OPENAI_BASE_URL}" "${HICLAW_LLM_API_KEY}" "${HICLAW_DEFAULT_MODEL}"
                ;;
            *)
                log "$(msg llm.provider.invalid)"
                HICLAW_LLM_PROVIDER="openai-compat"
                HICLAW_OPENAI_BASE_URL="${HICLAW_OPENAI_BASE_URL:-https://coding.dashscope.aliyuncs.com/v1}"

                # Sub-menu: Select CodingPlan model
                echo ""
                echo "$(msg llm.codingplan.models_title)"
                echo "$(msg llm.codingplan.model.qwen35plus)"
                echo "$(msg llm.codingplan.model.glm5)"
                echo "$(msg llm.codingplan.model.kimi)"
                echo "$(msg llm.codingplan.model.minimax)"
                echo ""
                if [ "${HICLAW_QUICKSTART}" = "1" ]; then
                    read -p "$(msg llm.codingplan.model.select) [1]: " CODINGPLAN_MODEL_CHOICE
                    CODINGPLAN_MODEL_CHOICE="${CODINGPLAN_MODEL_CHOICE:-1}"
                else
                    read -p "$(msg llm.codingplan.model.select): " CODINGPLAN_MODEL_CHOICE
                    CODINGPLAN_MODEL_CHOICE="${CODINGPLAN_MODEL_CHOICE:-1}"
                fi

                case "${CODINGPLAN_MODEL_CHOICE}" in
                    1|qwen3.5-plus)
                        HICLAW_DEFAULT_MODEL="qwen3.5-plus"
                        ;;
                    2|glm-5)
                        HICLAW_DEFAULT_MODEL="glm-5"
                        ;;
                    3|kimi-k2.5)
                        HICLAW_DEFAULT_MODEL="kimi-k2.5"
                        ;;
                    4|MiniMax-M2.5)
                        HICLAW_DEFAULT_MODEL="MiniMax-M2.5"
                        ;;
                    *)
                        HICLAW_DEFAULT_MODEL="qwen3.5-plus"
                        ;;
                esac

                log "$(msg llm.provider.selected_codingplan)"
                log "$(msg llm.model.label "${HICLAW_DEFAULT_MODEL}")"
                log ""
                log "$(msg llm.apikey_hint)"
                log "$(msg llm.apikey_url)"
                log ""
                prompt HICLAW_LLM_API_KEY "$(msg llm.apikey_prompt)" "" "true"
                test_llm_connectivity "${HICLAW_OPENAI_BASE_URL}" "${HICLAW_LLM_API_KEY}" "${HICLAW_DEFAULT_MODEL}" "$(msg llm.openai.test.fail.codingplan)"
                ;;
        esac
    fi

    log ""

    # Admin Credentials (password auto-generated if not provided)
    log "$(msg admin.title)"
    prompt HICLAW_ADMIN_USER "$(msg admin.username_prompt)" "admin"
    if [ -z "${HICLAW_ADMIN_PASSWORD}" ]; then
        prompt_optional HICLAW_ADMIN_PASSWORD "$(msg admin.password_prompt)" "true"
        if [ -z "${HICLAW_ADMIN_PASSWORD}" ]; then
            HICLAW_ADMIN_PASSWORD="admin$(openssl rand -hex 6)"
            log "$(msg admin.password_generated)"
        fi
    else
        log "  $(msg prompt.preset "HICLAW_ADMIN_PASSWORD")"
    fi

    # Validate password length (MinIO requires at least 8 characters)
    if [ ${#HICLAW_ADMIN_PASSWORD} -lt 8 ]; then
        error "$(msg admin.password_too_short "${#HICLAW_ADMIN_PASSWORD}")"
    fi

    log ""

    # Port Configuration (must come before Domain so MATRIX_DOMAIN default uses the correct port)
    log "$(msg port.local_only.title)"
    echo ""
    echo "  1) $(msg port.local_only.hint_yes)"
    echo "  2) $(msg port.local_only.hint_no)"
    echo ""
    if [ "${HICLAW_NON_INTERACTIVE}" = "1" ]; then
        HICLAW_LOCAL_ONLY="${HICLAW_LOCAL_ONLY:-1}"
    elif [ -z "${HICLAW_LOCAL_ONLY+x}" ]; then
        read -p "$(msg port.local_only.choice): " _local_choice
        _local_choice="${_local_choice:-1}"
        case "${_local_choice}" in
            2|n|N|no|NO) HICLAW_LOCAL_ONLY="0" ;;
            *)            HICLAW_LOCAL_ONLY="1" ;;
        esac
        unset _local_choice
    fi
    export HICLAW_LOCAL_ONLY

    if [ "${HICLAW_LOCAL_ONLY}" = "1" ]; then
        log "$(msg port.local_only.selected_local)"
    else
        log "$(msg port.local_only.selected_external)"
        echo ""
        echo -e "\033[33m$(msg port.local_only.https_hint)\033[0m"
    fi

    log "$(msg port.title)"
    prompt HICLAW_PORT_GATEWAY "$(msg port.gateway_prompt)" "18080"
    prompt HICLAW_PORT_CONSOLE "$(msg port.console_prompt)" "18001"
    prompt HICLAW_PORT_ELEMENT_WEB "$(msg port.element_prompt)" "18088"

    log ""

    # Domain Configuration
    log "$(msg domain.title)"
    prompt HICLAW_MATRIX_DOMAIN "$(msg domain.matrix_prompt)" "matrix-local.hiclaw.io:${HICLAW_PORT_GATEWAY}"
    prompt HICLAW_MATRIX_CLIENT_DOMAIN "$(msg domain.element_prompt)" "matrix-client-local.hiclaw.io"
    prompt HICLAW_AI_GATEWAY_DOMAIN "$(msg domain.gateway_prompt)" "aigw-local.hiclaw.io"
    prompt HICLAW_FS_DOMAIN "$(msg domain.fs_prompt)" "fs-local.hiclaw.io"

    log ""

    # Optional: GitHub PAT
    log "$(msg github.title)"
    prompt_optional HICLAW_GITHUB_TOKEN "$(msg github.token_prompt)" "true"

    # Optional: Skills Registry URL
    log ""
    log "$(msg skills.title)"
    prompt_optional HICLAW_SKILLS_API_URL "$(msg skills.url_prompt)"

    log ""

    # Data persistence
    log "$(msg data.title)"
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ] && [ "${HICLAW_QUICKSTART}" != "1" ] && [ -z "${HICLAW_DATA_DIR+x}" ]; then
        read -p "$(msg data.volume_prompt): " HICLAW_DATA_DIR
        HICLAW_DATA_DIR="${HICLAW_DATA_DIR:-hiclaw-data}"
        export HICLAW_DATA_DIR
    fi
    HICLAW_DATA_DIR="${HICLAW_DATA_DIR:-hiclaw-data}"
    log "$(msg data.volume_using "${HICLAW_DATA_DIR}")"

    # Manager workspace directory (skills, memory, state — host-editable)
    log "$(msg workspace.title)"
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ] && [ "${HICLAW_QUICKSTART}" != "1" ] && [ -z "${HICLAW_WORKSPACE_DIR+x}" ]; then
        read -p "$(msg workspace.dir_prompt "${HOME}/hiclaw-manager"): " HICLAW_WORKSPACE_DIR
        HICLAW_WORKSPACE_DIR="${HICLAW_WORKSPACE_DIR:-${HOME}/hiclaw-manager}"
        export HICLAW_WORKSPACE_DIR
    elif [ -z "${HICLAW_WORKSPACE_DIR+x}" ]; then
        HICLAW_WORKSPACE_DIR="${HOME}/hiclaw-manager"
        export HICLAW_WORKSPACE_DIR
    fi
    HICLAW_WORKSPACE_DIR="$(cd "${HICLAW_WORKSPACE_DIR}" 2>/dev/null && pwd || echo "${HICLAW_WORKSPACE_DIR}")"
    mkdir -p "${HICLAW_WORKSPACE_DIR}"
    log "$(msg workspace.dir_label "${HICLAW_WORKSPACE_DIR}")"

    log ""

    # Generate secrets (only if not already set)
    log "$(msg install.generating_secrets)"
    HICLAW_MANAGER_PASSWORD="${HICLAW_MANAGER_PASSWORD:-$(generate_key)}"
    HICLAW_REGISTRATION_TOKEN="${HICLAW_REGISTRATION_TOKEN:-$(generate_key)}"
    HICLAW_MINIO_USER="${HICLAW_MINIO_USER:-${HICLAW_ADMIN_USER}}"
    HICLAW_MINIO_PASSWORD="${HICLAW_MINIO_PASSWORD:-${HICLAW_ADMIN_PASSWORD}}"
    HICLAW_MANAGER_GATEWAY_KEY="${HICLAW_MANAGER_GATEWAY_KEY:-$(generate_key)}"

    # Write .env file
    ENV_FILE="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
    cat > "${ENV_FILE}" << EOF
# HiClaw Manager Configuration
# Generated by hiclaw-install.sh on $(date)

# Language
HICLAW_LANGUAGE=${HICLAW_LANGUAGE}

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
HICLAW_PORT_ELEMENT_WEB=${HICLAW_PORT_ELEMENT_WEB}

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

# Skills Registry (optional, default: https://skills.sh)
HICLAW_SKILLS_API_URL=${HICLAW_SKILLS_API_URL:-}

# Worker image (for direct container creation)
HICLAW_WORKER_IMAGE=${WORKER_IMAGE}

# Higress WASM plugin image registry (auto-selected by timezone)
HIGRESS_ADMIN_WASM_PLUGIN_IMAGE_REGISTRY=${HICLAW_REGISTRY}

# Data persistence
HICLAW_DATA_DIR=${HICLAW_DATA_DIR:-hiclaw-data}
# Manager workspace (skills, memory, state — host-editable)
HICLAW_WORKSPACE_DIR=${HICLAW_WORKSPACE_DIR:-}
# Host directory sharing
HICLAW_HOST_SHARE_DIR=${HICLAW_HOST_SHARE_DIR:-}
EOF

    chmod 600 "${ENV_FILE}"
    log "$(msg install.config_saved "${ENV_FILE}")"

    # Detect container runtime socket
    SOCKET_MOUNT_ARGS=""
    if [ "${HICLAW_MOUNT_SOCKET}" = "1" ]; then
        CONTAINER_SOCK=$(detect_socket)
        if [ -n "${CONTAINER_SOCK}" ]; then
            log "$(msg install.socket_detected "${CONTAINER_SOCK}")"
            SOCKET_MOUNT_ARGS="-v ${CONTAINER_SOCK}:/var/run/docker.sock --security-opt label=disable"
        else
            log "$(msg install.socket_not_found)"
        fi
    fi

    # Remove existing container if present
    if ${DOCKER_CMD} ps -a --format '{{.Names}}' | grep -q "^hiclaw-manager$"; then
        log "$(msg install.removing_existing)"
        ${DOCKER_CMD} stop hiclaw-manager 2>/dev/null || true
        ${DOCKER_CMD} rm hiclaw-manager 2>/dev/null || true
    fi

    # Create the data volume if it doesn't already exist (reuse on reinstall)
    if ! ${DOCKER_CMD} volume ls -q | grep -q "^${HICLAW_DATA_DIR}$"; then
        ${DOCKER_CMD} volume create "${HICLAW_DATA_DIR}" > /dev/null
    fi

    # Data mount: Docker volume
    DATA_MOUNT_ARGS="-v ${HICLAW_DATA_DIR}:/data"

    # Manager workspace mount (always a host directory, defaulting to ~/hiclaw-manager)
    WORKSPACE_MOUNT_ARGS="-v ${HICLAW_WORKSPACE_DIR}:/root/manager-workspace"

    # Pass host timezone to container so date/time commands reflect local time
    TZ_ARGS="-e TZ=${HICLAW_TIMEZONE}"

    # Host directory mount: for file sharing with agents (defaults to user's home)
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ] && [ -z "${HICLAW_HOST_SHARE_DIR}" ]; then
        read -p "$(msg host_share.prompt "$HOME"): " HICLAW_HOST_SHARE_DIR
        HICLAW_HOST_SHARE_DIR="${HICLAW_HOST_SHARE_DIR:-$HOME}"
        export HICLAW_HOST_SHARE_DIR
    elif [ -z "${HICLAW_HOST_SHARE_DIR}" ]; then
        HICLAW_HOST_SHARE_DIR="$HOME"
        export HICLAW_HOST_SHARE_DIR
    fi

    if [ -d "${HICLAW_HOST_SHARE_DIR}" ]; then
        HOST_SHARE_MOUNT_ARGS="-v ${HICLAW_HOST_SHARE_DIR}:/host-share"
        log "$(msg host_share.sharing "${HICLAW_HOST_SHARE_DIR}")"
    else
        log "$(msg host_share.not_exist "${HICLAW_HOST_SHARE_DIR}")"
        HOST_SHARE_MOUNT_ARGS="-v ${HICLAW_HOST_SHARE_DIR}:/host-share"
    fi

    # YOLO mode: pass through if set in environment (enables autonomous decisions)
    YOLO_ARGS=""
    if [ "${HICLAW_YOLO:-}" = "1" ]; then
        YOLO_ARGS="-e HICLAW_YOLO=1"
        log "$(msg install.yolo)"
    fi

    # Pull images (worker image must be ready before manager creates workers)
    LOCAL_IMAGE_PREFIX="hiclaw/"
    if echo "${MANAGER_IMAGE}" | grep -q "^${LOCAL_IMAGE_PREFIX}"; then
        if ${DOCKER_CMD} image inspect "${MANAGER_IMAGE}" >/dev/null 2>&1; then
            log "$(msg install.image.exists "${MANAGER_IMAGE}")"
        else
            log "$(msg install.image.pulling_manager "${MANAGER_IMAGE}")"
            ${DOCKER_CMD} pull "${MANAGER_IMAGE}"
        fi
    else
        log "$(msg install.image.pulling_manager "${MANAGER_IMAGE}")"
        ${DOCKER_CMD} pull "${MANAGER_IMAGE}"
    fi
    if echo "${WORKER_IMAGE}" | grep -q "^${LOCAL_IMAGE_PREFIX}"; then
        if ${DOCKER_CMD} image inspect "${WORKER_IMAGE}" >/dev/null 2>&1; then
            log "$(msg install.image.worker_exists "${WORKER_IMAGE}")"
        else
            log "$(msg install.image.pulling_worker "${WORKER_IMAGE}")"
            ${DOCKER_CMD} pull "${WORKER_IMAGE}"
        fi
    else
        log "$(msg install.image.pulling_worker "${WORKER_IMAGE}")"
        ${DOCKER_CMD} pull "${WORKER_IMAGE}"
    fi

    # Run Manager container
    log "$(msg install.starting_manager)"
    # Build port binding args (127.0.0.1 prefix for local-only mode)
    if [ "${HICLAW_LOCAL_ONLY:-1}" = "1" ]; then
        _port_prefix="127.0.0.1:"
    else
        _port_prefix=""
    fi
    # shellcheck disable=SC2086
    ${DOCKER_CMD} run -d \
        --name hiclaw-manager \
        --env-file "${ENV_FILE}" \
        -e HOME=/root/manager-workspace \
        -w /root/manager-workspace \
        -e HOST_ORIGINAL_HOME="${HICLAW_HOST_SHARE_DIR}" \
        ${YOLO_ARGS} \
        ${TZ_ARGS} \
        ${SOCKET_MOUNT_ARGS} \
        -p "${_port_prefix}${HICLAW_PORT_GATEWAY}:8080" \
        -p "${_port_prefix}${HICLAW_PORT_CONSOLE}:8001" \
        -p "${_port_prefix}${HICLAW_PORT_ELEMENT_WEB:-18088}:8088" \
        ${DATA_MOUNT_ARGS} \
        ${WORKSPACE_MOUNT_ARGS} \
        ${HOST_SHARE_MOUNT_ARGS} \
        --restart unless-stopped \
        "${MANAGER_IMAGE}"
    unset _port_prefix

    # Wait for Manager agent to be ready
    wait_manager_ready "hiclaw-manager"

    # Wait for Matrix server to be ready
    wait_matrix_ready "hiclaw-manager"

    # Send welcome message to Manager (skipped automatically if soul-configured marker exists)
    send_welcome_message

    log ""
    log "$(msg success.title)"
    log ""
    log "$(msg success.domains_configured)"
    log "  ${HICLAW_MATRIX_DOMAIN%%:*} ${HICLAW_MATRIX_CLIENT_DOMAIN} ${HICLAW_AI_GATEWAY_DOMAIN} ${HICLAW_FS_DOMAIN}"
    log ""
    local lan_ip
    lan_ip=$(detect_lan_ip)
    echo -e "\033[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
    echo -e "\033[33m  $(msg success.open_url)\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[1;36m    http://127.0.0.1:${HICLAW_PORT_ELEMENT_WEB:-18088}/#/login\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[33m  $(msg success.login_with)\033[0m"
    echo -e "\033[33m    $(msg success.username "${HICLAW_ADMIN_USER}")\033[0m"
    echo -e "\033[33m    $(msg success.password "${HICLAW_ADMIN_PASSWORD}")\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[33m  $(msg success.after_login)\033[0m"
    echo -e "\033[33m    $(msg success.tell_it)\033[0m"
    echo -e "\033[33m    $(msg success.manager_auto)\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[33m  ─────────────────────────────────────────────────────────────────────────────  \033[0m"
    echo -e "\033[33m  $(msg success.mobile_title)\033[0m"
    echo -e "\033[33m                                                                                 \033[0m"
    if [ -n "${lan_ip}" ]; then
        echo -e "\033[33m    $(msg success.mobile_step1)\033[0m"
        echo -e "\033[33m    $(msg success.mobile_step2 "http://${lan_ip}:${HICLAW_PORT_GATEWAY}")\033[0m"
        echo -e "\033[33m    $(msg success.mobile_step3)\033[0m"
        echo -e "\033[33m         $(msg success.mobile_username "${HICLAW_ADMIN_USER}")\033[0m"
        echo -e "\033[33m         $(msg success.mobile_password "${HICLAW_ADMIN_PASSWORD}")\033[0m"
    else
        echo -e "\033[33m    $(msg success.mobile_step1)\033[0m"
        echo -e "\033[33m    $(msg success.mobile_step2_noip "${HICLAW_PORT_GATEWAY}")\033[0m"
        echo -e "\033[33m    $(msg success.mobile_noip_hint)\033[0m"
        echo -e "\033[33m    $(msg success.mobile_step3)\033[0m"
        echo -e "\033[33m         $(msg success.mobile_username "${HICLAW_ADMIN_USER}")\033[0m"
        echo -e "\033[33m         $(msg success.mobile_password "${HICLAW_ADMIN_PASSWORD}")\033[0m"
    fi
    echo -e "\033[33m                                                                                 \033[0m"
    echo -e "\033[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
    log ""
    log "$(msg success.other_consoles)"
    log "$(msg success.higress_console "${HICLAW_PORT_CONSOLE}" "${HICLAW_ADMIN_USER}" "${HICLAW_ADMIN_PASSWORD}")"
    log ""
    log "$(msg success.switch_llm.title)"
    log "$(msg success.switch_llm.hint)"
    log "$(msg success.switch_llm.docs)"
    log "$(msg success.switch_llm.url)"
    log ""
    log "$(msg success.tip)"
    log ""
    if [ "${HICLAW_LOCAL_ONLY:-1}" != "1" ]; then
        echo -e "\033[33m$(msg port.local_only.https_hint)\033[0m"
        log ""
    fi
    log "$(msg success.config_file "${ENV_FILE}")"
    log "$(msg success.data_volume "${HICLAW_DATA_DIR}")"
    log "$(msg success.workspace "${HICLAW_WORKSPACE_DIR}")"
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
    local ENABLE_FIND_SKILLS=false
    local SKILLS_API_URL=""

    # Parse arguments
    while [ $# -gt 0 ]; do
        case $1 in
            --name)       WORKER_NAME="$2"; shift 2 ;;
            --fs)         FS="$2"; shift 2 ;;
            --fs-key)     FS_KEY="$2"; shift 2 ;;
            --fs-secret)  FS_SECRET="$2"; shift 2 ;;
            --find-skills) ENABLE_FIND_SKILLS=true; shift ;;
            --skills-api-url) SKILLS_API_URL="$2"; shift 2 ;;
            --reset)      RESET=true; shift ;;
            *)            error "$(msg error.unknown_option "$1")" ;;
        esac
    done

    # Validate required params
    [ -z "${WORKER_NAME}" ] && error "$(msg error.name_required)"
    [ -z "${FS}" ] && error "$(msg error.fs_required)"
    [ -z "${FS_KEY}" ] && error "$(msg error.fs_key_required)"
    [ -z "${FS_SECRET}" ] && error "$(msg error.fs_secret_required)"

    local CONTAINER_NAME="hiclaw-worker-${WORKER_NAME}"

    # Handle reset
    if [ "${RESET}" = true ]; then
        log "$(msg worker.resetting "${WORKER_NAME}")"
        ${DOCKER_CMD} stop "${CONTAINER_NAME}" 2>/dev/null || true
        ${DOCKER_CMD} rm "${CONTAINER_NAME}" 2>/dev/null || true
    fi

    # Check for existing container
    if ${DOCKER_CMD} ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        error "$(msg worker.exists "${CONTAINER_NAME}")"
    fi

    log "$(msg worker.starting "${WORKER_NAME}")"

    # Build docker run args
    local DOCKER_ENV=""
    DOCKER_ENV="${DOCKER_ENV} -e HOME=/root/hiclaw-fs/agents/${WORKER_NAME}"
    DOCKER_ENV="${DOCKER_ENV} -w /root/hiclaw-fs/agents/${WORKER_NAME}"
    DOCKER_ENV="${DOCKER_ENV} -e HICLAW_WORKER_NAME=${WORKER_NAME}"
    DOCKER_ENV="${DOCKER_ENV} -e HICLAW_FS_ENDPOINT=${FS}"
    DOCKER_ENV="${DOCKER_ENV} -e HICLAW_FS_ACCESS_KEY=${FS_KEY}"
    DOCKER_ENV="${DOCKER_ENV} -e HICLAW_FS_SECRET_KEY=${FS_SECRET}"

    # Add SKILLS_API_URL if find-skills is enabled and URL is specified
    if [ "${ENABLE_FIND_SKILLS}" = true ] && [ -n "${SKILLS_API_URL}" ]; then
        DOCKER_ENV="${DOCKER_ENV} -e SKILLS_API_URL=${SKILLS_API_URL}"
        log "$(msg worker.skills_url "${SKILLS_API_URL}")"
    fi

    # shellcheck disable=SC2086
    ${DOCKER_CMD} run -d \
        --name "${CONTAINER_NAME}" \
        ${DOCKER_ENV} \
        --restart unless-stopped \
        "${WORKER_IMAGE}"

    log ""
    log "$(msg worker.started "${WORKER_NAME}")"
    log "$(msg worker.container "${CONTAINER_NAME}")"
    log "$(msg worker.view_logs "${CONTAINER_NAME}")"
}

# ============================================================
# Checkpoint and Rollback Mechanism
# ============================================================

# Backup directory
BACKUP_DIR="${HOME}/.hiclaw-backups"
INSTALL_STATE_FILE="${HOME}/.hiclaw-install-state"

# Backup before installation
backup_before_install() {
    local timestamp
    timestamp=$(date +%Y%m%d%H%M%S)
    local backup_path="${BACKUP_DIR}/backup-${timestamp}"
    local env_file="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
    
    mkdir -p "${backup_path}"
    
    # Backup configuration file
    if [ -f "${env_file}" ]; then
        cp "${env_file}" "${backup_path}/hiclaw-manager.env"
    fi
    
    # Save backup information
    cat > "${INSTALL_STATE_FILE}" <<EOF
{
    "backup_path": "${backup_path}",
    "timestamp": "$(date -Iseconds)",
    "env_file": "${env_file}"
}
EOF
    
    log "$(msg backup.created "${backup_path}")"
    echo "${backup_path}"
}

# Rollback installation
rollback_install() {
    if [ ! -f "${INSTALL_STATE_FILE}" ]; then
        log "$(msg rollback.no_backup)"
        return 1
    fi
    
    # Read backup information
    local backup_path
    if command -v jq &>/dev/null; then
        backup_path=$(jq -r '.backup_path // empty' "${INSTALL_STATE_FILE}" 2>/dev/null)
    else
        backup_path=$(grep "backup_path" "${INSTALL_STATE_FILE}" | cut -d'"' -f4)
    fi
    
    if [ -z "${backup_path}" ] || [ ! -d "${backup_path}" ]; then
        log "$(msg rollback.no_backup)"
        return 1
    fi
    
    log "$(msg rollback.start)"
    
    # Stop and remove containers
    docker stop hiclaw-manager 2>/dev/null || true
    docker rm hiclaw-manager 2>/dev/null || true
    
    # Remove worker containers
    for c in $(docker ps -a --filter "name=hiclaw-worker-" --format '{{.Names}}' 2>/dev/null); do
        docker rm -f "${c}" 2>/dev/null || true
    done
    
    # Restore configuration
    if [ -f "${backup_path}/hiclaw-manager.env" ]; then
        cp "${backup_path}/hiclaw-manager.env" "${HOME}/"
        log "Configuration restored"
    fi
    
    # Cleanup
    rm -rf "${backup_path}" "${INSTALL_STATE_FILE}"
    
    log "$(msg rollback.complete)"
}

# Check incomplete installation
check_incomplete_install() {
    local has_issue=false
    
    # Check if there's a container but invalid configuration
    if docker ps -a --format '{{.Names}}' 2>/dev/null | grep -q "hiclaw-manager"; then
        local env_file="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
        if [ ! -f "${env_file}" ]; then
            log "$(msg cleanup.incomplete)"
            has_issue=true
        elif ! grep -q "^HICLAW_LLM_API_KEY=." "${env_file}" 2>/dev/null; then
            log "$(msg cleanup.incomplete) - API Key not configured"
            has_issue=true
        fi
    fi
    
    if [ "${has_issue}" = true ]; then
        return 1
    fi
    
    return 0
}

# Complete cleanup
complete_cleanup() {
    log "$(msg cleanup.start)"
    
    # Stop all containers
    docker stop hiclaw-manager 2>/dev/null || true
    docker rm hiclaw-manager 2>/dev/null || true
    
    # Remove all worker containers
    for c in $(docker ps -a --filter "name=hiclaw-worker-" --format '{{.Names}}' 2>/dev/null); do
        log "Removing: ${c}"
        docker rm -f "${c}" 2>/dev/null || true
    done
    
    # Remove data volumes
    docker volume rm hiclaw-data 2>/dev/null || true
    
    # Remove configuration files
    local env_file="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
    rm -f "${env_file}" 2>/dev/null || true
    
    # Remove checkpoints
    rm -rf "${CHECKPOINT_DIR}" 2>/dev/null || true
    
    # Remove backup state
    rm -f "${INSTALL_STATE_FILE}" 2>/dev/null || true
    
    # Remove old backups
    rm -rf "${BACKUP_DIR}" 2>/dev/null || true
    
    log "$(msg cleanup.complete)"
}

# ============================================================
# New Commands
# ============================================================

# Rollback command handler
cmd_rollback() {
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
        echo "$(msg rollback.confirm)"
        read -r choice
        case "$choice" in
            y|Y) ;;
            *) 
                log "Rollback cancelled"
                return 1
                ;;
        esac
    fi
    
    rollback_install
}

# Cleanup command handler
cmd_cleanup() {
    if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
        echo "$(msg cleanup.confirm)"
        read -r choice
        case "$choice" in
            y|Y) ;;
            *) 
                log "Cleanup cancelled"
                return 1
                ;;
        esac
    fi
    
    complete_cleanup
}

# ============================================================
# Main
# ============================================================

# ============================================================
# LLM API connectivity test
# ============================================================

test_llm_connectivity() {
    local base_url="$1"
    local api_key="$2"
    local model="$3"
    local hint="${4:-}"  # optional: extra hint shown on failure
    if ! command -v curl >/dev/null 2>&1; then
        echo -e "\033[33m$(msg llm.openai.test.no_curl)\033[0m"
        return 0
    fi
    log "$(msg llm.openai.test.testing)"
    local _body _http_code _tmpfile
    _tmpfile=$(mktemp)
    _http_code=$(curl -s -o "${_tmpfile}" -w "%{http_code}" \
        -X POST "${base_url%/}/chat/completions" \
        -H "Authorization: Bearer ${api_key}" \
        -H "Content-Type: application/json" \
        -H "User-Agent: HiClaw/${HICLAW_VERSION:-latest}" \
        --max-time 30 \
        -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":1}" \
        2>/dev/null)
    _body=$(cat "${_tmpfile}")
    rm -f "${_tmpfile}"
    if [ "${_http_code}" = "200" ] || [ "${_http_code}" = "201" ]; then
        log "$(msg llm.openai.test.ok)"
        LLM_TEST_HTTP_CODE="200"
        return 0
    else
        LLM_TEST_HTTP_CODE="${_http_code}"
        LLM_TEST_BODY="${_body}"
        echo -e "\033[33m$(msg llm.openai.test.fail "${_http_code}" "${_body}")\033[0m"
        if [ -n "${hint}" ]; then
            echo -e "\033[33m${hint}\033[0m"
        fi
        if [ "${HICLAW_NON_INTERACTIVE}" != "1" ]; then
            local _confirm
            read -p "$(msg llm.openai.test.confirm)" _confirm
            if [ "${_confirm}" != "y" ] && [ "${_confirm}" != "Y" ]; then
                log "$(msg llm.openai.test.aborted)"
                exit 1
            fi
        fi
        return 1
    fi
}

# Show API error with solution
show_api_error_solution() {
    local http_code="${LLM_TEST_HTTP_CODE}"
    local provider="${1:-}"
    
    case "${http_code}" in
        401)
            echo -e "\033[31m$(msg llm.error.401)\033[0m"
            echo -e "\033[33m$(msg llm.error.401.hint)\033[0m"
            ;;
        403)
            echo -e "\033[31m$(msg llm.error.403)\033[0m"
            echo -e "\033[33m$(msg llm.error.403.hint)\033[0m"
            ;;
        404)
            echo -e "\033[31m$(msg llm.error.404)\033[0m"
            echo -e "\033[33m$(msg llm.error.404.hint)\033[0m"
            ;;
        *)
            echo -e "\033[31m$(msg llm.error.generic "${http_code}")\033[0m"
            ;;
    esac
}

# Validate LLM API Key with retry
validate_llm_with_retry() {
    local base_url="$1"
    local api_key="$2"
    local model="$3"
    local hint="$4"
    local max_retries=3
    local retry=0
    
    while [ ${retry} -lt ${max_retries} ]; do
        if test_llm_connectivity "${base_url}" "${HICLAW_LLM_API_KEY}" "${model}" "${hint}"; then
            return 0
        fi
        
        retry=$((retry + 1))
        
        if [ ${retry} -lt ${max_retries} ]; then
            show_api_error_solution "${HICLAW_LLM_PROVIDER}"
            echo ""
            echo -e "\033[36m$(msg llm.apikey.retry_prompt)\033[0m"
            read -p "> " HICLAW_LLM_API_KEY
        fi
    done
    
    # Max retries reached, show menu
    echo ""
    log "$(msg llm.apikey.max_retries)"
    echo ""
    echo "$(msg llm.retry.menu.title)"
    echo "  1) $(msg llm.retry.menu.retry)"
    echo "  2) $(msg llm.retry.menu.skip)"
    echo "  3) $(msg llm.retry.menu.abort)"
    echo ""
    read -p "$(msg llm.retry.menu.prompt): " choice
    
    case "${choice}" in
        1)
            read -p "$(msg llm.apikey_prompt): " HICLAW_LLM_API_KEY
            validate_llm_with_retry "${base_url}" "${HICLAW_LLM_API_KEY}" "${model}" "${hint}"
            return $?
            ;;
        2)
            log "$(msg llm.validation.skipped)"
            echo -e "\033[33m$(msg llm.validation.skipped.warning)\033[0m"
            return 0
            ;;
        3)
            error "$(msg install.cancelled)"
            exit 1
            ;;
        *)
            validate_llm_with_retry "${base_url}" "${api_key}" "${model}" "${hint}"
            return $?
            ;;
    esac
}

# ============================================================
# Config management commands
# ============================================================

cmd_config() {
    local action="${1:-"--help"}"
    
    case "${action}" in
        --fix|--fix-llm)
            log "$(msg config.fix.llm.title)"
            echo ""
            echo "$(msg config.current.provider "${HICLAW_LLM_PROVIDER:-unknown}")"
            echo "$(msg config.current.model "${HICLAW_DEFAULT_MODEL:-unknown}")"
            echo ""
            
            # Re-prompt for provider
            log "$(msg llm.provider.title)"
            echo "  1) $(msg llm.provider.alibaba)"
            echo "  2) $(msg llm.provider.openai_compat)"
            echo ""
            read -p "$(msg llm.provider.prompt): " provider_choice
            
            case "${provider_choice}" in
                1)
                    HICLAW_LLM_PROVIDER="alibaba-cloud"
                    HICLAW_OPENAI_BASE_URL="https://dashscope.aliyuncs.com/compatible-mode/v1"
                    HICLAW_DEFAULT_MODEL="qwen3.5-plus"
                    ;;
                2)
                    HICLAW_LLM_PROVIDER="openai-compat"
                    read -p "$(msg llm.openai.base_url_prompt): " HICLAW_OPENAI_BASE_URL
                    read -p "$(msg llm.openai.model_prompt): " HICLAW_DEFAULT_MODEL
                    ;;
            esac
            
            # Re-prompt for API key
            log "$(msg llm.apikey_hint)"
            prompt HICLAW_LLM_API_KEY "$(msg llm.apikey_prompt)" "" "true"
            
            # Validate
            validate_llm_with_retry "${HICLAW_OPENAI_BASE_URL}" "${HICLAW_LLM_API_KEY}" "${HICLAW_DEFAULT_MODEL}"
            
            # Save config
            local env_file="${HOME}/hiclaw-manager.env"
            if [ -f "${env_file}" ]; then
                # Update existing env file
                sed -i "s|^HICLAW_LLM_PROVIDER=.*|HICLAW_LLM_PROVIDER=${HICLAW_LLM_PROVIDER}|" "${env_file}"
                sed -i "s|^HICLAW_LLM_API_KEY=.*|HICLAW_LLM_API_KEY=${HICLAW_LLM_API_KEY}|" "${env_file}"
                sed -i "s|^HICLAW_DEFAULT_MODEL=.*|HICLAW_DEFAULT_MODEL=${HICLAW_DEFAULT_MODEL}|" "${env_file}"
                sed -i "s|^HICLAW_OPENAI_BASE_URL=.*|HICLAW_OPENAI_BASE_URL=${HICLAW_OPENAI_BASE_URL}|" "${env_file}"
            fi
            
            log "$(msg config.fix.success)"
            echo -e "\033[33m$(msg config.fix.restart_required)\033[0m"
            ;;
       --show)
            msg "config.show.title"
            msg "config.show.provider" "${HICLAW_LLM_PROVIDER:-$(msg config.show.api_key_unset)}"
            msg "config.show.model" "${HICLAW_DEFAULT_MODEL:-$(msg config.show.api_key_unset)}"
            msg "config.show.base_url" "${HICLAW_OPENAI_BASE_URL:-$(msg config.show.default)}"
            local _api_key_status
            if [ -n "${HICLAW_LLM_API_KEY}" ]; then
                _api_key_status=$(msg "config.show.api_key_set")
            else
                _api_key_status=$(msg "config.show.api_key_unset")
            fi
            msg "config.show.api_key" "$_api_key_status"
            echo ""
            ;;
        --verify)
            if [ -z "${HICLAW_LLM_API_KEY}" ]; then
                error "$(msg "config.error.no_api_key")"
                exit 1
            fi
            test_llm_connectivity "${HICLAW_OPENAI_BASE_URL}" "${HICLAW_LLM_API_KEY}" "${HICLAW_DEFAULT_MODEL}"
            ;;
        --help|*)
            msg "config.help.usage" "$0"
            echo ""
            msg "config.help.commands"
            msg "config.help.fix_llm"
            msg "config.help.show"
            msg "config.help.verify"
            msg "config.help.help"
            echo ""
            msg "config.help.examples"
            msg "config.help.example_fix" "$0"
            msg "config.help.example_verify" "$0"
            ;;
    esac
}

# ============================================================
# Check container runtime (docker or podman)
# ============================================================

check_container_runtime() {
    if command -v docker >/dev/null 2>&1; then
        DOCKER_CMD="docker"
    elif command -v podman >/dev/null 2>&1; then
        DOCKER_CMD="podman"
    else
        echo -e "\033[31m[HiClaw ERROR]\033[0m $(msg error.docker_not_found)" >&2
        exit 1
    fi

    # Command exists — check if daemon is running
    if ! ${DOCKER_CMD} ps >/dev/null 2>&1; then
        echo -e "\033[31m[HiClaw ERROR]\033[0m $(msg error.docker_not_running)" >&2
        exit 1
    fi
}

check_container_runtime

case "${1:-}" in
    manager|"")
        # Default to manager installation if no argument or explicit "manager"
        install_manager
        ;;
    worker)
        shift
        install_worker "$@"
        ;;
    config)
        shift
        cmd_config "$@"
        ;;
    *)
        echo "Usage: $0 [manager|worker|config [options]]"
        echo ""
        echo "Commands:"
        echo "  manager              Interactive Manager installation (default)"
        echo "                       Choose Quick Start (all defaults) or Manual mode"
        echo "  worker               Worker installation (requires --name and connection params)"
        echo "  config               Configuration management commands"
        echo ""
        echo "Quick Start (fastest):"
        echo "  $0"
        echo "  # Then select '1' for Quick Start mode"
        echo ""
        echo "Non-interactive (for automation):"
        echo "  HICLAW_NON_INTERACTIVE=1 HICLAW_LLM_API_KEY=sk-xxx $0"
        echo ""
        echo "Config Commands:"
        echo "  config --fix-llm     Fix LLM configuration (interactive)"
        echo "  config --show        Show current configuration"
        echo "  config --verify      Verify configuration validity"
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
