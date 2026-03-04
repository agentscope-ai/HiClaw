#!/usr/bin/env pwsh
# hiclaw-install.ps1 - One-click installation for HiClaw Manager and Worker on Windows
#
# Usage:
#   .\hiclaw-install.ps1                  # Interactive installation (choose Quick Start or Manual)
#   .\hiclaw-install.ps1 manager          # Same as above (explicit)
#   .\hiclaw-install.ps1 worker --name <name> ...  # Worker installation
#
# Onboarding Modes:
#   Quick Start  - Fast installation with all default values (recommended)
#   Manual       - Customize each option step by step
#
# Environment variables (for automation):
#   HICLAW_NON_INTERACTIVE    Skip all prompts, use defaults  (default: 0)
#   HICLAW_LLM_PROVIDER       LLM provider       (default: qwen)
#   HICLAW_DEFAULT_MODEL      Default model      (default: qwen3.5-plus)
#   HICLAW_LLM_API_KEY        LLM API key        (required)
#   HICLAW_ADMIN_USER         Admin username     (default: admin)
#   HICLAW_ADMIN_PASSWORD     Admin password     (auto-generated if not set, min 8 chars)
#   HICLAW_MATRIX_DOMAIN      Matrix domain      (default: matrix-local.hiclaw.io:18080)
#   HICLAW_MOUNT_SOCKET       Mount container runtime socket (default: 1)
#   HICLAW_DATA_DIR           Docker volume name for persistent data (default: hiclaw-data)
#   HICLAW_WORKSPACE_DIR      Host directory for manager workspace (default: ~/hiclaw-manager)
#   HICLAW_VERSION            Image tag          (default: latest)
#   HICLAW_REGISTRY           Image registry     (default: auto-detected by timezone)
#   HICLAW_INSTALL_MANAGER_IMAGE  Override manager image (e.g., local build)
#   HICLAW_INSTALL_WORKER_IMAGE   Override worker image  (e.g., local build)
#   HICLAW_PORT_GATEWAY       Host port for Higress gateway (default: 18080)
#   HICLAW_PORT_CONSOLE       Host port for Higress console (default: 18001)
#   HICLAW_PORT_ELEMENT_WEB   Host port for Element Web direct access (default: 18088)

#Requires -Version 7.0

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("manager", "worker", "uninstall")]
    [string]$Command = "manager",

    # Worker options
    [string]$Name,
    [string]$Fs,
    [string]$FsKey,
    [string]$FsSecret,
    [switch]$Reset,
    [switch]$FindSkills,
    [string]$SkillsApiUrl,

    # General options
    [switch]$NonInteractive,
    [string]$EnvFile
)

# ============================================================
# Configuration
# ============================================================

$script:HICLAW_VERSION = if ($env:HICLAW_VERSION) { $env:HICLAW_VERSION } else { "latest" }
$script:HICLAW_NON_INTERACTIVE = if ($env:HICLAW_NON_INTERACTIVE -eq "1" -or $NonInteractive) { $true } else { $false }
$script:HICLAW_MOUNT_SOCKET = if ($env:HICLAW_MOUNT_SOCKET -eq "0") { $false } else { $true }
$script:HICLAW_ENV_FILE = if ($EnvFile) { $EnvFile } elseif ($env:HICLAW_ENV_FILE) { $env:HICLAW_ENV_FILE } else { "$env:USERPROFILE\hiclaw-manager.env" }

# ============================================================
# Utility Functions
# ============================================================

function Write-Log {
    param([string]$Message)
    Write-Host "`e[36m[HiClaw]`e[0m $Message"
}

function Write-Error {
    param([string]$Message)
    Write-Host "`e[31m[HiClaw ERROR]`e[0m $Message" -ForegroundColor Red
    throw $Message
}

function Write-Warning {
    param([string]$Message)
    Write-Host "`e[33m[HiClaw WARNING]`e[0m $Message"
}

function Test-DockerRunning {
    try {
        $null = docker info 2>&1
        if ($LASTEXITCODE -ne 0) {
            return $false
        }
        return $true
    }
    catch {
        return $false
    }
}

function Get-HiClawTimeZone {
    try {
        if ($env:HICLAW_TIMEZONE) {
            return $env:HICLAW_TIMEZONE
        }

        $tz = (Get-TimeZone).Id
        # Convert Windows timezone to IANA format
        $tzMap = @{
            "China Standard Time" = "Asia/Shanghai"
            "Pacific Standard Time" = "America/Los_Angeles"
            "Mountain Standard Time" = "America/Denver"
            "Central Standard Time" = "America/Chicago"
            "Eastern Standard Time" = "America/New_York"
            "GMT Standard Time" = "Europe/London"
            "Central European Standard Time" = "Europe/Berlin"
            "Tokyo Standard Time" = "Asia/Tokyo"
            "Singapore Standard Time" = "Asia/Singapore"
            "Korea Standard Time" = "Asia/Seoul"
            "India Standard Time" = "Asia/Kolkata"
        }

        if ($tzMap.ContainsKey($tz)) {
            return $tzMap[$tz]
        }
        return $tz
    }
    catch {
        return "Asia/Shanghai"
    }
}

function Get-Registry {
    param([string]$Timezone)

    if ($env:HICLAW_REGISTRY) {
        return $env:HICLAW_REGISTRY
    }

    # Americas
    if ($Timezone -match "^America/") {
        return "higress-registry.us-west-1.cr.aliyuncs.com"
    }

    # Southeast Asia
    if ($Timezone -match "^(Asia/Singapore|Asia/Bangkok|Asia/Jakarta|Asia/Kuala_Lumpur|Asia/Ho_Chi_Minh|Asia/Manila|Asia/Yangon)") {
        return "higress-registry.ap-southeast-7.cr.aliyuncs.com"
    }

    # Default: China
    return "higress-registry.cn-hangzhou.cr.aliyuncs.com"
}

function Get-HiClawLanguage {
    param([string]$Timezone)
    $chineseZones = @(
        "Asia/Shanghai", "Asia/Chongqing", "Asia/Harbin", "Asia/Urumqi",
        "Asia/Taipei", "Asia/Hong_Kong", "Asia/Macau"
    )
    if ($chineseZones -contains $Timezone) { return "zh" }
    return "en"
}

# ============================================================
# Centralized message dictionary and Get-Msg function
# ============================================================

$script:Messages = @{
    # --- Timezone detection messages ---
    "tz.warning.title" = @{ zh = "无法自动检测时区。"; en = "Could not detect timezone automatically." }
    "tz.warning.prompt" = @{ zh = "请输入您的时区（例如 Asia/Shanghai、America/New_York）。"; en = "Please enter your timezone (e.g., Asia/Shanghai, America/New_York)." }
    "tz.default" = @{ zh = "使用默认时区: {0}"; en = "Using default timezone: {0}" }
    "tz.input_prompt" = @{ zh = "时区"; en = "Timezone" }

    # --- Installation title and info ---
    "install.title" = @{ zh = "=== HiClaw Manager 安装 ==="; en = "=== HiClaw Manager Installation ===" }
    "install.registry" = @{ zh = "镜像仓库: {0}"; en = "Registry: {0}" }
    "install.dir" = @{ zh = "安装目录: {0}"; en = "Installation directory: {0}" }
    "install.dir_hint" = @{ zh = "  （env 文件 'hiclaw-manager.env' 将保存到 HOME 目录。）"; en = "  (The env file 'hiclaw-manager.env' will be saved to your HOME directory.)" }
    "install.dir_hint2" = @{ zh = "  （请从您希望管理此安装的目录运行此脚本。）"; en = "  (Run this script from the directory where you want to manage this installation.)" }

    # --- Onboarding mode ---
    "install.mode.title" = @{ zh = "--- Onboarding 模式 ---"; en = "--- Onboarding Mode ---" }
    "install.mode.choose" = @{ zh = "选择安装模式:"; en = "Choose your installation mode:" }
    "install.mode.quickstart" = @{ zh = "  1) 快速开始  - 使用阿里云百炼快速安装（推荐）"; en = "  1) Quick Start  - Fast installation with Alibaba Cloud (recommended)" }
    "install.mode.manual" = @{ zh = "  2) 手动配置  - 选择 LLM 提供商并自定义选项"; en = "  2) Manual       - Choose LLM provider and customize options" }
    "install.mode.prompt" = @{ zh = "请选择 [1/2]"; en = "Enter choice [1/2]" }
    "install.mode.quickstart_selected" = @{ zh = "已选择快速开始模式 - 使用阿里云百炼"; en = "Quick Start mode selected - using Alibaba Cloud Bailian" }
    "install.mode.manual_selected" = @{ zh = "已选择手动配置模式 - 您将选择 LLM 提供商并自定义选项"; en = "Manual mode selected - you will choose LLM provider and customize options" }
    "install.mode.invalid" = @{ zh = "无效选择，默认使用快速开始模式"; en = "Invalid choice, defaulting to Quick Start mode" }

    # --- Existing installation detected ---
    "install.existing.detected" = @{ zh = "检测到已有 Manager 安装（env 文件: {0}）"; en = "Existing Manager installation detected (env file: {0})" }
    "install.existing.choose" = @{ zh = "选择操作:"; en = "Choose an action:" }
    "install.existing.upgrade" = @{ zh = "  1) 就地升级（保留数据、工作空间、env 文件）"; en = "  1) In-place upgrade (keep data, workspace, env file)" }
    "install.existing.reinstall" = @{ zh = "  2) 全新重装（删除所有数据，重新开始）"; en = "  2) Clean reinstall (remove all data, start fresh)" }
    "install.existing.cancel" = @{ zh = "  3) 取消"; en = "  3) Cancel" }
    "install.existing.prompt" = @{ zh = "请选择 [1/2/3]"; en = "Enter choice [1/2/3]" }
    "install.existing.upgrade_noninteractive" = @{ zh = "非交互模式: 执行就地升级..."; en = "Non-interactive mode: performing in-place upgrade..." }
    "install.existing.upgrading" = @{ zh = "执行就地升级..."; en = "Performing in-place upgrade..." }
    "install.existing.warn_manager_stop" = @{ zh = "⚠️  Manager 容器将被停止并重新创建。"; en = "⚠️  Manager container will be stopped and recreated." }
    "install.existing.warn_worker_recreate" = @{ zh = "⚠️  Worker 容器也将被重新创建（以更新 Manager IP）。"; en = "⚠️  Worker containers will also be recreated (to update Manager IP in hosts)." }
    "install.existing.continue_prompt" = @{ zh = "继续？[y/N]"; en = "Continue? [y/N]" }
    "install.existing.cancelled" = @{ zh = "安装已取消。"; en = "Installation cancelled." }
    "install.existing.stopping_manager" = @{ zh = "停止并移除现有 manager 容器..."; en = "Stopping and removing existing manager container..." }
    "install.existing.stopping_workers" = @{ zh = "停止并移除现有 worker 容器..."; en = "Stopping and removing existing worker containers..." }
    "install.existing.removed" = @{ zh = "  已移除: {0}"; en = "  Removed: {0}" }

    # --- Clean reinstall messages ---
    "install.reinstall.performing" = @{ zh = "执行全新重装..."; en = "Performing clean reinstall..." }
    "install.reinstall.warn_stop" = @{ zh = "⚠️  以下运行中的容器将被停止:"; en = "⚠️  The following running containers will be stopped:" }
    "install.reinstall.warn_delete" = @{ zh = "⚠️  警告: 以下内容将被删除:"; en = "⚠️  WARNING: This will DELETE the following:" }
    "install.reinstall.warn_volume" = @{ zh = "   - Docker 卷: hiclaw-data"; en = "   - Docker volume: hiclaw-data" }
    "install.reinstall.warn_env" = @{ zh = "   - Env 文件: {0}"; en = "   - Env file: {0}" }
    "install.reinstall.warn_workspace" = @{ zh = "   - Manager 工作空间: {0}"; en = "   - Manager workspace: {0}" }
    "install.reinstall.warn_workers" = @{ zh = "   - 所有 worker 容器"; en = "   - All worker containers" }
    "install.reinstall.confirm_type" = @{ zh = "请输入工作空间路径以确认删除（或按 Ctrl+C 取消）:"; en = "To confirm deletion, please type the workspace path:" }
    "install.reinstall.confirm_path" = @{ zh = "输入路径以确认（或按 Ctrl+C 取消）"; en = "Type the path to confirm (or press Ctrl+C to cancel)" }
    "install.reinstall.path_mismatch" = @{ zh = "路径不匹配。中止重装。输入: '{0}'，期望: '{1}'"; en = "Path mismatch. Aborting reinstall. Input: '{0}', Expected: '{1}'" }
    "install.reinstall.confirmed" = @{ zh = "已确认。正在清理..."; en = "Confirmed. Cleaning up..." }
    "install.reinstall.removed_worker" = @{ zh = "  已移除 worker: {0}"; en = "  Removed worker: {0}" }
    "install.reinstall.removing_volume" = @{ zh = "正在移除 Docker 卷: hiclaw-data"; en = "Removing Docker volume: hiclaw-data" }
    "install.reinstall.warn_volume_fail" = @{ zh = "  警告: 无法移除卷（可能有引用）"; en = "  Warning: Could not remove volume (may have references)" }
    "install.reinstall.removing_workspace" = @{ zh = "正在移除工作空间目录: {0}"; en = "Removing workspace directory: {0}" }
    "install.reinstall.removing_env" = @{ zh = "正在移除 env 文件: {0}"; en = "Removing env file: {0}" }
    "install.reinstall.cleanup_done" = @{ zh = "清理完成。开始全新安装..."; en = "Cleanup complete. Starting fresh installation..." }
    "install.reinstall.failed_rm_workspace" = @{ zh = "无法移除工作空间目录"; en = "Failed to remove workspace directory" }

    # --- Loading existing config ---
    "install.loading_config" = @{ zh = "从 {0} 加载已有配置（shell 环境变量优先）..."; en = "Loading existing config from {0} (shell env vars take priority)..." }

    # --- LLM Configuration ---
    "llm.title" = @{ zh = "--- LLM 配置 ---"; en = "--- LLM Configuration ---" }
    "llm.provider.label" = @{ zh = "  提供商: {0}"; en = "  Provider: {0}" }
    "llm.model.label" = @{ zh = "  模型: {0}"; en = "  Model: {0}" }
    "llm.provider.qwen" = @{ zh = "  提供商: qwen（阿里云百炼）"; en = "  Provider: qwen (Alibaba Cloud Bailian)" }
    "llm.provider.qwen_default" = @{ zh = "  提供商: {0}（默认）"; en = "  Provider: {0} (default)" }
    "llm.model.default" = @{ zh = "  模型: {0}（默认）"; en = "  Model: {0} (default)" }
    "llm.apikey_hint" = @{ zh = "  💡 获取阿里云百炼 API Key:"; en = "  💡 Get your Alibaba Cloud Bailian API Key from:" }
    "llm.apikey_url" = @{ zh = "     https://www.aliyun.com/product/bailian"; en = "     https://www.aliyun.com/product/bailian" }
    "llm.apikey_prompt" = @{ zh = "LLM API Key"; en = "LLM API Key" }
    "llm.providers_title" = @{ zh = "可用 LLM 提供商:"; en = "Available LLM Providers:" }
    "llm.provider.alibaba" = @{ zh = "  1) 阿里云百炼  - 推荐中国用户使用"; en = "  1) Alibaba Cloud Bailian  - Recommended for Chinese users" }
    "llm.provider.openai_compat" = @{ zh = "  2) OpenAI 兼容 API  - 自定义 Base URL（OpenAI、DeepSeek 等）"; en = "  2) OpenAI-compatible API  - Custom Base URL (OpenAI, DeepSeek, etc.)" }
    "llm.provider.select" = @{ zh = "选择提供商 [1/2]"; en = "Select provider [1/2]" }
    "llm.alibaba.models_title" = @{ zh = "选择百炼模型系列:"; en = "Select Bailian model series:" }
    "llm.alibaba.model.codingplan" = @{ zh = "  1) CodingPlan  - 专为编程任务优化（推荐）"; en = "  1) CodingPlan  - Optimized for coding tasks (recommended)" }
    "llm.alibaba.model.qwen" = @{ zh = "  2) 百炼通用接口"; en = "  2) qwen general  - General purpose LLM" }
    "llm.alibaba.model.select" = @{ zh = "选择模型系列 [1/2]"; en = "Select model series [1/2]" }
    "llm.provider.selected_codingplan" = @{ zh = "  提供商: 阿里云百炼 CodingPlan"; en = "  Provider: Alibaba Cloud Bailian CodingPlan" }
    "llm.provider.selected_qwen" = @{ zh = "  提供商: 阿里云百炼"; en = "  Provider: Alibaba Cloud Bailian" }
    "llm.provider.selected_openai" = @{ zh = "  提供商: {0}（OpenAI 兼容）"; en = "  Provider: {0} (OpenAI-compatible)" }
    "llm.provider.invalid" = @{ zh = "无效选择，默认使用阿里云百炼 CodingPlan"; en = "Invalid choice, defaulting to Alibaba Cloud Bailian CodingPlan" }
    "llm.openai.base_url_prompt" = @{ zh = "Base URL（例如 https://api.openai.com/v1）"; en = "Base URL (e.g., https://api.openai.com/v1)" }
    "llm.openai.model_prompt" = @{ zh = "默认模型 ID [gpt-4o]"; en = "Default Model ID [gpt-4o]" }
    "llm.openai.base_url_label" = @{ zh = "  Base URL: {0}"; en = "  Base URL: {0}" }

    # --- Admin Credentials ---
    "admin.title" = @{ zh = "--- 管理员凭据 ---"; en = "--- Admin Credentials ---" }
    "admin.username_prompt" = @{ zh = "管理员用户名"; en = "Admin Username" }
    "admin.password_prompt" = @{ zh = "管理员密码（留空自动生成，最少 8 位）"; en = "Admin Password (leave empty to auto-generate, min 8 chars)" }
    "admin.password_generated" = @{ zh = "  已自动生成管理员密码"; en = "  Auto-generated admin password" }
    "admin.password_too_short" = @{ zh = "管理员密码至少需要 8 个字符（MinIO 要求）。当前长度: {0}"; en = "Admin password must be at least 8 characters (MinIO requirement). Current length: {0}" }

    # --- Port Configuration ---
    "port.title" = @{ zh = "--- 端口配置（按回车使用默认值）---"; en = "--- Port Configuration (press Enter for defaults) ---" }
    "port.gateway_prompt" = @{ zh = "网关主机端口（容器内 8080）"; en = "Host port for gateway (8080 inside container)" }
    "port.console_prompt" = @{ zh = "Higress 控制台主机端口（容器内 8001）"; en = "Host port for Higress console (8001 inside container)" }
    "port.element_prompt" = @{ zh = "Element Web 直接访问主机端口（容器内 8088）"; en = "Host port for Element Web direct access (8088 inside container)" }
    "port.local_only.title" = @{ zh = "--- 网络访问模式 ---"; en = "--- Network Access Mode ---" }
    "port.local_only.hint_yes" = @{ zh = "  仅本机使用，无需开放外部端口（推荐）"; en = "  Local use only, no external port exposure (recommended)" }
    "port.local_only.hint_no" = @{ zh = "  允许外部访问（局域网 / 公网）"; en = "  Allow external access (LAN / public network)" }
    "port.local_only.choice" = @{ zh = "请选择 [1/2]"; en = "Enter choice [1/2]" }
    "port.local_only.selected_local" = @{ zh = "端口已绑定到 127.0.0.1（仅本机访问）"; en = "Ports bound to 127.0.0.1 (localhost only)" }
    "port.local_only.selected_external" = @{ zh = "端口已绑定到所有网络接口（0.0.0.0）"; en = "Ports bound to all interfaces (0.0.0.0)" }
    "port.local_only.https_hint" = @{ zh = "⚠️  建议在 Higress 控制台配置 TLS 证书并启用 HTTPS，避免明文传输。"; en = "⚠️  It is recommended to configure TLS certificates and enable HTTPS in the Higress Console to avoid plaintext transmission." }
    "port.local_only.https_docs" = @{ zh = ""; en = "" }

    # --- Domain Configuration ---
    "domain.title" = @{ zh = "--- 域名配置（按回车使用默认值）---"; en = "--- Domain Configuration (press Enter for defaults) ---" }
    "domain.matrix_prompt" = @{ zh = "Matrix 域名"; en = "Matrix Domain" }
    "domain.element_prompt" = @{ zh = "Element Web 域名"; en = "Element Web Domain" }
    "domain.gateway_prompt" = @{ zh = "AI 网关域名"; en = "AI Gateway Domain" }
    "domain.fs_prompt" = @{ zh = "文件系统域名"; en = "File System Domain" }

    # --- GitHub Integration ---
    "github.title" = @{ zh = "--- GitHub 集成（可选，按回车跳过）---"; en = "--- GitHub Integration (optional, press Enter to skip) ---" }
    "github.token_prompt" = @{ zh = "GitHub 个人访问令牌（可选）"; en = "GitHub Personal Access Token (optional)" }

    # --- Skills Registry ---
    "skills.title" = @{ zh = "--- Skills 注册中心（可选，按回车使用默认 https://skills.sh）---"; en = "--- Skills Registry (optional, press Enter for default https://skills.sh) ---" }
    "skills.url_prompt" = @{ zh = "Skills 注册中心 URL（留空使用默认 https://skills.sh）"; en = "Skills Registry URL (leave empty for default https://skills.sh)" }

    # --- Data Persistence ---
    "data.title" = @{ zh = "--- 数据持久化 ---"; en = "--- Data Persistence ---" }
    "data.volume_prompt" = @{ zh = "Docker 卷名称 [hiclaw-data]"; en = "Docker volume name for persistent data [hiclaw-data]" }
    "data.volume_using" = @{ zh = "  使用 Docker 卷: {0}"; en = "  Using Docker volume: {0}" }

    # --- Manager Workspace ---
    "workspace.title" = @{ zh = "--- Manager 工作空间 ---"; en = "--- Manager Workspace ---" }
    "workspace.dir_prompt" = @{ zh = "Manager 工作空间目录 [{0}]"; en = "Manager workspace directory [{0}]" }
    "workspace.dir_label" = @{ zh = "  Manager 工作空间: {0}"; en = "  Manager workspace: {0}" }

    # --- Host directory sharing ---
    "host_share.prompt" = @{ zh = "与 Agent 共享的主机目录（默认: {0}）"; en = "Host directory to share with agents (default: {0})" }
    "host_share.sharing" = @{ zh = "共享主机目录: {0} -> 容器内 /host-share"; en = "Sharing host directory: {0} -> /host-share in container" }
    "host_share.not_exist" = @{ zh = "警告: 主机目录 {0} 不存在，跳过验证继续使用"; en = "WARNING: Host directory {0} does not exist, using without validation" }

    # --- Secrets and config ---
    "install.generating_secrets" = @{ zh = "正在生成密钥..."; en = "Generating secrets..." }
    "install.config_saved" = @{ zh = "配置已保存到 {0}"; en = "Configuration saved to {0}" }

    # --- Container runtime socket ---
    "install.socket_detected" = @{ zh = "容器运行时 socket: {0}（已启用直接创建 Worker）"; en = "Container runtime socket: {0} (direct Worker creation enabled)" }
    "install.socket_not_found" = @{ zh = "未找到容器运行时 socket（Worker 创建将输出命令）"; en = "No container runtime socket found (Worker creation will output commands)" }

    # --- Container management ---
    "install.removing_existing" = @{ zh = "正在移除现有 hiclaw-manager 容器..."; en = "Removing existing hiclaw-manager container..." }

    # --- Volume conflict ---
    "install.volume_conflict.title" = @{ zh = "卷冲突检测！"; en = "Volume conflict detected!" }
    "install.volume_conflict.detail" = @{ zh = "  Docker 卷 '{0}' 由另一个安装创建:"; en = "  Docker volume '{0}' was created by a different installation:" }
    "install.volume_conflict.original" = @{ zh = "    原始安装目录 : {0}"; en = "    Original install directory : {0}" }
    "install.volume_conflict.current" = @{ zh = "    当前安装目录 : {0}"; en = "    Current  install directory : {0}" }
    "install.volume_conflict.hint" = @{ zh = "  每个 HiClaw 安装必须使用独立的 Docker 卷。"; en = "  Each HiClaw installation must use its own Docker volume." }
    "install.volume_conflict.options" = @{ zh = "  选项:"; en = "  Options:" }
    "install.volume_conflict.opt1" = @{ zh = "    1. 从原始目录运行此脚本: {0}"; en = "    1. Run this script from the original directory: {0}" }
    "install.volume_conflict.opt2" = @{ zh = "    2. 使用不同的卷名: HICLAW_DATA_DIR=hiclaw-data-2 {0}"; en = "    2. Use a different volume name: HICLAW_DATA_DIR=hiclaw-data-2 {0}" }
    "install.volume_conflict.opt3" = @{ zh = "    3. 从原始目录执行全新重装（升级菜单中的选项 2）"; en = "    3. Perform a clean reinstall from the original directory (option 2 in the upgrade menu)" }

    # --- YOLO mode ---
    "install.yolo" = @{ zh = "YOLO 模式已启用（自主决策，无交互提示）"; en = "YOLO mode enabled (autonomous decisions, no interactive prompts)" }

    # --- Image pulling ---
    "install.image.exists" = @{ zh = "Manager 镜像已存在: {0}"; en = "Manager image already exists locally: {0}" }
    "install.image.pulling_manager" = @{ zh = "正在拉取 Manager 镜像: {0}"; en = "Pulling Manager image: {0}" }
    "install.image.worker_exists" = @{ zh = "Worker 镜像已存在: {0}"; en = "Worker image already exists locally: {0}" }
    "install.image.pulling_worker" = @{ zh = "正在拉取 Worker 镜像: {0}"; en = "Pulling Worker image: {0}" }

    # --- Starting container ---
    "install.starting_manager" = @{ zh = "正在启动 Manager 容器..."; en = "Starting Manager container..." }

    # --- Wait for Manager ready ---
    "install.wait_ready" = @{ zh = "等待 Manager Agent 就绪（超时: {0}s）..."; en = "Waiting for Manager agent to be ready (timeout: {0}s)..." }
    "install.wait_ready.ok" = @{ zh = "Manager Agent 已就绪！"; en = "Manager agent is ready!" }
    "install.wait_ready.waiting" = @{ zh = "等待中... ({0}s/{1}s)"; en = "Waiting... ({0}s/{1}s)" }
    "install.wait_ready.timeout" = @{ zh = "Manager Agent 在 {0}s 内未就绪。请检查: docker logs {1}"; en = "Manager agent did not become ready within {0}s. Check: docker logs {1}" }

    # --- OpenAI-compatible provider creation ---
    "install.openai_compat.missing" = @{ zh = "警告: OpenAI Base URL 或 API Key 未设置，跳过提供商创建"; en = "WARNING: OpenAI Base URL or API Key not set, skipping provider creation" }
    "install.openai_compat.creating" = @{ zh = "正在创建 OpenAI 兼容提供商..."; en = "Creating OpenAI-compatible provider..." }
    "install.openai_compat.domain" = @{ zh = "  域名: {0}"; en = "  Domain: {0}" }
    "install.openai_compat.port" = @{ zh = "  端口: {0}"; en = "  Port: {0}" }
    "install.openai_compat.protocol" = @{ zh = "  协议: {0}"; en = "  Protocol: {0}" }
    "install.openai_compat.service_fail" = @{ zh = "警告: 创建 DNS 服务源失败（可能已存在）"; en = "WARNING: Failed to create DNS service source (may already exist)" }
    "install.openai_compat.provider_fail" = @{ zh = "警告: 创建 AI 提供商失败（可能已存在）"; en = "WARNING: Failed to create AI provider (may already exist)" }
    "install.openai_compat.success" = @{ zh = "OpenAI 兼容提供商创建成功"; en = "OpenAI-compatible provider created successfully" }

    # --- Welcome message ---
    "install.welcome_msg.soul_configured" = @{ zh = "Soul 已配置（找到 soul-configured 标记），跳过 onboarding 消息"; en = "Soul already configured (soul-configured marker found), skipping onboarding message" }
    "install.welcome_msg.logging_in" = @{ zh = "正在以 {0} 身份登录以发送欢迎消息..."; en = "Logging in as {0} to send welcome message..." }
    "install.welcome_msg.login_failed" = @{ zh = "警告: 以 {0} 身份登录失败，跳过欢迎消息"; en = "WARNING: Failed to login as {0}, skipping welcome message" }
    "install.welcome_msg.finding_room" = @{ zh = "正在查找与 Manager 的 DM 房间..."; en = "Finding DM room with Manager..." }
    "install.welcome_msg.creating_room" = @{ zh = "正在创建与 Manager 的 DM 房间..."; en = "Creating DM room with Manager..." }
    "install.welcome_msg.no_room" = @{ zh = "警告: 无法找到或创建与 Manager 的 DM 房间"; en = "WARNING: Could not find or create DM room with Manager" }
    "install.welcome_msg.waiting_join" = @{ zh = "等待 Manager 加入房间..."; en = "Waiting for Manager to join the room..." }
    "install.welcome_msg.sending" = @{ zh = "正在向 Manager 发送欢迎消息..."; en = "Sending welcome message to Manager..." }
    "install.welcome_msg.send_failed" = @{ zh = "警告: 发送欢迎消息失败"; en = "WARNING: Failed to send welcome message" }
    "install.welcome_msg.sent" = @{ zh = "欢迎消息已发送给 Manager"; en = "Welcome message sent to Manager" }

    # --- Final output panel ---
    "success.title" = @{ zh = "=== HiClaw Manager 已启动！==="; en = "=== HiClaw Manager Started! ===" }
    "success.domains_configured" = @{ zh = "以下域名已配置解析到 127.0.0.1:"; en = "The following domains are configured to resolve to 127.0.0.1:" }
    "success.open_url" = @{ zh = "  ★ 在浏览器中打开以下 URL 开始使用:                           ★"; en = "  ★ Open the following URL in your browser to start:                           ★" }
    "success.login_with" = @{ zh = "  登录信息:"; en = "  Login with:" }
    "success.username" = @{ zh = "    用户名: {0}"; en = "    Username: {0}" }
    "success.password" = @{ zh = "    密码: {0}"; en = "    Password: {0}" }
    "success.after_login" = @{ zh = "  登录后，开始与 Manager 聊天！"; en = "  After login, start chatting with the Manager!" }
    "success.tell_it" = @{ zh = "    告诉它: `"创建一个名为 alice 的前端开发 Worker`""; en = "    Tell it: `"Create a Worker named alice for frontend dev`"" }
    "success.manager_auto" = @{ zh = "    Manager 会自动处理一切。"; en = "    The Manager will handle everything automatically." }
    "success.mobile_title" = @{ zh = "  📱 移动端访问（FluffyChat / Element Mobile）:"; en = "  📱 Mobile access (FluffyChat / Element Mobile):" }
    "success.mobile_step1" = @{ zh = "    1. 在手机上下载 FluffyChat 或 Element"; en = "    1. Download FluffyChat or Element on your phone" }
    "success.mobile_step2" = @{ zh = "    2. 设置 homeserver 为: {0}"; en = "    2. Set homeserver to: {0}" }
    "success.mobile_step2_noip" = @{ zh = "    2. 设置 homeserver 为: http://<本机局域网IP>:{0}"; en = "    2. Set homeserver to: http://<this-machine-LAN-IP>:{0}" }
    "success.mobile_noip_hint" = @{ zh = "       （无法自动检测局域网 IP — 请使用 ifconfig / ip addr 查看）"; en = "       (Could not detect LAN IP automatically — check with: ifconfig / ip addr)" }
    "success.mobile_step3" = @{ zh = "    3. 登录信息:"; en = "    3. Login with:" }
    "success.mobile_username" = @{ zh = "         用户名: {0}"; en = "         Username: {0}" }
    "success.mobile_password" = @{ zh = "         密码: {0}"; en = "         Password: {0}" }

    # --- Other consoles and tips ---
    "success.other_consoles" = @{ zh = "--- 其他控制台 ---"; en = "--- Other Consoles ---" }
    "success.higress_console" = @{ zh = "  Higress 控制台: http://localhost:{0}（用户名: {1} / 密码: {2}）"; en = "  Higress Console: http://localhost:{0} (Username: {1} / Password: {2})" }
    "success.switch_llm.title" = @{ zh = "--- 切换 LLM 提供商 ---"; en = "--- Switch LLM Providers ---" }
    "success.switch_llm.hint" = @{ zh = "  您可以通过 Higress 控制台切换到其他 LLM 提供商（OpenAI、Anthropic 等）。"; en = "  You can switch to other LLM providers (OpenAI, Anthropic, etc.) via Higress Console." }
    "success.switch_llm.docs" = @{ zh = "  详细说明请参阅:"; en = "  For detailed instructions, see:" }
    "success.switch_llm.url" = @{ zh = "  https://higress.ai/en/docs/ai/scene-guide/multi-proxy#console-configuration"; en = "  https://higress.ai/en/docs/ai/scene-guide/multi-proxy#console-configuration" }
    "success.tip" = @{ zh = "提示: 您也可以在聊天中让 Manager 为您配置 LLM 提供商。"; en = "Tip: You can also ask the Manager to configure LLM providers for you in the chat." }
    "success.config_file" = @{ zh = "配置文件: {0}"; en = "Configuration file: {0}" }
    "success.data_volume" = @{ zh = "数据卷:        {0}"; en = "Data volume:        {0}" }
    "success.workspace" = @{ zh = "Manager 工作空间:  {0}"; en = "Manager workspace:  {0}" }

    # --- Worker installation ---
    "worker.resetting" = @{ zh = "正在重置 Worker: {0}..."; en = "Resetting Worker: {0}..." }
    "worker.exists" = @{ zh = "容器 '{0}' 已存在。使用 --reset 重新创建。"; en = "Container '{0}' already exists. Use --reset to recreate." }
    "worker.starting" = @{ zh = "正在启动 Worker: {0}..."; en = "Starting Worker: {0}..." }
    "worker.skills_url" = @{ zh = "  Skills API URL: {0}"; en = "  Skills API URL: {0}" }
    "worker.started" = @{ zh = "=== Worker {0} 已启动！==="; en = "=== Worker {0} Started! ===" }
    "worker.container" = @{ zh = "容器: {0}"; en = "Container: {0}" }
    "worker.view_logs" = @{ zh = "查看日志: docker logs -f {0}"; en = "View logs: docker logs -f {0}" }

    # --- Prompt function messages ---
    "prompt.preset" = @{ zh = "  {0} = （已通过环境变量预设）"; en = "  {0} = (pre-set via env)" }
    "prompt.default" = @{ zh = "  {0} = {1}（默认）"; en = "  {0} = {1} (default)" }
    "prompt.required" = @{ zh = "{0} 是必需的（在非交互模式下通过环境变量设置）"; en = "{0} is required (set via environment variable in non-interactive mode)" }
    "prompt.required_empty" = @{ zh = "{0} 是必需的"; en = "{0} is required" }

    # --- Language switch prompt (bilingual by design) ---
    "lang.detected.zh" = @{ zh = "检测到语言 / Detected language: 中文"; en = "检测到语言 / Detected language: 中文" }
    "lang.detected.en" = @{ zh = "检测到语言 / Detected language: English"; en = "检测到语言 / Detected language: English" }
    "lang.switch_title" = @{ zh = "切换语言 / Switch language:"; en = "切换语言 / Switch language:" }
    "lang.option_zh" = @{ zh = "  1) 中文"; en = "  1) 中文" }
    "lang.option_en" = @{ zh = "  2) English"; en = "  2) English" }
    "lang.prompt" = @{ zh = "请选择 / Enter choice"; en = "请选择 / Enter choice" }

    # --- Error messages ---
    "error.name_required" = @{ zh = "--name 是必需的"; en = "--name is required" }
    "error.fs_required" = @{ zh = "--fs 是必需的"; en = "--fs is required" }
    "error.fs_key_required" = @{ zh = "--fs-key 是必需的"; en = "--fs-key is required" }
    "error.fs_secret_required" = @{ zh = "--fs-secret 是必需的"; en = "--fs-secret is required" }
    "error.unknown_option" = @{ zh = "未知选项: {0}"; en = "Unknown option: {0}" }
    "error.docker_not_running" = @{ zh = "Docker 未运行。请先启动 Docker Desktop 或 Podman Desktop。"; en = "Docker is not running. Please start Docker Desktop or Podman Desktop first." }
    "error.docker_not_found" = @{ zh = "未找到 docker 或 podman 命令。请先安装 Docker Desktop 或 Podman Desktop：`n  Docker Desktop: https://www.docker.com/products/docker-desktop/`n  Podman Desktop: https://podman-desktop.io/"; en = "docker or podman command not found. Please install Docker Desktop or Podman Desktop first:`n  Docker Desktop: https://www.docker.com/products/docker-desktop/`n  Podman Desktop: https://podman-desktop.io/" }

    # --- Uninstall messages ---
    "uninstall.title" = @{ zh = "正在卸载 HiClaw..."; en = "Uninstalling HiClaw..." }
    "uninstall.stopping_manager" = @{ zh = "正在停止并移除 hiclaw-manager..."; en = "Stopping and removing hiclaw-manager..." }
    "uninstall.stopping_workers" = @{ zh = "正在停止并移除 worker 容器..."; en = "Stopping and removing worker containers..." }
    "uninstall.removed" = @{ zh = "  已移除: {0}"; en = "  Removed: {0}" }
    "uninstall.removing_volume" = @{ zh = "正在移除 Docker 卷: hiclaw-data"; en = "Removing Docker volume: hiclaw-data" }
    "uninstall.removing_env" = @{ zh = "正在移除 env 文件: {0}"; en = "Removing env file: {0}" }
    "uninstall.done" = @{ zh = "HiClaw 已卸载。"; en = "HiClaw has been uninstalled." }
    "uninstall.workspace_note" = @{ zh = "注意: Manager 工作空间目录已保留。如需删除请手动操作。"; en = "Note: Manager workspace directory was preserved. Remove manually if desired." }
}

# Get-Msg: look up message by key, with -f style argument substitution.
# Falls back to English if the current language translation is missing.
function Get-Msg {
    param(
        [Parameter(Mandatory)][string]$Key,
        [object[]]$f
    )
    $lang = $script:HICLAW_LANGUAGE
    if (-not $lang) { $lang = "en" }
    $entry = $script:Messages[$Key]
    if (-not $entry) { return $Key }
    $text = $entry[$lang]
    if (-not $text) { $text = $entry["en"] }
    if (-not $text) { return $Key }
    if ($f) { return ($text -f $f) }
    return $text
}
function Get-LanIP {
    # Detect local LAN IP address on Windows
    try {
        # Get network adapters with IPv4 addresses, prefer connected/active interfaces
        $adapters = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
            Where-Object {
                $_.IPAddress -notlike "127.*" -and
                $_.PrefixOrigin -ne "WellKnown" -and
                $_.InterfaceAlias -notlike "*Loopback*"
            } |
            Sort-Object { $_.InterfaceAlias -like "*Wi-Fi*" -or $_.InterfaceAlias -like "*Ethernet*" ? 0 : 1 }

        if ($adapters) {
            return $adapters[0].IPAddress
        }

        # Fallback: use ipconfig
        $ipconfig = ipconfig 2>$null
        $ip = ($ipconfig | Select-String "IPv4 Address.*?: (\d+\.\d+\.\d+\.\d+)" | Select-Object -First 1)
        if ($ip -match "(\d+\.\d+\.\d+\.\d+)") {
            return $Matches[1]
        }
    }
    catch {
        # Ignore errors
    }

    return ""
}

function New-RandomKey {
    # Generate 64 character hex string (32 bytes)
    $bytes = [byte[]]::new(32)
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    return [BitConverter]::ToString($bytes).Replace("-", "").ToLower()
}

function ConvertTo-DockerPath {
    param([string]$Path)

    # Convert Windows path to Docker mount format
    $fullPath = (Resolve-Path $Path -ErrorAction SilentlyContinue).Path
    if (-not $fullPath) {
        $fullPath = $Path
    }

    # Convert C:\path to /c/path format for Docker
    if ($fullPath -match "^([A-Za-z]):") {
        $drive = $Matches[1].ToLower()
        $rest = $fullPath.Substring(2).Replace("\", "/")
        return "/$drive$rest"
    }
    return $fullPath.Replace("\", "/")
}

function Wait-ManagerReady {
    param(
        [string]$Container = "hiclaw-manager",
        [int]$Timeout = 300
    )

    $elapsed = 0
    Write-Log (Get-Msg "install.wait_ready" -f $Timeout)

    while ($elapsed -lt $Timeout) {
        try {
            $result = docker exec $Container openclaw gateway health --json 2>$null
            if ($result -match '"ok"') {
                Write-Log (Get-Msg "install.wait_ready.ok")
                return $true
            }
        } catch {
            # Ignore errors during polling
        }

        Start-Sleep -Seconds 5
        $elapsed += 5
        Write-Host "`r`e[36m[HiClaw]`e[0m $(Get-Msg 'install.wait_ready.waiting' -f $elapsed, $Timeout)" -NoNewline
    }

    Write-Host ""
    Write-Error (Get-Msg "install.wait_ready.timeout" -f $Timeout, $Container)
}

function New-EnvFile {
    param([hashtable]$Config, [string]$Path)

    $content = @"
# HiClaw Manager Configuration
# Generated by hiclaw-install.ps1 on $(Get-Date -Format "yyyy-MM-dd HH:mm:ss")

# Language
HICLAW_LANGUAGE=$($Config.LANGUAGE)

# LLM
HICLAW_LLM_PROVIDER=$($Config.LLM_PROVIDER)
HICLAW_DEFAULT_MODEL=$($Config.DEFAULT_MODEL)
HICLAW_LLM_API_KEY=$($Config.LLM_API_KEY)
HICLAW_OPENAI_BASE_URL=$($Config.OPENAI_BASE_URL)

# Admin
HICLAW_ADMIN_USER=$($Config.ADMIN_USER)
HICLAW_ADMIN_PASSWORD=$($Config.ADMIN_PASSWORD)

# Ports
HICLAW_PORT_GATEWAY=$($Config.PORT_GATEWAY)
HICLAW_PORT_CONSOLE=$($Config.PORT_CONSOLE)
HICLAW_PORT_ELEMENT_WEB=$($Config.PORT_ELEMENT_WEB)

# Matrix
HICLAW_MATRIX_DOMAIN=$($Config.MATRIX_DOMAIN)
HICLAW_MATRIX_CLIENT_DOMAIN=$($Config.MATRIX_CLIENT_DOMAIN)

# Gateway
HICLAW_AI_GATEWAY_DOMAIN=$($Config.AI_GATEWAY_DOMAIN)
HICLAW_MANAGER_GATEWAY_KEY=$($Config.MANAGER_GATEWAY_KEY)

# File System
HICLAW_FS_DOMAIN=$($Config.FS_DOMAIN)
HICLAW_MINIO_USER=$($Config.MINIO_USER)
HICLAW_MINIO_PASSWORD=$($Config.MINIO_PASSWORD)

# Internal
HICLAW_MANAGER_PASSWORD=$($Config.MANAGER_PASSWORD)
HICLAW_REGISTRATION_TOKEN=$($Config.REGISTRATION_TOKEN)

# GitHub (optional)
HICLAW_GITHUB_TOKEN=$($Config.GITHUB_TOKEN)

# Skills Registry (optional, default: https://skills.sh)
HICLAW_SKILLS_API_URL=$($Config.SKILLS_API_URL)

# Worker image (for direct container creation)
HICLAW_WORKER_IMAGE=$($Config.WORKER_IMAGE)

# Higress WASM plugin image registry (auto-selected by timezone)
HIGRESS_ADMIN_WASM_PLUGIN_IMAGE_REGISTRY=$($Config.REGISTRY)

# Data persistence
HICLAW_DATA_DIR=$($Config.DATA_DIR)
# Manager workspace (skills, memory, state - host-editable)
HICLAW_WORKSPACE_DIR=$($Config.WORKSPACE_DIR)
# Host directory sharing
HICLAW_HOST_SHARE_DIR=$($Config.HOST_SHARE_DIR)
"@

    Set-Content -Path $Path -Value $content -Encoding UTF8
    Write-Log (Get-Msg "install.config_saved" -f $Path)
}

# ============================================================
# Prompt Functions
# ============================================================

function Read-Prompt {
    param(
        [string]$VarName,
        [string]$PromptText,
        [string]$Default = "",
        [switch]$Secret,
        [switch]$Optional
    )

    # Check if already set in environment
    $envValue = [Environment]::GetEnvironmentVariable($VarName)
    if ($envValue) {
        Write-Log (Get-Msg "prompt.preset" -f $VarName)
        return $envValue
    }

    # Non-interactive or quickstart mode
    if ($script:HICLAW_NON_INTERACTIVE -or $script:HICLAW_QUICKSTART) {
        if ($Default) {
            Write-Log (Get-Msg "prompt.default" -f $VarName, $Default)
            return $Default
        }
        elseif ($Optional) {
            return ""
        }
        elseif ($script:HICLAW_NON_INTERACTIVE) {
            # Only hard-error in fully non-interactive mode, not quickstart
            Write-Error (Get-Msg "prompt.required" -f $VarName)
        }
        # quickstart + no default + not optional: fall through to interactive prompt
    }

    # Interactive prompt
    $prompt = if ($Default) { "$PromptText [$Default]" } else { $PromptText }

    if ($Secret) {
        $value = Read-Host -Prompt $prompt -AsSecureString
        $value = [System.Runtime.InteropServices.Marshal]::PtrToStringAuto(
            [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($value)
        )
    }
    else {
        $value = Read-Host -Prompt $prompt
    }

    if (-not $value -and $Default) {
        $value = $Default
    }

    if (-not $value -and -not $Optional) {
        Write-Error (Get-Msg "prompt.required_empty" -f $VarName)
    }

    return $value
}

# ============================================================
# OpenAI-Compatible Provider
# ============================================================

function New-OpenAICompatProvider {
    param(
        [string]$BaseUrl,
        [string]$ApiKey,
        [int]$ConsolePort = 18001
    )

    if (-not $BaseUrl -or -not $ApiKey) {
        Write-Log (Get-Msg "install.openai_compat.missing")
        return $false
    }

    $consoleUrl = "http://localhost:$ConsolePort"

    # Parse base URL
    $protocol = "https"
    $port = 443
    $urlWithoutProto = $BaseUrl -replace "^https?://", ""

    if ($BaseUrl -match "^http://") {
        $protocol = "http"
        $port = 80
    }

    $domain = $urlWithoutProto.Split("/")[0]

    if ($domain -match ":(\d+)$") {
        $port = [int]$Matches[1]
        $domain = $domain -replace ":\d+$", ""
    }

    Write-Log (Get-Msg "install.openai_compat.creating")
    Write-Log (Get-Msg "install.openai_compat.domain" -f $domain)
    Write-Log (Get-Msg "install.openai_compat.port" -f $port)
    Write-Log (Get-Msg "install.openai_compat.protocol" -f $protocol)

    $serviceName = "openai-compat"

    # Create DNS service source
    $serviceBody = @{
        type = "dns"
        name = $serviceName
        port = $port.ToString()
        protocol = $protocol
        proxyName = ""
        domain = $domain
    } | ConvertTo-Json -Compress

    try {
        Invoke-RestMethod -Uri "$consoleUrl/v1/service-sources" -Method POST -ContentType "application/json" -Body $serviceBody -ErrorAction SilentlyContinue | Out-Null
    }
    catch {
        Write-Log (Get-Msg "install.openai_compat.service_fail")
    }

    Start-Sleep -Seconds 2

    # Create AI provider
    $providerBody = @{
        type = "openai"
        name = "openai-compat"
        tokens = @($ApiKey)
        version = 0
        protocol = "openai/v1"
        tokenFailoverConfig = @{ enabled = $false }
        rawConfigs = @{
            openaiCustomUrl = $BaseUrl
            openaiCustomServiceName = "$serviceName.dns"
            openaiCustomServicePort = $port
        }
    } | ConvertTo-Json -Compress -Depth 3

    try {
        Invoke-RestMethod -Uri "$consoleUrl/v1/ai/providers" -Method POST -ContentType "application/json" -Body $providerBody -ErrorAction SilentlyContinue | Out-Null
        Write-Log (Get-Msg "install.openai_compat.success")
        return $true
    }
    catch {
        Write-Log (Get-Msg "install.openai_compat.provider_fail")
        return $false
    }
}

# ============================================================
# Welcome Message
# ============================================================

function Send-WelcomeMessage {
    param(
        [string]$Container = "hiclaw-manager",
        [string]$AdminUser,
        [string]$AdminPassword,
        [string]$MatrixDomain,
        [string]$Timezone,
        [string]$Language
    )

    # Skip if soul already configured
    $soulConfigured = docker exec $Container test -f /root/manager-workspace/soul-configured 2>$null
    if ($LASTEXITCODE -eq 0) {
        Write-Log (Get-Msg "install.welcome_msg.soul_configured")
        return $true
    }

    $matrixUrl = "http://127.0.0.1:6167"
    $managerUser = "manager"
    $managerFullId = "@${managerUser}:${MatrixDomain}"

    # Login to get admin access token
    Write-Log (Get-Msg "install.welcome_msg.logging_in" -f $AdminUser)

    $loginBody = @{
        type = "m.login.password"
        identifier = @{ type = "m.id.user"; user = $AdminUser }
        password = $AdminPassword
    } | ConvertTo-Json -Compress

    try {
        $loginResp = docker exec $Container curl -sf -X POST "$matrixUrl/_matrix/client/v3/login" `
            -H "Content-Type: application/json" `
            -d $loginBody 2>$null

        $accessToken = ($loginResp | ConvertFrom-Json).access_token
        if (-not $accessToken) {
            Write-Log (Get-Msg "install.welcome_msg.login_failed" -f $AdminUser)
            return $false
        }
    }
    catch {
        Write-Log (Get-Msg "install.welcome_msg.login_failed" -f $AdminUser)
        return $false
    }

    # Find or create DM room
    Write-Log (Get-Msg "install.welcome_msg.finding_room")

    try {
        $roomsResp = docker exec $Container curl -sf "$matrixUrl/_matrix/client/v3/joined_rooms" `
            -H "Authorization: Bearer $accessToken" 2>$null
        $rooms = ($roomsResp | ConvertFrom-Json).joined_rooms
    }
    catch {
        $rooms = @()
    }

    $roomId = $null
    foreach ($rid in $rooms) {
        try {
            $membersResp = docker exec $Container curl -sf "$matrixUrl/_matrix/client/v3/rooms/$rid/members" `
                -H "Authorization: Bearer $accessToken" 2>$null
            $members = ($membersResp | ConvertFrom-Json).chunk.state_key

            if ($members.Count -eq 2 -and $members -match "@${managerUser}:") {
                $roomId = $rid
                break
            }
        } catch {
            continue
        }
    }

    if (-not $roomId) {
        Write-Log (Get-Msg "install.welcome_msg.creating_room")
        $createBody = @{
            is_direct = $true
            invite = @($managerFullId)
            preset = "trusted_private_chat"
        } | ConvertTo-Json -Compress

        try {
            $createResp = docker exec $Container curl -sf -X POST "$matrixUrl/_matrix/client/v3/createRoom" `
                -H "Authorization: Bearer $accessToken" `
                -H "Content-Type: application/json" `
                -d $createBody 2>$null
            $roomId = ($createResp | ConvertFrom-Json).room_id
        } catch {
            Write-Log (Get-Msg "install.welcome_msg.no_room")
            return $false
        }
    }

    if (-not $roomId) {
        Write-Log (Get-Msg "install.welcome_msg.no_room")
        return $false
    }

    # Wait for Manager to join
    Write-Log (Get-Msg "install.welcome_msg.waiting_join")
    $waitElapsed = 0
    $waitTimeout = 60

    while ($waitElapsed -lt $waitTimeout) {
        try {
            $membersResp = docker exec $Container curl -sf "$matrixUrl/_matrix/client/v3/rooms/$roomId/members" `
                -H "Authorization: Bearer $accessToken" 2>$null
            $members = ($membersResp | ConvertFrom-Json).chunk.state_key

            if ($members -match [regex]::Escape($managerFullId)) {
                break
            }
        } catch {
            # Continue waiting
        }

        Start-Sleep -Seconds 2
        $waitElapsed += 2
    }

    # Send welcome message
    Write-Log (Get-Msg "install.welcome_msg.sending")

    $welcomeMsg = @"
This is an automated message from the HiClaw installation script. This is a fresh installation.

--- Installation Context ---
User Language: $Language  (zh = Chinese, en = English)
User Timezone: $Timezone  (IANA timezone identifier)
---

You are an AI agent that manages a team of worker agents. Your identity and personality have not been configured yet — the human admin is about to meet you for the first time.

Please begin the onboarding conversation:

1. Greet the admin warmly and briefly describe what you can do (coordinate workers, manage tasks, run multi-agent projects) — without referring to yourself by any specific title yet
2. The user has selected "$Language" as their preferred language during installation. Use this language for your greeting and all subsequent communication.
3. The user's timezone is $Timezone. Based on this timezone, you may infer their likely region and suggest additional language options (e.g., Japanese, Korean, German, etc.) that they might prefer for future interactions.
4. Ask them the following questions (one message is fine):
   a. What would they like to call you? (name or title)
   b. What communication style do they prefer? (e.g. formal, casual, concise, detailed)
   c. Any specific behavior guidelines or constraints they want you to follow?
   d. Confirm the default language they want you to use (offer alternatives based on timezone)
5. After they reply, write their preferences to the "Identity & Personality" section of ~/SOUL.md — replace the "(not yet configured)" placeholder with the configured identity
6. Confirm what you wrote, and ask if they would like to adjust anything
7. Once the admin confirms the identity is set, run: touch ~/soul-configured

The human admin will start chatting shortly.
"@

    $txnId = "welcome-$(Get-Date -UFormat %s)"
    $msgBody = @{
        msgtype = "m.text"
        body = $welcomeMsg
    } | ConvertTo-Json -Compress

    try {
        docker exec $Container curl -sf -X PUT "$matrixUrl/_matrix/client/v3/rooms/$roomId/send/m.room.message/$txnId" `
            -H "Authorization: Bearer $accessToken" `
            -H "Content-Type: application/json" `
            -d $msgBody 2>$null | Out-Null

        Write-Log (Get-Msg "install.welcome_msg.sent")
        return $true
    }
    catch {
        Write-Log (Get-Msg "install.welcome_msg.send_failed")
        return $false
    }
}

# ============================================================
# Manager Installation
# ============================================================

function Install-Manager {
    Write-Log (Get-Msg "install.title")

    # Detect timezone
    $script:HICLAW_TIMEZONE =  { $(Get-HiClawTimeZone) } 

    # Language priority: env var > existing env file > timezone detection
    if ($env:HICLAW_LANGUAGE) {
        $script:HICLAW_LANGUAGE = $env:HICLAW_LANGUAGE
    } else {
        # Check existing env file for saved language preference (upgrade scenario)
        $_envFile = $script:HICLAW_ENV_FILE
        # Migrate from legacy location (current directory) if needed
        if (-not (Test-Path $_envFile) -and (Test-Path ".\hiclaw-manager.env")) {
            Write-Log "Migrating hiclaw-manager.env to $_envFile..."
            Move-Item ".\hiclaw-manager.env" $_envFile -ErrorAction SilentlyContinue
        }
        if (Test-Path $_envFile) {
            $_savedLang = (Get-Content $_envFile | Select-String "^HICLAW_LANGUAGE=" | ForEach-Object {
                $_.Line -replace '^HICLAW_LANGUAGE=', ''
            } | Select-Object -First 1)
            if ($_savedLang) {
                $script:HICLAW_LANGUAGE = $_savedLang
            }
        }
        # Fall back to timezone-based detection
        if (-not $script:HICLAW_LANGUAGE) {
            $script:HICLAW_LANGUAGE = Get-HiClawLanguage -Timezone $script:HICLAW_TIMEZONE
        }
    }
    $env:HICLAW_LANGUAGE = $script:HICLAW_LANGUAGE

    # Language switch interaction (skip in non-interactive mode)
    if (-not $script:HICLAW_NON_INTERACTIVE) {
        # Determine default choice based on current detected language
        $langDefaultChoice = if ($script:HICLAW_LANGUAGE -eq "zh") { "1" } else { "2" }

        $langDetectedKey = "lang.detected.$($script:HICLAW_LANGUAGE)"
        Write-Log (Get-Msg $langDetectedKey)
        Write-Log (Get-Msg "lang.switch_title")
        Write-Host (Get-Msg "lang.option_zh")
        Write-Host (Get-Msg "lang.option_en")
        Write-Host ""
        $langChoice = Read-Host "$(Get-Msg 'lang.prompt') [$langDefaultChoice]"
        if (-not $langChoice) { $langChoice = $langDefaultChoice }

        switch ($langChoice) {
            "1" { $script:HICLAW_LANGUAGE = "zh" }
            "2" { $script:HICLAW_LANGUAGE = "en" }
            default {
                # Invalid input - keep current detected language
            }
        }
        $env:HICLAW_LANGUAGE = $script:HICLAW_LANGUAGE
        Write-Log ""
    }

    # Detect registry
    $script:HICLAW_REGISTRY = Get-Registry -Timezone $script:HICLAW_TIMEZONE

    # Set image names
    $script:MANAGER_IMAGE = if ($env:HICLAW_INSTALL_MANAGER_IMAGE) {
        $env:HICLAW_INSTALL_MANAGER_IMAGE
    } else {
        "$($script:HICLAW_REGISTRY)/higress/hiclaw-manager:$($script:HICLAW_VERSION)"
    }

    $script:WORKER_IMAGE = if ($env:HICLAW_INSTALL_WORKER_IMAGE) {
        $env:HICLAW_INSTALL_WORKER_IMAGE
    } else {
        "$($script:HICLAW_REGISTRY)/higress/hiclaw-worker:$($script:HICLAW_VERSION)"
    }

    Write-Log (Get-Msg "install.registry" -f $script:HICLAW_REGISTRY)
    Write-Log ""
    Write-Log (Get-Msg "install.dir" -f (Get-Location))
    Write-Log (Get-Msg "install.dir_hint")
    Write-Log (Get-Msg "install.dir_hint2")
    Write-Log ""

    # Check container runtime (docker or podman)
    $dockerCmd = $null
    if (Get-Command "docker" -ErrorAction SilentlyContinue) {
        $dockerCmd = "docker"
    } elseif (Get-Command "podman" -ErrorAction SilentlyContinue) {
        $dockerCmd = "podman"
    } else {
        Write-Host "`e[31m[HiClaw ERROR]`e[0m $(Get-Msg 'error.docker_not_found')" -ForegroundColor Red
        exit 1
    }

    if (-not (Test-DockerRunning)) {
        Write-Host "`e[31m[HiClaw ERROR]`e[0m $(Get-Msg 'error.docker_not_running')" -ForegroundColor Red
        exit 1
    }

    # Initialize config hashtable
    $config = @{}

    # Onboarding mode selection
    if (-not $script:HICLAW_NON_INTERACTIVE) {
        Write-Log (Get-Msg "install.mode.title")
        Write-Host ""
        Write-Host (Get-Msg "install.mode.choose")
        Write-Host (Get-Msg "install.mode.quickstart")
        Write-Host (Get-Msg "install.mode.manual")
        Write-Host ""

        $choice = Read-Host (Get-Msg "install.mode.prompt")
        $choice = if ($choice) { $choice } else { "1" }

        switch -Regex ($choice) {
            "^(1|quick|quickstart)$" {
                Write-Log (Get-Msg "install.mode.quickstart_selected")
                $script:HICLAW_QUICKSTART = $true
            }
            "^(2|manual)$" {
                Write-Log (Get-Msg "install.mode.manual_selected")
                $script:HICLAW_QUICKSTART = $false
            }
            default {
                Write-Log (Get-Msg "install.mode.invalid")
                $script:HICLAW_QUICKSTART = $true
            }
        }
        Write-Log ""
    }

    # Check for existing installation
    # Migrate from legacy location (current directory) if needed
    if (-not (Test-Path $script:HICLAW_ENV_FILE) -and (Test-Path ".\hiclaw-manager.env")) {
        Write-Log "Migrating hiclaw-manager.env to $($script:HICLAW_ENV_FILE)..."
        Move-Item ".\hiclaw-manager.env" $script:HICLAW_ENV_FILE -ErrorAction SilentlyContinue
    }
    if (Test-Path $script:HICLAW_ENV_FILE) {
        Write-Log (Get-Msg "install.existing.detected" -f $script:HICLAW_ENV_FILE)

        # Check for running containers
        $runningManager = docker ps --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$"
        $runningWorkers = docker ps --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-"
        $existingWorkers = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-"

        if ($script:HICLAW_NON_INTERACTIVE) {
            Write-Log (Get-Msg "install.existing.upgrade_noninteractive")
            $upgradeChoice = "1"
        }
        else {
            Write-Host ""
            Write-Host (Get-Msg "install.existing.choose")
            Write-Host (Get-Msg "install.existing.upgrade")
            Write-Host (Get-Msg "install.existing.reinstall")
            Write-Host (Get-Msg "install.existing.cancel")
            Write-Host ""

            $upgradeChoice = Read-Host (Get-Msg "install.existing.prompt")
            $upgradeChoice = if ($upgradeChoice) { $upgradeChoice } else { "1" }
        }

        switch -Regex ($upgradeChoice) {
            "^(1|upgrade)$" {
                Write-Log (Get-Msg "install.existing.upgrading")

                if ($runningManager -or $runningWorkers) {
                    Write-Host ""
                    Write-Host "`e[33m$(Get-Msg 'install.existing.warn_manager_stop')`e[0m"
                    if ($existingWorkers) {
                        Write-Host "`e[33m$(Get-Msg 'install.existing.warn_worker_recreate')`e[0m"
                    }

                    if (-not $script:HICLAW_NON_INTERACTIVE) {
                        $confirm = Read-Host (Get-Msg "install.existing.continue_prompt")
                        if ($confirm -ne "y" -and $confirm -ne "Y") {
                            Write-Log (Get-Msg "install.existing.cancelled")
                            exit 0
                        }
                    }
                }

                # Stop and remove containers
                if ($runningManager -or (docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$")) {
                    Write-Log (Get-Msg "install.existing.stopping_manager")
                    docker stop hiclaw-manager *>$null
                    docker rm hiclaw-manager *>$null
                }

                if ($existingWorkers) {
                    Write-Log (Get-Msg "install.existing.stopping_workers")
                    $existingWorkers | ForEach-Object {
                        docker stop $_ *>$null
                        docker rm $_ *>$null
                        Write-Log (Get-Msg "install.existing.removed" -f $_)
                    }
                }
                break
            }
            "^(2|reinstall)$" {
                Write-Log (Get-Msg "install.reinstall.performing")

                # Get existing workspace
                $existingWorkspace = "$env:USERPROFILE\hiclaw-manager"
                if (Test-Path $script:HICLAW_ENV_FILE) {
                    $envContent = Get-Content $script:HICLAW_ENV_FILE
                    $wsLine = $envContent | Select-String "^HICLAW_WORKSPACE_DIR="
                    if ($wsLine) {
                        $existingWorkspace = $wsLine.Line.Substring(21)
                    }
                }

                Write-Host ""
                Write-Host "`e[33m$(Get-Msg 'install.reinstall.warn_stop')`e[0m"
                if ($runningManager) { Write-Host "`e[33m   - hiclaw-manager (manager)`e[0m" }
                $runningWorkers | ForEach-Object { Write-Host "`e[33m   - $_ (worker)`e[0m" }

                Write-Host ""
                Write-Host "`e[31m$(Get-Msg 'install.reinstall.warn_delete')`e[0m"
                Write-Host "`e[31m$(Get-Msg 'install.reinstall.warn_volume')`e[0m"
                Write-Host "`e[31m$(Get-Msg 'install.reinstall.warn_env' -f $script:HICLAW_ENV_FILE)`e[0m"
                Write-Host "`e[31m$(Get-Msg 'install.reinstall.warn_workspace' -f $existingWorkspace)`e[0m"
                Write-Host "`e[31m$(Get-Msg 'install.reinstall.warn_workers')`e[0m"
                Write-Host ""
                Write-Host "`e[31m$(Get-Msg 'install.reinstall.confirm_type')`e[0m"
                Write-Host "`e[31m  $existingWorkspace`e[0m"
                Write-Host ""

                $confirmPath = Read-Host (Get-Msg "install.reinstall.confirm_path")

                if ($confirmPath -ne $existingWorkspace) {
                    Write-Error (Get-Msg "install.reinstall.path_mismatch" -f $confirmPath, $existingWorkspace)
                }

                Write-Log (Get-Msg "install.reinstall.confirmed")

                # Stop and remove all containers
                docker stop hiclaw-manager *>$null
                docker rm hiclaw-manager *>$null

                docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-" | ForEach-Object {
                    docker stop $_ *>$null
                    docker rm $_ *>$null
                    Write-Log (Get-Msg "install.reinstall.removed_worker" -f $_)
                }

                # Remove Docker volume
                if (docker volume ls -q 2>$null | Select-String "^hiclaw-data$") {
                    Write-Log (Get-Msg "install.reinstall.removing_volume")
                    docker volume rm hiclaw-data *>$null
                }

                # Remove workspace
                if (Test-Path $existingWorkspace) {
                    Write-Log (Get-Msg "install.reinstall.removing_workspace" -f $existingWorkspace)
                    Remove-Item -Recurse -Force $existingWorkspace
                }

                # Remove env file
                if (Test-Path $script:HICLAW_ENV_FILE) {
                    Write-Log (Get-Msg "install.reinstall.removing_env" -f $script:HICLAW_ENV_FILE)
                    Remove-Item -Force $script:HICLAW_ENV_FILE
                }

                Write-Log (Get-Msg "install.reinstall.cleanup_done")
                break
            }
            "^(3|cancel|.*)$" {
                Write-Log (Get-Msg "install.existing.cancelled")
                exit 0
            }
        }

        # Load existing env file
        if (Test-Path $script:HICLAW_ENV_FILE) {
            Write-Log (Get-Msg "install.loading_config" -f $script:HICLAW_ENV_FILE)
            Get-Content $script:HICLAW_ENV_FILE | ForEach-Object {
                if ($_ -match "^([^#=][^=]*)=(.*)$") {
                    $key = $Matches[1].Trim()
                    $value = $Matches[2].Split("#")[0].Trim()

                    # Only set if not already in environment
                    if (-not [Environment]::GetEnvironmentVariable($key)) {
                        [Environment]::SetEnvironmentVariable($key, $value, "Process")
                    }
                }
            }
        }
    }

    # LLM Configuration
    Write-Log (Get-Msg "llm.title")

    if ($script:HICLAW_NON_INTERACTIVE) {
        # Non-interactive mode: use qwen defaults
        $config.LLM_PROVIDER = if ($env:HICLAW_LLM_PROVIDER) { $env:HICLAW_LLM_PROVIDER } else { "qwen" }
        $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }
        $config.OPENAI_BASE_URL = if ($env:HICLAW_OPENAI_BASE_URL) { $env:HICLAW_OPENAI_BASE_URL } else { "" }

        Write-Log (Get-Msg "llm.provider.label" -f $config.LLM_PROVIDER)
        Write-Log (Get-Msg "llm.model.label" -f $config.DEFAULT_MODEL)
        Write-Log ""
        $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText (Get-Msg "llm.apikey_prompt") -Secret
    }
    else {
        # Both Quick Start and Manual: show two-level provider menu
        Write-Host ""
        Write-Host (Get-Msg "llm.providers_title")
        Write-Host (Get-Msg "llm.provider.alibaba")
        Write-Host (Get-Msg "llm.provider.openai_compat")
        Write-Host ""

        if ($script:HICLAW_QUICKSTART) {
            $providerChoice = Read-Host "$(Get-Msg 'llm.provider.select') [1]"
        } else {
            $providerChoice = Read-Host (Get-Msg "llm.provider.select")
        }
        $providerChoice = if ($providerChoice) { $providerChoice } else { "1" }

        switch -Regex ($providerChoice) {
            "^(1|alibaba-cloud)$" {
                # Sub-menu: CodingPlan or qwen general
                Write-Host ""
                Write-Host (Get-Msg "llm.alibaba.models_title")
                Write-Host (Get-Msg "llm.alibaba.model.codingplan")
                Write-Host (Get-Msg "llm.alibaba.model.qwen")
                Write-Host ""

                if ($script:HICLAW_QUICKSTART) {
                    $modelChoice = Read-Host "$(Get-Msg 'llm.alibaba.model.select') [1]"
                } else {
                    $modelChoice = Read-Host (Get-Msg "llm.alibaba.model.select")
                }
                $modelChoice = if ($modelChoice) { $modelChoice } else { "1" }

                if ($modelChoice -match "^(2|qwen)$") {
                    $config.LLM_PROVIDER = "qwen"
                    $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }
                    $config.OPENAI_BASE_URL = ""
                    Write-Log (Get-Msg "llm.provider.selected_qwen")
                } else {
                    $config.LLM_PROVIDER = "openai-compat"
                    $config.OPENAI_BASE_URL = if ($env:HICLAW_OPENAI_BASE_URL) { $env:HICLAW_OPENAI_BASE_URL } else { "https://coding.dashscope.aliyuncs.com/v1" }
                    $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }
                    Write-Log (Get-Msg "llm.provider.selected_codingplan")
                }

                Write-Log (Get-Msg "llm.model.label" -f $config.DEFAULT_MODEL)
                Write-Log ""
                Write-Log (Get-Msg "llm.apikey_hint")
                Write-Log (Get-Msg "llm.apikey_url")
                Write-Log ""
                $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText (Get-Msg "llm.apikey_prompt") -Secret
            }
            "^(2|openai-compat)$" {
                $config.LLM_PROVIDER = "openai-compat"
                Write-Log (Get-Msg "llm.provider.selected_openai" -f $config.LLM_PROVIDER)
                Write-Host ""

                $config.OPENAI_BASE_URL = Read-Host (Get-Msg "llm.openai.base_url_prompt")
                $modelInput = Read-Host (Get-Msg "llm.openai.model_prompt")
                $config.DEFAULT_MODEL = if ($modelInput) { $modelInput } else { "gpt-4o" }

                Write-Log (Get-Msg "llm.openai.base_url_label" -f $config.OPENAI_BASE_URL)
                Write-Log (Get-Msg "llm.model.label" -f $config.DEFAULT_MODEL)
                Write-Log ""
                $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText (Get-Msg "llm.apikey_prompt") -Secret
            }
            default {
                Write-Log (Get-Msg "llm.provider.invalid")
                $config.LLM_PROVIDER = "openai-compat"
                $config.OPENAI_BASE_URL = if ($env:HICLAW_OPENAI_BASE_URL) { $env:HICLAW_OPENAI_BASE_URL } else { "https://coding.dashscope.aliyuncs.com/v1" }
                $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }
                Write-Log (Get-Msg "llm.provider.selected_codingplan")
                Write-Log (Get-Msg "llm.model.label" -f $config.DEFAULT_MODEL)
                Write-Log ""
                Write-Log (Get-Msg "llm.apikey_hint")
                Write-Log (Get-Msg "llm.apikey_url")
                Write-Log ""
                $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText (Get-Msg "llm.apikey_prompt") -Secret
            }
        }
    }

    Write-Log ""

    # Admin Credentials
    Write-Log (Get-Msg "admin.title")
    $config.ADMIN_USER = Read-Prompt -VarName "HICLAW_ADMIN_USER" -PromptText (Get-Msg "admin.username_prompt") -Default "admin"

    if (-not $env:HICLAW_ADMIN_PASSWORD) {
        $config.ADMIN_PASSWORD = Read-Prompt -VarName "HICLAW_ADMIN_PASSWORD" -PromptText (Get-Msg "admin.password_prompt") -Secret -Optional

        if (-not $config.ADMIN_PASSWORD) {
            $randomSuffix = (New-RandomKey).Substring(0, 12)
            $config.ADMIN_PASSWORD = "admin$randomSuffix"
            Write-Log (Get-Msg "admin.password_generated")
        }
    }
    else {
        $config.ADMIN_PASSWORD = $env:HICLAW_ADMIN_PASSWORD
        Write-Log (Get-Msg "prompt.preset" -f "HICLAW_ADMIN_PASSWORD")
    }

    # Validate password length
    if ($config.ADMIN_PASSWORD.Length -lt 8) {
        Write-Error (Get-Msg "admin.password_too_short" -f $config.ADMIN_PASSWORD.Length)
    }

    Write-Log ""

    # Network Access Mode
    Write-Log (Get-Msg "port.local_only.title")
    Write-Host ""
    Write-Host "  1) $(Get-Msg 'port.local_only.hint_yes')"
    Write-Host "  2) $(Get-Msg 'port.local_only.hint_no')"
    Write-Host ""
    if ($script:HICLAW_NON_INTERACTIVE -eq "1" -or $script:HICLAW_QUICKSTART) {
        $localOnly = if ($env:HICLAW_LOCAL_ONLY) { $env:HICLAW_LOCAL_ONLY } else { "1" }
    } elseif ($null -ne $env:HICLAW_LOCAL_ONLY) {
        $localOnly = $env:HICLAW_LOCAL_ONLY
    } else {
        $localChoice = Read-Host "$(Get-Msg 'port.local_only.choice')"
        if (-not $localChoice) { $localChoice = "1" }
        $localOnly = if ($localChoice -match '^(2|n|N|no|NO)$') { "0" } else { "1" }
    }
    $config.LOCAL_ONLY = $localOnly

    if ($localOnly -eq "1") {
        Write-Log (Get-Msg "port.local_only.selected_local")
    } else {
        Write-Log (Get-Msg "port.local_only.selected_external")
        Write-Host ""
        Write-Host (Get-Msg "port.local_only.https_hint") -ForegroundColor Yellow
    }
    Write-Log ""

    # Port Configuration
    Write-Log (Get-Msg "port.title")
    $config.PORT_GATEWAY = Read-Prompt -VarName "HICLAW_PORT_GATEWAY" -PromptText (Get-Msg "port.gateway_prompt") -Default "18080"
    $config.PORT_CONSOLE = Read-Prompt -VarName "HICLAW_PORT_CONSOLE" -PromptText (Get-Msg "port.console_prompt") -Default "18001"
    $config.PORT_ELEMENT_WEB = Read-Prompt -VarName "HICLAW_PORT_ELEMENT_WEB" -PromptText (Get-Msg "port.element_prompt") -Default "18088"

    Write-Log ""

    # Domain Configuration
    Write-Log (Get-Msg "domain.title")
    $config.MATRIX_DOMAIN = Read-Prompt -VarName "HICLAW_MATRIX_DOMAIN" -PromptText (Get-Msg "domain.matrix_prompt") -Default "matrix-local.hiclaw.io:$($config.PORT_GATEWAY)"
    $config.MATRIX_CLIENT_DOMAIN = Read-Prompt -VarName "HICLAW_MATRIX_CLIENT_DOMAIN" -PromptText (Get-Msg "domain.element_prompt") -Default "matrix-client-local.hiclaw.io"
    $config.AI_GATEWAY_DOMAIN = Read-Prompt -VarName "HICLAW_AI_GATEWAY_DOMAIN" -PromptText (Get-Msg "domain.gateway_prompt") -Default "aigw-local.hiclaw.io"
    $config.FS_DOMAIN = Read-Prompt -VarName "HICLAW_FS_DOMAIN" -PromptText (Get-Msg "domain.fs_prompt") -Default "fs-local.hiclaw.io"

    Write-Log ""

    # GitHub Integration
    Write-Log (Get-Msg "github.title")
    $config.GITHUB_TOKEN = Read-Prompt -VarName "HICLAW_GITHUB_TOKEN" -PromptText (Get-Msg "github.token_prompt") -Secret -Optional

    # Skills Registry
    Write-Log ""
    Write-Log (Get-Msg "skills.title")
    $config.SKILLS_API_URL = Read-Prompt -VarName "HICLAW_SKILLS_API_URL" -PromptText (Get-Msg "skills.url_prompt") -Optional

    Write-Log ""

    # Data Persistence
    Write-Log (Get-Msg "data.title")
    if (-not $script:HICLAW_NON_INTERACTIVE -and -not $script:HICLAW_QUICKSTART -and -not $env:HICLAW_DATA_DIR) {
        $dataDirInput = Read-Host (Get-Msg "data.volume_prompt")
        $config.DATA_DIR = if ($dataDirInput) { $dataDirInput } else { "hiclaw-data" }
    }
    elseif ($env:HICLAW_DATA_DIR) {
        $config.DATA_DIR = $env:HICLAW_DATA_DIR
    }
    else {
        $config.DATA_DIR = "hiclaw-data"
    }
    Write-Log (Get-Msg "data.volume_using" -f $config.DATA_DIR)

    # Manager Workspace
    Write-Log (Get-Msg "workspace.title")
    $defaultWorkspace = "$env:USERPROFILE\hiclaw-manager"

    if (-not $script:HICLAW_NON_INTERACTIVE -and -not $script:HICLAW_QUICKSTART -and -not $env:HICLAW_WORKSPACE_DIR) {
        $wsInput = Read-Host (Get-Msg "workspace.dir_prompt" -f $defaultWorkspace)
        $config.WORKSPACE_DIR = if ($wsInput) { $wsInput } else { $defaultWorkspace }
    }
    elseif ($env:HICLAW_WORKSPACE_DIR) {
        $config.WORKSPACE_DIR = $env:HICLAW_WORKSPACE_DIR
    }
    else {
        $config.WORKSPACE_DIR = $defaultWorkspace
    }

    if (-not (Test-Path $config.WORKSPACE_DIR)) {
        New-Item -ItemType Directory -Path $config.WORKSPACE_DIR -Force | Out-Null
    }
    Write-Log (Get-Msg "workspace.dir_label" -f $config.WORKSPACE_DIR)

    Write-Log ""

    # Generate secrets
    Write-Log (Get-Msg "install.generating_secrets")
    $config.MANAGER_PASSWORD = if ($env:HICLAW_MANAGER_PASSWORD) { $env:HICLAW_MANAGER_PASSWORD } else { New-RandomKey }
    $config.REGISTRATION_TOKEN = if ($env:HICLAW_REGISTRATION_TOKEN) { $env:HICLAW_REGISTRATION_TOKEN } else { New-RandomKey }
    $config.MINIO_USER = if ($env:HICLAW_MINIO_USER) { $env:HICLAW_MINIO_USER } else { $config.ADMIN_USER }
    $config.MINIO_PASSWORD = if ($env:HICLAW_MINIO_PASSWORD) { $env:HICLAW_MINIO_PASSWORD } else { $config.ADMIN_PASSWORD }
    $config.MANAGER_GATEWAY_KEY = if ($env:HICLAW_MANAGER_GATEWAY_KEY) { $env:HICLAW_MANAGER_GATEWAY_KEY } else { New-RandomKey }

    # Store additional config
    $config.LANGUAGE = $script:HICLAW_LANGUAGE
    $config.REGISTRY = $script:HICLAW_REGISTRY
    $config.WORKER_IMAGE = $script:WORKER_IMAGE

    # Host share directory
    if (-not $script:HICLAW_NON_INTERACTIVE -and -not $script:HICLAW_QUICKSTART -and -not $env:HICLAW_HOST_SHARE_DIR) {
        $shareInput = Read-Host (Get-Msg "host_share.prompt" -f $env:USERPROFILE)
        $config.HOST_SHARE_DIR = if ($shareInput) { $shareInput } else { $env:USERPROFILE }
    }
    elseif ($env:HICLAW_HOST_SHARE_DIR) {
        $config.HOST_SHARE_DIR = $env:HICLAW_HOST_SHARE_DIR
    }
    else {
        $config.HOST_SHARE_DIR = $env:USERPROFILE
    }

    # Write env file
    New-EnvFile -Config $config -Path $script:HICLAW_ENV_FILE

    # Build Docker arguments
    $dockerArgs = @(
        "run", "-d",
        "--name", "hiclaw-manager",
        "--env-file", $script:HICLAW_ENV_FILE,
        "-e", "HOME=/root/manager-workspace",
        "-w", "/root/manager-workspace",
        "-e", "HOST_ORIGINAL_HOME=$($config.HOST_SHARE_DIR)"
    )

    # Timezone
    $dockerArgs += @("-e", "TZ=$($script:HICLAW_TIMEZONE)")

    # Docker socket mount (Windows uses named pipe)
    if ($script:HICLAW_MOUNT_SOCKET) {
        $dockerArgs += @("-v", "//var/run/docker.sock:/var/run/docker.sock")
        Write-Log (Get-Msg "install.socket_detected" -f "//var/run/docker.sock")
    }

    # Port mappings
    $portPrefix = if ($config.LOCAL_ONLY -eq "1") { "127.0.0.1:" } else { "" }
    $dockerArgs += @("-p", "${portPrefix}$($config.PORT_GATEWAY):8080")
    $dockerArgs += @("-p", "${portPrefix}$($config.PORT_CONSOLE):8001")
    $dockerArgs += @("-p", "${portPrefix}$($config.PORT_ELEMENT_WEB):8088")

    # Data mount: Docker volume
    $dockerArgs += @("-v", "$($config.DATA_DIR):/data")

    # Workspace mount
    $wsDockerPath = ConvertTo-DockerPath -Path $config.WORKSPACE_DIR
    $dockerArgs += @("-v", "${wsDockerPath}:/root/manager-workspace")

    # Host share mount
    $shareDockerPath = ConvertTo-DockerPath -Path $config.HOST_SHARE_DIR
    $dockerArgs += @("-v", "${shareDockerPath}:/host-share")
    Write-Log (Get-Msg "host_share.sharing" -f $config.HOST_SHARE_DIR)

    # YOLO mode
    if ($env:HICLAW_YOLO -eq "1") {
        $dockerArgs += @("-e", "HICLAW_YOLO=1")
        Write-Log (Get-Msg "install.yolo")
    }

    # Restart policy
    $dockerArgs += @("--restart", "unless-stopped")

    # Image
    $dockerArgs += $script:MANAGER_IMAGE

    # Remove existing container
    $existingContainer = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$"
    if ($existingContainer) {
        Write-Log (Get-Msg "install.removing_existing")
        docker stop hiclaw-manager *>$null
        docker rm hiclaw-manager *>$null
    }

    # Check if the Docker volume is already owned by a different installation directory
    $volumeExists = docker volume ls -q 2>$null | Select-String "^$($config.DATA_DIR)$"
    if ($volumeExists) {
        $existingInstallPath = (docker volume inspect $config.DATA_DIR --format '{{index .Labels "hiclaw.install-path"}}' 2>$null) -join ""
        $currentInstallPath = (Get-Location).Path
        if ($existingInstallPath -and $existingInstallPath.Trim() -ne $currentInstallPath) {
            Write-Host ""
            Write-Host "`e[31m[HiClaw ERROR] $(Get-Msg 'install.volume_conflict.title')`e[0m"
            Write-Host "`e[31m$(Get-Msg 'install.volume_conflict.detail' -f $config.DATA_DIR)`e[0m"
            Write-Host "`e[31m$(Get-Msg 'install.volume_conflict.original' -f $existingInstallPath.Trim())`e[0m"
            Write-Host "`e[31m$(Get-Msg 'install.volume_conflict.current' -f $currentInstallPath)`e[0m"
            Write-Host ""
            Write-Host "`e[33m$(Get-Msg 'install.volume_conflict.hint')`e[0m"
            Write-Host "`e[33m$(Get-Msg 'install.volume_conflict.options')`e[0m"
            Write-Host "`e[33m$(Get-Msg 'install.volume_conflict.opt1' -f $existingInstallPath.Trim())`e[0m"
            Write-Host "`e[33m$(Get-Msg 'install.volume_conflict.opt2' -f '.\hiclaw-install.ps1')`e[0m"
            Write-Host "`e[33m$(Get-Msg 'install.volume_conflict.opt3')`e[0m"
            Write-Host ""
            throw "Volume conflict: cannot reuse volume '$($config.DATA_DIR)' from a different installation directory."
        }
    } else {
        # Create the volume with an install-path label so future installs can detect conflicts
        docker volume create --label "hiclaw.install-path=$($(Get-Location).Path)" $config.DATA_DIR | Out-Null
    }

    # Pull images (skip if already exists locally)
    # For local images (prefix "hiclaw/"), skip pull if exists
    # For remote images, always pull to get updates
    $LocalImagePrefix = "hiclaw/"
    if ($script:MANAGER_IMAGE.StartsWith($LocalImagePrefix)) {
        $managerImageExists = docker image inspect $script:MANAGER_IMAGE 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Log (Get-Msg "install.image.exists" -f $script:MANAGER_IMAGE)
        } else {
            Write-Log (Get-Msg "install.image.pulling_manager" -f $script:MANAGER_IMAGE)
            & docker pull $script:MANAGER_IMAGE
        }
    } else {
        Write-Log (Get-Msg "install.image.pulling_manager" -f $script:MANAGER_IMAGE)
        & docker pull $script:MANAGER_IMAGE
    }

    if ($script:WORKER_IMAGE.StartsWith($LocalImagePrefix)) {
        $workerImageExists = docker image inspect $script:WORKER_IMAGE 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Log (Get-Msg "install.image.worker_exists" -f $script:WORKER_IMAGE)
        } else {
            Write-Log (Get-Msg "install.image.pulling_worker" -f $script:WORKER_IMAGE)
            & docker pull $script:WORKER_IMAGE
        }
    } else {
        Write-Log (Get-Msg "install.image.pulling_worker" -f $script:WORKER_IMAGE)
        & docker pull $script:WORKER_IMAGE
    }

    # Run container
    Write-Log (Get-Msg "install.starting_manager")
    & docker $dockerArgs

    # Wait for ready
    Wait-ManagerReady -Container "hiclaw-manager"

    # Create OpenAI-compatible provider if needed
    if ($config.LLM_PROVIDER -eq "openai-compat") {
        New-OpenAICompatProvider -BaseUrl $config.OPENAI_BASE_URL -ApiKey $config.LLM_API_KEY -ConsolePort ([int]$config.PORT_CONSOLE)
    }

    # Send welcome message
    Send-WelcomeMessage -Container "hiclaw-manager" -AdminUser $config.ADMIN_USER -AdminPassword $config.ADMIN_PASSWORD -MatrixDomain $config.MATRIX_DOMAIN -Timezone $script:HICLAW_TIMEZONE -Language $script:HICLAW_LANGUAGE

    # Print success message
    Write-Log ""
    Write-Log (Get-Msg "success.title")
    Write-Log ""
    Write-Log (Get-Msg "success.domains_configured")
    Write-Log "  $($config.MATRIX_DOMAIN.Split(':')[0]) $($config.MATRIX_CLIENT_DOMAIN) $($config.AI_GATEWAY_DOMAIN) $($config.FS_DOMAIN)"
    Write-Log ""

    $lanIP = Get-LanIP

    Write-Host "`e[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.open_url')`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[1;36m    http://127.0.0.1:$($config.PORT_ELEMENT_WEB)/#/login`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.login_with')`e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.username' -f $config.ADMIN_USER)`e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.password' -f $config.ADMIN_PASSWORD)`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.after_login')`e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.tell_it')`e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.manager_auto')`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m  ─────────────────────────────────────────────────────────────────────────────  `e[0m"
    Write-Host "`e[33m  $(Get-Msg 'success.mobile_title')`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    if ($lanIP) {
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_step1')`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_step2' -f "http://${lanIP}:$($config.PORT_GATEWAY)")`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_step3')`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_username' -f $config.ADMIN_USER)`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_password' -f $config.ADMIN_PASSWORD)`e[0m"
    } else {
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_step1')`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_step2_noip' -f $config.PORT_GATEWAY)`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_noip_hint')`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_step3')`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_username' -f $config.ADMIN_USER)`e[0m"
        Write-Host "`e[33m  $(Get-Msg 'success.mobile_password' -f $config.ADMIN_PASSWORD)`e[0m"
    }
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"

    Write-Log ""
    Write-Log (Get-Msg "success.other_consoles")
    Write-Log (Get-Msg "success.higress_console" -f $config.PORT_CONSOLE, $config.ADMIN_USER, $config.ADMIN_PASSWORD)
    Write-Log ""
    Write-Log (Get-Msg "success.switch_llm.title")
    Write-Log (Get-Msg "success.switch_llm.hint")
    Write-Log (Get-Msg "success.switch_llm.docs")
    Write-Log (Get-Msg "success.switch_llm.url")
    Write-Log ""
    Write-Log (Get-Msg "success.tip")
    Write-Log ""
    if ($config.LOCAL_ONLY -ne "1") {
        Write-Host (Get-Msg "port.local_only.https_hint") -ForegroundColor Yellow
        Write-Log ""
    }
    Write-Log (Get-Msg "success.config_file" -f $script:HICLAW_ENV_FILE)

    Write-Log (Get-Msg "success.data_volume" -f $config.DATA_DIR)

    Write-Log (Get-Msg "success.workspace" -f $config.WORKSPACE_DIR)
}

# ============================================================
# Worker Installation
# ============================================================

function Install-Worker {
    param(
        [string]$Name,
        [string]$Fs,
        [string]$FsKey,
        [string]$FsSecret,
        [switch]$Reset,
        [switch]$FindSkills,
        [string]$SkillsApiUrl
    )

    # Validate required parameters
    if (-not $Name) {
        Write-Error (Get-Msg "error.name_required")
    }
    if (-not $Fs) {
        Write-Error (Get-Msg "error.fs_required")
    }
    if (-not $FsKey) {
        Write-Error (Get-Msg "error.fs_key_required")
    }
    if (-not $FsSecret) {
        Write-Error (Get-Msg "error.fs_secret_required")
    }

    $containerName = "hiclaw-worker-$Name"

    # Handle reset
    if ($Reset) {
        Write-Log (Get-Msg "worker.resetting" -f $Name)
        docker stop $containerName *>$null
        docker rm $containerName *>$null
    }

    # Check for existing container
    $existing = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^$containerName$"
    if ($existing) {
        Write-Error (Get-Msg "worker.exists" -f $containerName)
    }

    # Detect timezone and registry
    $timezone =  { $(Get-HiClawTimeZone) } 
    $registry = Get-Registry -Timezone $timezone
    $workerImage = if ($env:HICLAW_INSTALL_WORKER_IMAGE) {
        $env:HICLAW_INSTALL_WORKER_IMAGE
    } else {
        "$registry/higress/hiclaw-worker:$($script:HICLAW_VERSION)"
    }

    Write-Log (Get-Msg "worker.starting" -f $Name)

    $dockerArgs = @(
        "run", "-d",
        "--name", $containerName,
        "-e", "HOME=/root/hiclaw-fs/agents/$Name",
        "-w", "/root/hiclaw-fs/agents/$Name",
        "-e", "HICLAW_WORKER_NAME=$Name",
        "-e", "HICLAW_FS_ENDPOINT=$Fs",
        "-e", "HICLAW_FS_ACCESS_KEY=$FsKey",
        "-e", "HICLAW_FS_SECRET_KEY=$FsSecret"
    )

    # Add SKILLS_API_URL if find-skills is enabled and URL is specified
    if ($FindSkills -and $SkillsApiUrl) {
        $dockerArgs += @("-e", "SKILLS_API_URL=$SkillsApiUrl")
        Write-Log (Get-Msg "worker.skills_url" -f $SkillsApiUrl)
    }

    $dockerArgs += @("--restart", "unless-stopped", $workerImage)

    & docker $dockerArgs

    Write-Log ""
    Write-Log (Get-Msg "worker.started" -f $Name)
    Write-Log (Get-Msg "worker.container" -f $containerName)
    Write-Log (Get-Msg "worker.view_logs" -f $containerName)
}

# ============================================================
# Uninstall
# ============================================================

function Uninstall-HiClaw {
    Write-Log (Get-Msg "uninstall.title")

    # Stop and remove manager
    $manager = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$"
    if ($manager) {
        Write-Log (Get-Msg "uninstall.stopping_manager")
        docker stop hiclaw-manager *>$null
        docker rm hiclaw-manager *>$null
    }

    # Stop and remove workers
    $workers = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-"
    if ($workers) {
        Write-Log (Get-Msg "uninstall.stopping_workers")
        $workers | ForEach-Object {
            docker stop $_ *>$null
            docker rm $_ *>$null
            Write-Log (Get-Msg "uninstall.removed" -f $_)
        }
    }

    # Remove Docker volume
    $volume = docker volume ls -q 2>$null | Select-String "^hiclaw-data$"
    if ($volume) {
        Write-Log (Get-Msg "uninstall.removing_volume")
        docker volume rm hiclaw-data *>$null
    }

    # Remove env file
    if (Test-Path $script:HICLAW_ENV_FILE) {
        Write-Log (Get-Msg "uninstall.removing_env" -f $script:HICLAW_ENV_FILE)
        Remove-Item -Force $script:HICLAW_ENV_FILE
    }

    Write-Log ""
    Write-Log (Get-Msg "uninstall.done")
    Write-Log (Get-Msg "uninstall.workspace_note")
}

# ============================================================
# Main Entry Point
# ============================================================

switch ($Command) {
    "manager" {
        Install-Manager
    }
    "worker" {
        Install-Worker -Name $Name -Fs $Fs -FsKey $FsKey -FsSecret $FsSecret -Reset:$Reset -FindSkills:$FindSkills -SkillsApiUrl $SkillsApiUrl
    }
    "uninstall" {
        Uninstall-HiClaw
    }
}
