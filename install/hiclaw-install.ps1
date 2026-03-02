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
$script:HICLAW_ENV_FILE = if ($EnvFile) { $EnvFile } elseif ($env:HICLAW_ENV_FILE) { $env:HICLAW_ENV_FILE } else { ".\hiclaw-manager.env" }

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

function Get-Timezone {
    try {
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
    Write-Log "Waiting for Manager agent to be ready (timeout: ${timeout}s)..."

    while ($elapsed -lt $Timeout) {
        try {
            $result = docker exec $Container openclaw gateway health --json 2>$null
            if ($result -match '"ok"') {
                Write-Log "Manager agent is ready!"
                return $true
            }
        }
        catch {
            # Ignore errors during polling
        }

        Start-Sleep -Seconds 5
        $elapsed += 5
        Write-Host "`r`e[36m[HiClaw]`e[0m Waiting... (${elapsed}s/${Timeout}s)" -NoNewline
    }

    Write-Host ""
    Write-Error "Manager agent did not become ready within ${timeout}s. Check: docker logs $Container"
}

function New-EnvFile {
    param([hashtable]$Config, [string]$Path)

    $content = @"
# HiClaw Manager Configuration
# Generated by hiclaw-install.ps1 on $(Get-Date -Format "yyyy-MM-dd HH:mm:ss")

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
    Write-Log "Configuration saved to $Path"
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
        Write-Log "  $VarName = (pre-set via env)"
        return $envValue
    }

    # Non-interactive mode
    if ($script:HICLAW_NON_INTERACTIVE) {
        if ($Default) {
            Write-Log "  $VarName = $Default (default)"
            return $Default
        }
        elseif ($Optional) {
            return ""
        }
        else {
            Write-Error "$VarName is required (set via environment variable in non-interactive mode)"
        }
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
        Write-Error "$VarName is required"
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
        Write-Log "WARNING: OpenAI Base URL or API Key not set, skipping provider creation"
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

    Write-Log "Creating OpenAI-compatible provider..."
    Write-Log "  Domain: $domain"
    Write-Log "  Port: $port"
    Write-Log "  Protocol: $protocol"

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
        Write-Log "WARNING: Failed to create DNS service source (may already exist)"
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
        Write-Log "OpenAI-compatible provider created successfully"
        return $true
    }
    catch {
        Write-Log "WARNING: Failed to create AI provider (may already exist)"
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
        [string]$Timezone
    )

    # Skip if soul already configured
    $soulConfigured = docker exec $Container test -f /root/manager-workspace/soul-configured 2>$null
    if ($LASTEXITCODE -eq 0) {
        Write-Log "Soul already configured (soul-configured marker found), skipping onboarding message"
        return $true
    }

    $matrixUrl = "http://127.0.0.1:6167"
    $managerUser = "manager"
    $managerFullId = "@${managerUser}:${MatrixDomain}"

    # Login to get admin access token
    Write-Log "Logging in as $AdminUser to send welcome message..."

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
            Write-Log "WARNING: Failed to login as $AdminUser, skipping welcome message"
            return $false
        }
    }
    catch {
        Write-Log "WARNING: Failed to login as $AdminUser, skipping welcome message"
        return $false
    }

    # Find or create DM room
    Write-Log "Finding DM room with Manager..."

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
        }
        catch {
            continue
        }
    }

    if (-not $roomId) {
        Write-Log "Creating DM room with Manager..."
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
        }
        catch {
            Write-Log "WARNING: Could not find or create DM room with Manager"
            return $false
        }
    }

    if (-not $roomId) {
        Write-Log "WARNING: Could not find or create DM room with Manager"
        return $false
    }

    # Wait for Manager to join
    Write-Log "Waiting for Manager to join the room..."
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
        }
        catch {
            # Continue waiting
        }

        Start-Sleep -Seconds 2
        $waitElapsed += 2
    }

    # Send welcome message
    Write-Log "Sending welcome message to Manager..."

    $welcomeMsg = @"
This is an automated message from the HiClaw installation script. This is a fresh installation.

You are an AI agent that manages a team of worker agents. Your identity and personality have not been configured yet - the human admin is about to meet you for the first time.

Please begin the onboarding conversation:

1. Greet the admin warmly and briefly describe what you can do (coordinate workers, manage tasks, run multi-agent projects) - without referring to yourself by any specific title yet
2. Detect their likely region from the current timezone: $Timezone
3. Respond in their most likely local language (or English if uncertain)
4. Ask them the following questions (one message is fine):
   a. What would they like to call you? (name or title)
   b. What communication style do they prefer? (e.g. formal, casual, concise, detailed)
   c. Any specific behavior guidelines or constraints they want you to follow?
   d. Confirm the default language they want you to use
5. After they reply, write their preferences to the "Identity & Personality" section of ~/SOUL.md - replace the "(not yet configured)" placeholder with the configured identity
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

        Write-Log "Welcome message sent to Manager"
        return $true
    }
    catch {
        Write-Log "WARNING: Failed to send welcome message"
        return $false
    }
}

# ============================================================
# Manager Installation
# ============================================================

function Install-Manager {
    Write-Log "=== HiClaw Manager Installation ==="

    # Detect timezone
    $script:HICLAW_TIMEZONE = if ($env:HICLAW_TIMEZONE) { $env:HICLAW_TIMEZONE } else { Get-Timezone }

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

    Write-Log "Registry: $($script:HICLAW_REGISTRY)"
    Write-Log ""

    # Check Docker
    if (-not (Test-DockerRunning)) {
        Write-Error "Docker Desktop is not running. Please start Docker Desktop first."
    }

    # Initialize config hashtable
    $config = @{}

    # Onboarding mode selection
    if (-not $script:HICLAW_NON_INTERACTIVE) {
        Write-Log "--- Onboarding Mode ---"
        Write-Host ""
        Write-Host "Choose your installation mode:"
        Write-Host "  1) Quick Start  - Fast installation with Alibaba Cloud (recommended)"
        Write-Host "  2) Manual       - Choose LLM provider and customize options"
        Write-Host ""

        $choice = Read-Host "Enter choice [1/2]"
        $choice = if ($choice) { $choice } else { "1" }

        switch -Regex ($choice) {
            "^(1|quick|quickstart)$" {
                Write-Log "Quick Start mode selected - using Alibaba Cloud Bailian"
                $config.LLM_PROVIDER = "qwen"
                $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }
                $script:HICLAW_QUICKSTART = $true
            }
            "^(2|manual)$" {
                Write-Log "Manual mode selected - you will choose LLM provider and customize options"
                $script:HICLAW_QUICKSTART = $false
            }
            default {
                Write-Log "Invalid choice, defaulting to Quick Start mode"
                $config.LLM_PROVIDER = "qwen"
                $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }
                $script:HICLAW_QUICKSTART = $true
            }
        }
        Write-Log ""
    }

    # Check for existing installation
    if (Test-Path $script:HICLAW_ENV_FILE) {
        Write-Log "Existing Manager installation detected (env file: $($script:HICLAW_ENV_FILE))"

        # Check for running containers
        $runningManager = docker ps --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$"
        $runningWorkers = docker ps --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-"
        $existingWorkers = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-"

        if ($script:HICLAW_NON_INTERACTIVE) {
            Write-Log "Non-interactive mode: performing in-place upgrade..."
            $upgradeChoice = "1"
        }
        else {
            Write-Host ""
            Write-Host "Choose an action:"
            Write-Host "  1) In-place upgrade (keep data, workspace, env file)"
            Write-Host "  2) Clean reinstall (remove all data, start fresh)"
            Write-Host "  3) Cancel"
            Write-Host ""

            $upgradeChoice = Read-Host "Enter choice [1/2/3]"
            $upgradeChoice = if ($upgradeChoice) { $upgradeChoice } else { "1" }
        }

        switch -Regex ($upgradeChoice) {
            "^(1|upgrade)$" {
                Write-Log "Performing in-place upgrade..."

                if ($runningManager -or $runningWorkers) {
                    Write-Host ""
                    Write-Host "`e[33mWarning: Manager container will be stopped and recreated.`e[0m"
                    if ($existingWorkers) {
                        Write-Host "`e[33mWarning: Worker containers will also be recreated.`e[0m"
                    }

                    if (-not $script:HICLAW_NON_INTERACTIVE) {
                        $confirm = Read-Host "Continue? [y/N]"
                        if ($confirm -ne "y" -and $confirm -ne "Y") {
                            Write-Log "Installation cancelled."
                            exit 0
                        }
                    }
                }

                # Stop and remove containers
                if ($runningManager -or (docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$")) {
                    Write-Log "Stopping and removing existing manager container..."
                    docker stop hiclaw-manager 2>$null
                    docker rm hiclaw-manager 2>$null
                }

                if ($existingWorkers) {
                    Write-Log "Stopping and removing existing worker containers..."
                    $existingWorkers | ForEach-Object {
                        docker stop $_ 2>$null
                        docker rm $_ 2>$null
                        Write-Log "  Removed: $_"
                    }
                }
            }
            "^(2|reinstall)$" {
                Write-Log "Performing clean reinstall..."

                # Get existing workspace
                $existingWorkspace = "$env:USERPROFILE\hiclaw-manager"
                if (Test-Path $script:HICLAW_ENV_FILE) {
                    $envContent = Get-Content $script:HICLAW_ENV_FILE
                    $wsLine = $envContent | Select-String "^HICLAW_WORKSPACE_DIR="
                    if ($wsLine) {
                        $existingWorkspace = $wsLine.Line.Substring(20)
                    }
                }

                Write-Host ""
                Write-Host "`e[33mWarning: The following running containers will be stopped:`e[0m"
                if ($runningManager) { Write-Host "`e[33m   - hiclaw-manager (manager)`e[0m" }
                $runningWorkers | ForEach-Object { Write-Host "`e[33m   - $_ (worker)`e[0m" }

                Write-Host ""
                Write-Host "`e[31mWarning: This will DELETE the following:`e[0m"
                Write-Host "`e[31m   - Docker volume: hiclaw-data`e[0m"
                Write-Host "`e[31m   - Env file: $($script:HICLAW_ENV_FILE)`e[0m"
                Write-Host "`e[31m   - Manager workspace: $existingWorkspace`e[0m"
                Write-Host "`e[31m   - All worker containers`e[0m"
                Write-Host ""
                Write-Host "`e[31mTo confirm deletion, please type the workspace path:`e[0m"
                Write-Host "`e[31m  $existingWorkspace`e[0m"
                Write-Host ""

                $confirmPath = Read-Host "Type the path to confirm (or press Ctrl+C to cancel)"

                if ($confirmPath -ne $existingWorkspace) {
                    Write-Error "Path mismatch. Aborting reinstall. Input: '$confirmPath', Expected: '$existingWorkspace'"
                }

                Write-Log "Confirmed. Cleaning up..."

                # Stop and remove all containers
                docker stop hiclaw-manager 2>$null
                docker rm hiclaw-manager 2>$null

                docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-" | ForEach-Object {
                    docker stop $_ 2>$null
                    docker rm $_ 2>$null
                    Write-Log "  Removed worker: $_"
                }

                # Remove Docker volume
                if (docker volume ls -q 2>$null | Select-String "^hiclaw-data$") {
                    Write-Log "Removing Docker volume: hiclaw-data"
                    docker volume rm hiclaw-data 2>$null
                }

                # Remove workspace
                if (Test-Path $existingWorkspace) {
                    Write-Log "Removing workspace directory: $existingWorkspace"
                    Remove-Item -Recurse -Force $existingWorkspace
                }

                # Remove env file
                if (Test-Path $script:HICLAW_ENV_FILE) {
                    Write-Log "Removing env file: $($script:HICLAW_ENV_FILE)"
                    Remove-Item -Force $script:HICLAW_ENV_FILE
                }

                Write-Log "Cleanup complete. Starting fresh installation..."
            }
            "^(3|cancel|.*)$" {
                Write-Log "Installation cancelled."
                exit 0
            }
        }

        # Load existing env file
        if (Test-Path $script:HICLAW_ENV_FILE) {
            Write-Log "Loading existing config from $($script:HICLAW_ENV_FILE) (shell env vars take priority)..."
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
    Write-Log "--- LLM Configuration ---"

    if ($script:HICLAW_QUICKSTART -or $script:HICLAW_NON_INTERACTIVE) {
        $config.LLM_PROVIDER = if ($env:HICLAW_LLM_PROVIDER) { $env:HICLAW_LLM_PROVIDER } else { "qwen" }
        $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }

        Write-Log "  Provider: $($config.LLM_PROVIDER)"
        Write-Log "  Model: $($config.DEFAULT_MODEL)"

        if ($config.LLM_PROVIDER -eq "qwen") {
            Write-Log ""
            Write-Log "  Get your Alibaba Cloud Bailian API Key from:"
            Write-Log "     https://www.aliyun.com/product/bailian"
        }

        Write-Log ""
        $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText "LLM API Key" -Secret
    }
    else {
        Write-Host ""
        Write-Host "Available LLM Providers:"
        Write-Host "  1) alibaba-cloud  - Alibaba Cloud Bailian (recommended for Chinese users)"
        Write-Host "  2) openai-compat  - OpenAI-compatible API (OpenAI, DeepSeek, etc.)"
        Write-Host ""

        $providerChoice = Read-Host "Select provider [1/2]"
        $providerChoice = if ($providerChoice) { $providerChoice } else { "1" }

        switch -Regex ($providerChoice) {
            "^(1|alibaba-cloud)$" {
                $config.LLM_PROVIDER = "qwen"
                $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }

                Write-Log "  Provider: $($config.LLM_PROVIDER) (Alibaba Cloud Bailian)"
                Write-Log "  Model: $($config.DEFAULT_MODEL)"
                Write-Log ""
                Write-Log "  Get your Alibaba Cloud Bailian API Key from:"
                Write-Log "     https://www.aliyun.com/product/bailian"
                Write-Log ""

                $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText "LLM API Key" -Secret
            }
            "^(2|openai-compat)$" {
                $config.LLM_PROVIDER = "openai-compat"

                Write-Log "  Provider: $($config.LLM_PROVIDER) (OpenAI-compatible)"
                Write-Host ""

                $config.OPENAI_BASE_URL = Read-Host "Base URL (e.g., https://api.openai.com/v1)"
                $modelInput = Read-Host "Default Model ID [gpt-4o]"
                $config.DEFAULT_MODEL = if ($modelInput) { $modelInput } else { "gpt-4o" }

                Write-Log "  Base URL: $($config.OPENAI_BASE_URL)"
                Write-Log "  Model: $($config.DEFAULT_MODEL)"
                Write-Log ""

                $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText "LLM API Key" -Secret
            }
            default {
                $config.LLM_PROVIDER = "qwen"
                $config.DEFAULT_MODEL = if ($env:HICLAW_DEFAULT_MODEL) { $env:HICLAW_DEFAULT_MODEL } else { "qwen3.5-plus" }

                Write-Log "Invalid choice, defaulting to Alibaba Cloud"
                Write-Log "  Provider: $($config.LLM_PROVIDER)"
                Write-Log "  Model: $($config.DEFAULT_MODEL)"
                Write-Log ""

                $config.LLM_API_KEY = Read-Prompt -VarName "HICLAW_LLM_API_KEY" -PromptText "LLM API Key" -Secret
            }
        }
    }

    Write-Log ""

    # Admin Credentials
    Write-Log "--- Admin Credentials ---"
    $config.ADMIN_USER = Read-Prompt -VarName "HICLAW_ADMIN_USER" -PromptText "Admin Username" -Default "admin"

    if (-not $env:HICLAW_ADMIN_PASSWORD) {
        $config.ADMIN_PASSWORD = Read-Prompt -VarName "HICLAW_ADMIN_PASSWORD" -PromptText "Admin Password (leave empty to auto-generate, min 8 chars)" -Secret -Optional

        if (-not $config.ADMIN_PASSWORD) {
            $randomSuffix = (New-RandomKey).Substring(0, 12)
            $config.ADMIN_PASSWORD = "admin$randomSuffix"
            Write-Log "  Auto-generated admin password"
        }
    }
    else {
        $config.ADMIN_PASSWORD = $env:HICLAW_ADMIN_PASSWORD
        Write-Log "  HICLAW_ADMIN_PASSWORD = (pre-set via env)"
    }

    # Validate password length
    if ($config.ADMIN_PASSWORD.Length -lt 8) {
        Write-Error "Admin password must be at least 8 characters (MinIO requirement). Current length: $($config.ADMIN_PASSWORD.Length)"
    }

    Write-Log ""

    # Port Configuration
    Write-Log "--- Port Configuration (press Enter for defaults) ---"
    $config.PORT_GATEWAY = Read-Prompt -VarName "HICLAW_PORT_GATEWAY" -PromptText "Host port for gateway (8080 inside container)" -Default "18080"
    $config.PORT_CONSOLE = Read-Prompt -VarName "HICLAW_PORT_CONSOLE" -PromptText "Host port for Higress console (8001 inside container)" -Default "18001"
    $config.PORT_ELEMENT_WEB = Read-Prompt -VarName "HICLAW_PORT_ELEMENT_WEB" -PromptText "Host port for Element Web direct access (8088 inside container)" -Default "18088"

    Write-Log ""

    # Domain Configuration
    Write-Log "--- Domain Configuration (press Enter for defaults) ---"
    $config.MATRIX_DOMAIN = Read-Prompt -VarName "HICLAW_MATRIX_DOMAIN" -PromptText "Matrix Domain" -Default "matrix-local.hiclaw.io:$($config.PORT_GATEWAY)"
    $config.MATRIX_CLIENT_DOMAIN = Read-Prompt -VarName "HICLAW_MATRIX_CLIENT_DOMAIN" -PromptText "Element Web Domain" -Default "matrix-client-local.hiclaw.io"
    $config.AI_GATEWAY_DOMAIN = Read-Prompt -VarName "HICLAW_AI_GATEWAY_DOMAIN" -PromptText "AI Gateway Domain" -Default "aigw-local.hiclaw.io"
    $config.FS_DOMAIN = Read-Prompt -VarName "HICLAW_FS_DOMAIN" -PromptText "File System Domain" -Default "fs-local.hiclaw.io"

    Write-Log ""

    # GitHub Integration
    Write-Log "--- GitHub Integration (optional, press Enter to skip) ---"
    $config.GITHUB_TOKEN = Read-Prompt -VarName "HICLAW_GITHUB_TOKEN" -PromptText "GitHub Personal Access Token (optional)" -Secret -Optional

    Write-Log ""

    # Data Persistence
    Write-Log "--- Data Persistence ---"
    if (-not $script:HICLAW_NON_INTERACTIVE -and -not $env:HICLAW_DATA_DIR) {
        $dataDirInput = Read-Host "Docker volume name for persistent data [hiclaw-data]"
        $config.DATA_DIR = if ($dataDirInput) { $dataDirInput } else { "hiclaw-data" }
    }
    elseif ($env:HICLAW_DATA_DIR) {
        $config.DATA_DIR = $env:HICLAW_DATA_DIR
    }
    else {
        $config.DATA_DIR = "hiclaw-data"
    }
    Write-Log "  Using Docker volume: $($config.DATA_DIR)"

    # Manager Workspace
    Write-Log "--- Manager Workspace ---"
    $defaultWorkspace = "$env:USERPROFILE\hiclaw-manager"

    if (-not $script:HICLAW_NON_INTERACTIVE -and -not $env:HICLAW_WORKSPACE_DIR) {
        $wsInput = Read-Host "Manager workspace directory [$defaultWorkspace]"
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
    Write-Log "  Manager workspace: $($config.WORKSPACE_DIR)"

    Write-Log ""

    # Generate secrets
    Write-Log "Generating secrets..."
    $config.MANAGER_PASSWORD = if ($env:HICLAW_MANAGER_PASSWORD) { $env:HICLAW_MANAGER_PASSWORD } else { New-RandomKey }
    $config.REGISTRATION_TOKEN = if ($env:HICLAW_REGISTRATION_TOKEN) { $env:HICLAW_REGISTRATION_TOKEN } else { New-RandomKey }
    $config.MINIO_USER = if ($env:HICLAW_MINIO_USER) { $env:HICLAW_MINIO_USER } else { $config.ADMIN_USER }
    $config.MINIO_PASSWORD = if ($env:HICLAW_MINIO_PASSWORD) { $env:HICLAW_MINIO_PASSWORD } else { $config.ADMIN_PASSWORD }
    $config.MANAGER_GATEWAY_KEY = if ($env:HICLAW_MANAGER_GATEWAY_KEY) { $env:HICLAW_MANAGER_GATEWAY_KEY } else { New-RandomKey }

    # Store additional config
    $config.REGISTRY = $script:HICLAW_REGISTRY
    $config.WORKER_IMAGE = $script:WORKER_IMAGE

    # Host share directory
    if (-not $script:HICLAW_NON_INTERACTIVE -and -not $env:HICLAW_HOST_SHARE_DIR) {
        $shareInput = Read-Host "Host directory to share with agents (default: $env:USERPROFILE)"
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
        Write-Log "Container runtime socket: //var/run/docker.sock (direct Worker creation enabled)"
    }

    # Port mappings
    $dockerArgs += @("-p", "$($config.PORT_GATEWAY):8080")
    $dockerArgs += @("-p", "$($config.PORT_CONSOLE):8001")
    $dockerArgs += @("-p", "$($config.PORT_ELEMENT_WEB):8088")

    # Data mount: Docker volume
    $dockerArgs += @("-v", "$($config.DATA_DIR):/data")

    # Workspace mount
    $wsDockerPath = ConvertTo-DockerPath -Path $config.WORKSPACE_DIR
    $dockerArgs += @("-v", "${wsDockerPath}:/root/manager-workspace")

    # Host share mount
    $shareDockerPath = ConvertTo-DockerPath -Path $config.HOST_SHARE_DIR
    $dockerArgs += @("-v", "${shareDockerPath}:/host-share")
    Write-Log "Sharing host directory: $($config.HOST_SHARE_DIR) -> /host-share in container"

    # YOLO mode
    if ($env:HICLAW_YOLO -eq "1") {
        $dockerArgs += @("-e", "HICLAW_YOLO=1")
        Write-Log "YOLO mode enabled (autonomous decisions, no interactive prompts)"
    }

    # Restart policy
    $dockerArgs += @("--restart", "unless-stopped")

    # Image
    $dockerArgs += $script:MANAGER_IMAGE

    # Remove existing container
    $existingContainer = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$"
    if ($existingContainer) {
        Write-Log "Removing existing hiclaw-manager container..."
        docker stop hiclaw-manager 2>$null
        docker rm hiclaw-manager 2>$null
    }

    # Pull images (skip if already exists locally)
    # For local images (prefix "hiclaw/"), skip pull if exists
    # For remote images, always pull to get updates
    $LocalImagePrefix = "hiclaw/"
    if ($script:MANAGER_IMAGE.StartsWith($LocalImagePrefix)) {
        $managerImageExists = docker image inspect $script:MANAGER_IMAGE 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Log "Manager image already exists locally: $($script:MANAGER_IMAGE)"
        } else {
            Write-Log "Pulling Manager image: $($script:MANAGER_IMAGE)"
            & docker pull $script:MANAGER_IMAGE
        }
    } else {
        Write-Log "Pulling Manager image: $($script:MANAGER_IMAGE)"
        & docker pull $script:MANAGER_IMAGE
    }

    if ($script:WORKER_IMAGE.StartsWith($LocalImagePrefix)) {
        $workerImageExists = docker image inspect $script:WORKER_IMAGE 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Log "Worker image already exists locally: $($script:WORKER_IMAGE)"
        } else {
            Write-Log "Pulling Worker image: $($script:WORKER_IMAGE)"
            & docker pull $script:WORKER_IMAGE
        }
    } else {
        Write-Log "Pulling Worker image: $($script:WORKER_IMAGE)"
        & docker pull $script:WORKER_IMAGE
    }

    # Run container
    Write-Log "Starting Manager container..."
    & docker $dockerArgs

    # Wait for ready
    Wait-ManagerReady -Container "hiclaw-manager"

    # Create OpenAI-compatible provider if needed
    if ($config.LLM_PROVIDER -eq "openai-compat") {
        New-OpenAICompatProvider -BaseUrl $config.OPENAI_BASE_URL -ApiKey $config.LLM_API_KEY -ConsolePort ([int]$config.PORT_CONSOLE)
    }

    # Send welcome message
    Send-WelcomeMessage -Container "hiclaw-manager" -AdminUser $config.ADMIN_USER -AdminPassword $config.ADMIN_PASSWORD -MatrixDomain $config.MATRIX_DOMAIN -Timezone $script:HICLAW_TIMEZONE

    # Print success message
    Write-Log ""
    Write-Log "=== HiClaw Manager Started! ==="
    Write-Log ""
    Write-Log "The following domains are configured to resolve to 127.0.0.1:"
    Write-Log "  $($config.MATRIX_DOMAIN.Split(':')[0]) $($config.MATRIX_CLIENT_DOMAIN) $($config.AI_GATEWAY_DOMAIN) $($config.FS_DOMAIN)"
    Write-Log ""

    $lanIP = Get-LanIP

    Write-Host "`e[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "`e[33m  ★ Open the following URL in your browser to start:                           ★`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[1;36m    http://127.0.0.1:$($config.PORT_ELEMENT_WEB)/#/login`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m  Login with:`e[0m"
    Write-Host "`e[33m    Username: `e[1;32m$($config.ADMIN_USER)`e[0m"
    Write-Host "`e[33m    Password: `e[1;32m$($config.ADMIN_PASSWORD)`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m  After login, start chatting with the Manager!`e[0m"
    Write-Host "`e[33m    Tell it: `"Create a Worker named alice for frontend dev`"`e[0m"
    Write-Host "`e[33m    The Manager will handle everything automatically.`e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m  ─────────────────────────────────────────────────────────────────────────────  `e[0m"
    Write-Host "`e[33m  📱 Mobile access (FluffyChat / Element Mobile):                               `e[0m"
    Write-Host "`e[33m                                                                                 `e[0m"
    if ($lanIP) {
        Write-Host "`e[33m    1. Download FluffyChat or Element on your phone                             `e[0m"
        Write-Host "`e[33m    2. Set homeserver to: `e[1;36mhttp://${lanIP}:$($config.PORT_GATEWAY)`e[0m"
        Write-Host "`e[33m    3. Login with:                                                               `e[0m"
        Write-Host "`e[33m         Username: `e[1;32m$($config.ADMIN_USER)`e[0m"
        Write-Host "`e[33m         Password: `e[1;32m$($config.ADMIN_PASSWORD)`e[0m"
    } else {
        Write-Host "`e[33m    1. Download FluffyChat or Element on your phone                             `e[0m"
        Write-Host "`e[33m    2. Set homeserver to: `e[1;36mhttp://<this-machine-LAN-IP>:$($config.PORT_GATEWAY)`e[0m"
        Write-Host "`e[33m       (Could not detect LAN IP automatically - check with: ipconfig)           `e[0m"
        Write-Host "`e[33m    3. Login with:                                                               `e[0m"
        Write-Host "`e[33m         Username: `e[1;32m$($config.ADMIN_USER)`e[0m"
        Write-Host "`e[33m         Password: `e[1;32m$($config.ADMIN_PASSWORD)`e[0m"
    }
    Write-Host "`e[33m                                                                                 `e[0m"
    Write-Host "`e[33m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"

    Write-Log ""
    Write-Log "--- Other Consoles ---"
    Write-Log "  Higress Console: http://localhost:$($config.PORT_CONSOLE) (Username: $($config.ADMIN_USER) / Password: $($config.ADMIN_PASSWORD))"
    Write-Log ""
    Write-Log "--- Switch LLM Providers ---"
    Write-Log "  You can switch to other LLM providers (OpenAI, Anthropic, etc.) via Higress Console."
    Write-Log "  For detailed instructions, see:"
    Write-Log "  https://higress.ai/en/docs/ai/scene-guide/multi-proxy#console-configuration"
    Write-Log ""
    Write-Log "Tip: You can also ask the Manager to configure LLM providers for you in the chat."
    Write-Log ""
    Write-Log "Configuration file: $($script:HICLAW_ENV_FILE)"

    Write-Log "Data volume:        $($config.DATA_DIR)"

    Write-Log "Manager workspace:  $($config.WORKSPACE_DIR)"
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
        [switch]$Reset
    )

    # Validate required parameters
    if (-not $Name) {
        Write-Error "--name is required"
    }
    if (-not $Fs) {
        Write-Error "--fs is required"
    }
    if (-not $FsKey) {
        Write-Error "--fs-key is required"
    }
    if (-not $FsSecret) {
        Write-Error "--fs-secret is required"
    }

    $containerName = "hiclaw-worker-$Name"

    # Handle reset
    if ($Reset) {
        Write-Log "Resetting Worker: $Name..."
        docker stop $containerName 2>$null
        docker rm $containerName 2>$null
    }

    # Check for existing container
    $existing = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^$containerName$"
    if ($existing) {
        Write-Error "Container '$containerName' already exists. Use --reset to recreate."
    }

    # Detect timezone and registry
    $timezone = if ($env:HICLAW_TIMEZONE) { $env:HICLAW_TIMEZONE } else { Get-Timezone }
    $registry = Get-Registry -Timezone $timezone
    $workerImage = if ($env:HICLAW_INSTALL_WORKER_IMAGE) {
        $env:HICLAW_INSTALL_WORKER_IMAGE
    } else {
        "$registry/higress/hiclaw-worker:$($script:HICLAW_VERSION)"
    }

    Write-Log "Starting Worker: $Name..."

    $dockerArgs = @(
        "run", "-d",
        "--name", $containerName,
        "-e", "HOME=/root/hiclaw-fs/agents/$Name",
        "-w", "/root/hiclaw-fs/agents/$Name",
        "-e", "HICLAW_WORKER_NAME=$Name",
        "-e", "HICLAW_FS_ENDPOINT=$Fs",
        "-e", "HICLAW_FS_ACCESS_KEY=$FsKey",
        "-e", "HICLAW_FS_SECRET_KEY=$FsSecret",
        "--restart", "unless-stopped",
        $workerImage
    )

    & docker $dockerArgs

    Write-Log ""
    Write-Log "=== Worker $Name Started! ==="
    Write-Log "Container: $containerName"
    Write-Log "View logs: docker logs -f $containerName"
}

# ============================================================
# Uninstall
# ============================================================

function Uninstall-HiClaw {
    Write-Log "Uninstalling HiClaw..."

    # Stop and remove manager
    $manager = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-manager$"
    if ($manager) {
        Write-Log "Stopping and removing hiclaw-manager..."
        docker stop hiclaw-manager 2>$null
        docker rm hiclaw-manager 2>$null
    }

    # Stop and remove workers
    $workers = docker ps -a --format "{{.Names}}" 2>$null | Select-String "^hiclaw-worker-"
    if ($workers) {
        Write-Log "Stopping and removing worker containers..."
        $workers | ForEach-Object {
            docker stop $_ 2>$null
            docker rm $_ 2>$null
            Write-Log "  Removed: $_"
        }
    }

    # Remove Docker volume
    $volume = docker volume ls -q 2>$null | Select-String "^hiclaw-data$"
    if ($volume) {
        Write-Log "Removing Docker volume: hiclaw-data"
        docker volume rm hiclaw-data 2>$null
    }

    # Remove env file
    if (Test-Path $script:HICLAW_ENV_FILE) {
        Write-Log "Removing env file: $($script:HICLAW_ENV_FILE)"
        Remove-Item -Force $script:HICLAW_ENV_FILE
    }

    Write-Log ""
    Write-Log "HiClaw has been uninstalled."
    Write-Log "Note: Manager workspace directory was preserved. Remove manually if desired."
}

# ============================================================
# Main Entry Point
# ============================================================

switch ($Command) {
    "manager" {
        Install-Manager
    }
    "worker" {
        Install-Worker -Name $Name -Fs $Fs -FsKey $FsKey -FsSecret $FsSecret -Reset:$Reset
    }
    "uninstall" {
        Uninstall-HiClaw
    }
}
