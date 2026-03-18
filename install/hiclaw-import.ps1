# hiclaw-import.ps1 - Import a Worker package into HiClaw
#
# Usage:
#   .\hiclaw-import.ps1 -Zip <path-or-url> [options]

param(
    [string]$Zip = $env:HICLAW_IMPORT_ZIP,
    [string]$Name = $env:HICLAW_IMPORT_WORKER_NAME,
    [string]$Proxy = $env:HICLAW_IMPORT_PROXY,
    [string]$NoProxy = $env:HICLAW_IMPORT_NO_PROXY,
    [string]$EnvFile = "",
    [string]$BaseImage = "",
    [switch]$SkipBuild,
    [switch]$Yes
)

$ErrorActionPreference = "Stop"

# ============================================================
# Utility functions
# ============================================================

function Write-Log { param([string]$Msg); Write-Host "[HiClaw Import] $Msg" -ForegroundColor Cyan }
function Write-Warn { param([string]$Msg); Write-Host "[HiClaw Import WARNING] $Msg" -ForegroundColor Yellow }
function Write-Err { param([string]$Msg); Write-Host "[HiClaw Import ERROR] $Msg" -ForegroundColor Red; exit 1 }

function New-RandomHex {
    param([int]$Bytes = 32)
    $buf = New-Object byte[] $Bytes
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rng.GetBytes($buf)
    return [BitConverter]::ToString($buf).Replace("-", "").ToLower()
}

function Invoke-MgrExec {
    param([string]$Cmd)
    $result = & $script:ContainerCmd exec hiclaw-manager bash -c $Cmd 2>&1
    return ($result -join "`n")
}

function Send-ToMgr {
    param([string]$Content, [string]$DestPath)
    $tmpFile = [System.IO.Path]::GetTempFileName()
    [System.IO.File]::WriteAllText($tmpFile, $Content)
    Get-Content -Raw $tmpFile | & $script:ContainerCmd exec -i hiclaw-manager sh -c "cat > $DestPath"
    Remove-Item $tmpFile -Force
}

# ============================================================
# i18n
# ============================================================

$tz = [System.TimeZoneInfo]::Local.Id
$script:Lang = "en"
if ($tz -match "China|Taipei|Hong Kong|Singapore") { $script:Lang = "zh" }
if ($env:HICLAW_LANGUAGE) { $script:Lang = $env:HICLAW_LANGUAGE }

$script:Msgs = @{
    "title"                     = @{ zh = "=== 导入 Worker 到 HiClaw ==="; en = "=== Import Worker into HiClaw ===" }
    "preflight"                 = @{ zh = "预检查..."; en = "Pre-flight checks..." }
    "preflight.runtime.found"   = @{ zh = "容器运行时: {0}"; en = "Container runtime: {0}" }
    "preflight.runtime.none"    = @{ zh = "未找到 Docker 或 Podman。"; en = "Docker or Podman not found." }
    "preflight.env"             = @{ zh = "读取 HiClaw 配置: {0}"; en = "Reading HiClaw config: {0}" }
    "preflight.env.missing"     = @{ zh = "HiClaw 环境文件未找到: {0}"; en = "HiClaw env file not found: {0}" }
    "preflight.manager.check"   = @{ zh = "检查 Manager 容器..."; en = "Checking Manager container..." }
    "preflight.manager.down"    = @{ zh = "Manager 容器未运行。"; en = "Manager container is not running." }
    "preflight.zip.check"       = @{ zh = "验证迁移包: {0}"; en = "Validating migration package: {0}" }
    "preflight.zip.missing"     = @{ zh = "ZIP 文件未找到: {0}"; en = "ZIP file not found: {0}" }
    "preflight.zip.invalid"     = @{ zh = "无效迁移包: 缺少 manifest.json"; en = "Invalid package: missing manifest.json" }
    "summary.title"             = @{ zh = "--- 导入摘要 ---"; en = "--- Import Summary ---" }
    "summary.name"              = @{ zh = "  Worker 名称: {0}"; en = "  Worker name: {0}" }
    "summary.source"            = @{ zh = "  来源主机: {0}"; en = "  Source host: {0}" }
    "summary.image"             = @{ zh = "  自定义镜像: {0}"; en = "  Custom image: {0}" }
    "summary.confirm"           = @{ zh = "是否继续? [Y/n]"; en = "Continue? [Y/n]" }
    "step.build"                = @{ zh = "步骤 1/7: 构建自定义 Worker 镜像..."; en = "Step 1/7: Building custom Worker image..." }
    "step.build.skip"           = @{ zh = "步骤 1/7: 跳过镜像构建"; en = "Step 1/7: Skipping image build" }
    "step.build.done"           = @{ zh = "镜像构建完成: {0}"; en = "Image built: {0}" }
    "step.matrix"               = @{ zh = "步骤 2/7: 注册 Matrix 账号并创建 Room..."; en = "Step 2/7: Registering Matrix account and creating Room..." }
    "step.minio"                = @{ zh = "步骤 3/7: 创建 MinIO 用户..."; en = "Step 3/7: Creating MinIO user..." }
    "step.gateway"              = @{ zh = "步骤 4/7: 配置 Higress Gateway..."; en = "Step 4/7: Configuring Higress Gateway..." }
    "step.config"               = @{ zh = "步骤 5/7: 生成配置并推送到 MinIO..."; en = "Step 5/7: Generating config and pushing to MinIO..." }
    "step.registry"             = @{ zh = "步骤 6/7: 更新 Manager 注册表..."; en = "Step 6/7: Updating Manager registry..." }
    "step.start"                = @{ zh = "步骤 7/7: 通知 Manager 启动 Worker..."; en = "Step 7/7: Notifying Manager to start Worker..." }
    "done.title"                = @{ zh = "=== 导入完成 ==="; en = "=== Import Complete ===" }
    "done.hint"                 = @{ zh = "已通知 Manager 启动 Worker。请在 Element Web 中查看状态。"; en = "Manager notified. Check Worker status in Element Web." }
    "error.abort"               = @{ zh = "导入已取消。"; en = "Import aborted." }
}

function Get-Msg {
    param([string]$Key, [object[]]$f)
    $entry = $script:Msgs[$Key]
    if (-not $entry) { return $Key }
    $text = $entry[$script:Lang]
    if (-not $text) { $text = $entry["en"] }
    if (-not $text) { return $Key }
    if ($f) { return ($text -f $f) }
    return $text
}

# ============================================================
# Validate arguments
# ============================================================

if (-not $Zip) {
    Write-Host "Usage: .\hiclaw-import.ps1 -Zip <path-or-url> [-Name <worker>] [-Proxy <url>] [-EnvFile <path>] [-SkipBuild] [-Yes]"
    exit 1
}

if (-not $EnvFile) { $EnvFile = Join-Path $HOME "hiclaw-manager.env" }
$AutoYes = $Yes -or ($env:HICLAW_NON_INTERACTIVE -eq "1")

# Download ZIP if URL provided
$DownloadedZip = ""
if ($Zip -match "^https?://") {
    Write-Log "Downloading Worker package: $Zip"
    $DownloadedZip = Join-Path ([System.IO.Path]::GetTempPath()) "hiclaw-import-$([System.Guid]::NewGuid().ToString('N').Substring(0,8)).zip"
    try {
        [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -Uri $Zip -OutFile $DownloadedZip -UseBasicParsing
        $Zip = $DownloadedZip
        Write-Log "  Downloaded to: $Zip"
    } catch {
        if ($DownloadedZip -and (Test-Path $DownloadedZip)) { Remove-Item $DownloadedZip -Force }
        Write-Err "Download failed: $_"
    }
}

Write-Host ""
Write-Host (Get-Msg "title")
Write-Host ""

# ============================================================
# Pre-flight checks
# ============================================================

Write-Log (Get-Msg "preflight")

# Detect container runtime
$script:ContainerCmd = ""
try { $null = & docker info 2>$null; $script:ContainerCmd = "docker" } catch {}
if (-not $script:ContainerCmd) {
    try { $null = & podman info 2>$null; $script:ContainerCmd = "podman" } catch {}
}
if (-not $script:ContainerCmd) { Write-Err (Get-Msg "preflight.runtime.none") }
Write-Log (Get-Msg "preflight.runtime.found" -f $script:ContainerCmd)

# Read env file
Write-Log (Get-Msg "preflight.env" -f $EnvFile)
if (-not (Test-Path $EnvFile)) { Write-Err (Get-Msg "preflight.env.missing" -f $EnvFile) }

$envVars = @{}
Get-Content $EnvFile | ForEach-Object {
    if ($_ -match "^([A-Z_]+)=(.*)$") {
        $envVars[$Matches[1]] = $Matches[2].Trim('"').Trim("'")
    }
}

# Check Manager container
Write-Log (Get-Msg "preflight.manager.check")
$mgrRunning = & $script:ContainerCmd ps --filter "name=hiclaw-manager" --format "{{.Names}}" 2>$null
if ($mgrRunning -notmatch "hiclaw-manager") { Write-Err (Get-Msg "preflight.manager.down") }
Write-Log "  OK"

# Validate ZIP
Write-Log (Get-Msg "preflight.zip.check" -f $Zip)
if (-not (Test-Path $Zip)) { Write-Err (Get-Msg "preflight.zip.missing" -f $Zip) }

$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "hiclaw-import-$([System.Guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null
try {
    Expand-Archive -Path $Zip -DestinationPath $TmpDir -Force
} catch {
    Write-Err "Failed to extract ZIP: $_"
}

$ManifestPath = Join-Path $TmpDir "manifest.json"
if (-not (Test-Path $ManifestPath)) { Write-Err (Get-Msg "preflight.zip.invalid") }

$Manifest = Get-Content $ManifestPath -Raw | ConvertFrom-Json
$SuggestedName = if ($Manifest.worker.suggested_name) { $Manifest.worker.suggested_name } else { "migrated-worker" }
$SourceHost = if ($Manifest.source.hostname) { $Manifest.source.hostname } else { "unknown" }
$ManifestBaseImage = if ($Manifest.worker.base_image) { $Manifest.worker.base_image } else { "hiclaw/worker-agent:latest" }

if (-not $Name) { $Name = $SuggestedName }
$Name = $Name.ToLower() -replace "[^a-z0-9-]", ""

$HasDockerfile = Test-Path (Join-Path $TmpDir "Dockerfile")
if ($HasDockerfile) {
    $ImageTag = "hiclaw/worker-imported-${Name}:latest"
} else {
    $ImageTag = ""
}
$EffectiveBaseImage = if ($BaseImage) { $BaseImage } else { $ManifestBaseImage }

# Read config values from env
$MatrixDomain = if ($envVars["HICLAW_MATRIX_DOMAIN"]) { $envVars["HICLAW_MATRIX_DOMAIN"] } else { "matrix-local.hiclaw.io:8080" }
$AdminUser = if ($envVars["HICLAW_ADMIN_USER"]) { $envVars["HICLAW_ADMIN_USER"] } else { "admin" }
$AdminPassword = $envVars["HICLAW_ADMIN_PASSWORD"]
$GatewayPort = if ($envVars["HICLAW_PORT_GATEWAY"]) { $envVars["HICLAW_PORT_GATEWAY"] } else { "18080" }
$RegistrationToken = $envVars["HICLAW_REGISTRATION_TOKEN"]
$ManagerPassword = $envVars["HICLAW_MANAGER_PASSWORD"]
$StoragePrefix = "hiclaw/hiclaw-storage"
$MatrixServer = "http://127.0.0.1:6167"

# Show summary
Write-Host ""
Write-Host (Get-Msg "summary.title")
Write-Host (Get-Msg "summary.name" -f $Name)
Write-Host (Get-Msg "summary.source" -f $SourceHost)
if ($HasDockerfile) {
    Write-Host "  Image: $ImageTag"
} else {
    Write-Host "  Image: Standard Worker image (no Dockerfile in package)"
}
if ($Proxy) { Write-Host "  Proxy: $Proxy" }
Write-Host ""

if (-not $AutoYes) {
    $confirm = Read-Host (Get-Msg "summary.confirm")
    if ($confirm -match "^[nN]") { Write-Host (Get-Msg "error.abort"); exit 0 }
}

# ============================================================
# Step 1: Build custom Worker image
# ============================================================

if ($SkipBuild) {
    Write-Log (Get-Msg "step.build.skip")
} elseif ($HasDockerfile) {
    Write-Log (Get-Msg "step.build")
    $dockerfilePath = Join-Path $TmpDir "Dockerfile"
    $buildArgs = @("build", "-t", $ImageTag, "--build-arg", "BASE_IMAGE=$EffectiveBaseImage")
    if ($tz -match "China|Taipei") {
        $buildArgs += @("--build-arg", "APT_MIRROR=mirrors.aliyun.com")
    }
    if ($Proxy) {
        $buildArgs += @("--build-arg", "HTTP_PROXY=$Proxy", "--build-arg", "HTTPS_PROXY=$Proxy")
        $buildArgs += @("--build-arg", "http_proxy=$Proxy", "--build-arg", "https_proxy=$Proxy")
    }
    $buildArgs += @("-f", $dockerfilePath, $TmpDir)
    & $script:ContainerCmd @buildArgs
    if ($LASTEXITCODE -ne 0) { Write-Err "Docker build failed" }
    Write-Log (Get-Msg "step.build.done" -f $ImageTag)
} else {
    Write-Log "Step 1/7: No Dockerfile in package, using standard Worker image"
}

# ============================================================
# Step 2: Matrix account registration + Room creation
# ============================================================

Write-Log (Get-Msg "step.matrix")

$WorkerPassword = New-RandomHex -Bytes 16
$WorkerMinioPassword = New-RandomHex -Bytes 24
$WorkerGatewayKey = New-RandomHex -Bytes 32

# Register Matrix account
$regResp = Invoke-MgrExec "curl -sf -X POST $MatrixServer/_matrix/client/v3/register -H 'Content-Type: application/json' -d '{""username"":""$Name"",""password"":""$WorkerPassword"",""auth"":{""type"":""m.login.registration_token"",""token"":""$RegistrationToken""}}' 2>/dev/null || true"
$WorkerMatrixToken = ""
try { $WorkerMatrixToken = ($regResp | ConvertFrom-Json).access_token } catch {}

if (-not $WorkerMatrixToken) {
    $loginResp = Invoke-MgrExec "curl -sf -X POST $MatrixServer/_matrix/client/v3/login -H 'Content-Type: application/json' -d '{""type"":""m.login.password"",""identifier"":{""type"":""m.id.user"",""user"":""$Name""},""password"":""$WorkerPassword""}' 2>/dev/null || true"
    try { $WorkerMatrixToken = ($loginResp | ConvertFrom-Json).access_token } catch {}
    if (-not $WorkerMatrixToken) { Write-Err "Failed to register or login Matrix account for $Name" }
    Write-Log "  Logged into existing account: @${Name}:${MatrixDomain}"
} else {
    Write-Log "  Registered: @${Name}:${MatrixDomain}"
}

# Get Manager token
$mgrLoginResp = Invoke-MgrExec "curl -sf -X POST $MatrixServer/_matrix/client/v3/login -H 'Content-Type: application/json' -d '{""type"":""m.login.password"",""identifier"":{""type"":""m.id.user"",""user"":""manager""},""password"":""$ManagerPassword""}' 2>/dev/null"
$ManagerMatrixToken = ""
try { $ManagerMatrixToken = ($mgrLoginResp | ConvertFrom-Json).access_token } catch {}
if (-not $ManagerMatrixToken) { Write-Err "Failed to obtain Manager Matrix token" }

# Create 3-party room
$ManagerMatrixId = "@manager:${MatrixDomain}"
$AdminMatrixId = "@${AdminUser}:${MatrixDomain}"
$WorkerMatrixId = "@${Name}:${MatrixDomain}"

$roomResp = Invoke-MgrExec "curl -sf -X POST $MatrixServer/_matrix/client/v3/createRoom -H 'Authorization: Bearer $ManagerMatrixToken' -H 'Content-Type: application/json' -d '{""name"":""Worker: $Name"",""topic"":""Communication channel for $Name"",""invite"":[""$AdminMatrixId"",""$WorkerMatrixId""],""preset"":""trusted_private_chat"",""power_level_content_override"":{""users"":{""$ManagerMatrixId"":100,""$AdminMatrixId"":100,""$WorkerMatrixId"":0}}}' 2>/dev/null"
$RoomId = ""
try { $RoomId = ($roomResp | ConvertFrom-Json).room_id } catch {}
if (-not $RoomId) { Write-Err "Failed to create Matrix room: $roomResp" }
Write-Log "  Room created: $RoomId"

# ============================================================
# Step 3: MinIO user creation
# ============================================================

Write-Log (Get-Msg "step.minio")

$policyJson = @"
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::hiclaw-storage"],"Condition":{"StringLike":{"s3:prefix":["agents/$Name","agents/$Name/*","shared","shared/*"]}}},{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject"],"Resource":["arn:aws:s3:::hiclaw-storage/agents/$Name/*","arn:aws:s3:::hiclaw-storage/shared/*"]}]}
"@

Send-ToMgr -Content $policyJson -DestPath "/tmp/migrate-policy.json"
Invoke-MgrExec "mc admin user add hiclaw '$Name' '$WorkerMinioPassword' 2>/dev/null || true" | Out-Null
Invoke-MgrExec "mc admin policy remove hiclaw 'worker-$Name' 2>/dev/null || true" | Out-Null
Invoke-MgrExec "mc admin policy create hiclaw 'worker-$Name' /tmp/migrate-policy.json" | Out-Null
Invoke-MgrExec "mc admin policy attach hiclaw 'worker-$Name' --user '$Name'" | Out-Null
Invoke-MgrExec "rm -f /tmp/migrate-policy.json" | Out-Null
Write-Log "  MinIO user $Name created"

# ============================================================
# Step 4: Higress Gateway consumer + route authorization
# ============================================================

Write-Log (Get-Msg "step.gateway")

Invoke-MgrExec @"
source /opt/hiclaw/scripts/lib/hiclaw-env.sh
source /opt/hiclaw/scripts/lib/gateway-api.sh
gateway_ensure_session
gateway_create_consumer 'worker-$Name' '$WorkerGatewayKey' >/dev/null 2>&1
gateway_authorize_routes 'worker-$Name' >/dev/null 2>&1
gateway_authorize_mcp 'worker-$Name' '' >/dev/null 2>&1
"@ | Out-Null
Write-Log "  Gateway consumer and routes configured"

# ============================================================
# Step 5: Generate config and push to MinIO
# ============================================================

Write-Log (Get-Msg "step.config")

# 5a: Generate openclaw.json
Invoke-MgrExec "bash /opt/hiclaw/agent/skills/worker-management/scripts/generate-worker-config.sh '$Name' '$WorkerMatrixToken' '$WorkerGatewayKey'" | Out-Null
Write-Log "  openclaw.json generated"

# 5b: Push SOUL.md
$soulPath = Join-Path $TmpDir "config/SOUL.md"
if (Test-Path $soulPath) {
    $soulContent = Get-Content -Raw $soulPath
    Send-ToMgr -Content $soulContent -DestPath "/root/hiclaw-fs/agents/$Name/SOUL.md"
    Write-Log "  SOUL.md copied"
}

# 5c: Sync to MinIO
Invoke-MgrExec "mc mirror '/root/hiclaw-fs/agents/$Name/' '$StoragePrefix/agents/$Name/' --overwrite 2>&1 | tail -3" | Out-Null
Write-Log "  Agent config synced to MinIO"

# 5d: Merge AGENTS.md
$agentsPath = Join-Path $TmpDir "config/AGENTS.md"
if (Test-Path $agentsPath) {
    $agentsContent = Get-Content -Raw $agentsPath
    Send-ToMgr -Content $agentsContent -DestPath "/tmp/migrate-agents-user.md"
    Invoke-MgrExec @"
source /opt/hiclaw/scripts/lib/builtin-merge.sh
update_builtin_section_minio '$StoragePrefix/agents/$Name/AGENTS.md' '/opt/hiclaw/agent/worker-agent/AGENTS.md'
mc cp '$StoragePrefix/agents/$Name/AGENTS.md' /tmp/migrate-agents-merged.md 2>/dev/null
echo '' >> /tmp/migrate-agents-merged.md
cat /tmp/migrate-agents-user.md >> /tmp/migrate-agents-merged.md
mc cp /tmp/migrate-agents-merged.md '$StoragePrefix/agents/$Name/AGENTS.md' 2>/dev/null
rm -f /tmp/migrate-agents-user.md /tmp/migrate-agents-merged.md
"@ | Out-Null
    Write-Log "  AGENTS.md merged"
}

# 5e: Push MEMORY.md
$memoryPath = Join-Path $TmpDir "config/MEMORY.md"
if (Test-Path $memoryPath) {
    $memContent = Get-Content -Raw $memoryPath
    Send-ToMgr -Content $memContent -DestPath "/tmp/migrate-memory.md"
    Invoke-MgrExec "mc cp /tmp/migrate-memory.md '$StoragePrefix/agents/$Name/MEMORY.md' 2>/dev/null; rm -f /tmp/migrate-memory.md" | Out-Null
    Write-Log "  MEMORY.md pushed"
}

# 5f: Push custom skills
$skillsDir = Join-Path $TmpDir "skills"
if (Test-Path $skillsDir) {
    Get-ChildItem -Path $skillsDir -Directory | ForEach-Object {
        $skillName = $_.Name
        # Use tar via temp file to transfer directory
        $tarTmp = [System.IO.Path]::GetTempFileName() + ".tar"
        & tar -C $_.FullName -cf $tarTmp . 2>$null
        if (Test-Path $tarTmp) {
            Get-Content -Raw -Encoding Byte $tarTmp | & $script:ContainerCmd exec -i hiclaw-manager sh -c "mkdir -p /tmp/migrate-skill && tar -C /tmp/migrate-skill -xf - && mc mirror /tmp/migrate-skill/ '$StoragePrefix/agents/$Name/custom-skills/$skillName/' --overwrite 2>/dev/null; rm -rf /tmp/migrate-skill"
            Remove-Item $tarTmp -Force
            Write-Log "  Skill pushed: $skillName"
        }
    }
}

# 5g: Push Matrix password
Send-ToMgr -Content $WorkerPassword -DestPath "/tmp/migrate-pw"
Invoke-MgrExec "mc cp /tmp/migrate-pw '$StoragePrefix/agents/$Name/credentials/matrix/password' 2>/dev/null; rm -f /tmp/migrate-pw" | Out-Null

# 5h: Push file-sync skill
Invoke-MgrExec "mc mirror '/opt/hiclaw/agent/worker-agent/skills/file-sync/' '$StoragePrefix/agents/$Name/skills/file-sync/' --overwrite 2>/dev/null || true" | Out-Null
Write-Log "  file-sync skill pushed"

# 5i: Persist credentials
Invoke-MgrExec @"
mkdir -p /data/worker-creds
cat > '/data/worker-creds/$Name.env' <<CREDS
WORKER_PASSWORD="$WorkerPassword"
WORKER_MINIO_PASSWORD="$WorkerMinioPassword"
WORKER_GATEWAY_KEY="$WorkerGatewayKey"
CREDS
chmod 600 '/data/worker-creds/$Name.env'
"@ | Out-Null
Write-Log "  Credentials persisted"

# ============================================================
# Step 6: Update Manager registry and config
# ============================================================

Write-Log (Get-Msg "step.registry")

# 6a: Update Manager groupAllowFrom
Invoke-MgrExec @"
CONFIG="\${HOME}/openclaw.json"
WORKER_ID='$WorkerMatrixId'
if [ -f "\${CONFIG}" ]; then
    ALREADY=\$(jq -r --arg w "\${WORKER_ID}" '.channels.matrix.groupAllowFrom // [] | map(select(. == \$w)) | length' "\${CONFIG}" 2>/dev/null || echo '0')
    if [ "\${ALREADY}" = '0' ]; then
        jq --arg w "\${WORKER_ID}" '.channels.matrix.groupAllowFrom += [\$w]' "\${CONFIG}" > /tmp/manager-config-updated.json
        mv /tmp/manager-config-updated.json "\${CONFIG}"
    fi
fi
"@ | Out-Null
Write-Log "  Manager groupAllowFrom updated"

# 6b: Update workers-registry.json
$NowTs = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

Invoke-MgrExec @"
REGISTRY="\${HOME}/workers-registry.json"
if [ ! -f "\${REGISTRY}" ]; then
    echo '{"version":1,"updated_at":"","workers":{}}' > "\${REGISTRY}"
fi
jq --arg w '$Name' \
   --arg uid '$WorkerMatrixId' \
   --arg rid '$RoomId' \
   --arg ts '$NowTs' \
   --arg runtime 'openclaw' \
   --arg deployment 'local' \
   --arg image '$ImageTag' \
   --argjson skills '["file-sync","mcporter"]' \
   '.workers[\$w] = {
     "matrix_user_id": \$uid,
     "room_id": \$rid,
     "runtime": \$runtime,
     "deployment": \$deployment,
     "skills": \$skills,
     "image": \$image,
     "created_at": \$ts,
     "skills_updated_at": \$ts
   } | .updated_at = \$ts' \
   "\${REGISTRY}" > /tmp/workers-registry-updated.json
mv /tmp/workers-registry-updated.json "\${REGISTRY}"
"@ | Out-Null
Write-Log "  workers-registry.json updated"

# 6c: Push worker skills
Invoke-MgrExec "bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh --worker '$Name' --no-notify 2>/dev/null || true" | Out-Null
Write-Log "  Worker skills pushed"

# ============================================================
# Step 7: DM Manager to start the Worker
# ============================================================

Write-Log (Get-Msg "step.start")

$baseUrl = "http://127.0.0.1:${GatewayPort}"

# Login as admin
try {
    $loginBody = @{ type = "m.login.password"; identifier = @{ type = "m.id.user"; user = $AdminUser }; password = $AdminPassword } | ConvertTo-Json -Compress
    $loginResp = Invoke-RestMethod -Uri "$baseUrl/_matrix/client/v3/login" -Method Post -Body $loginBody -ContentType "application/json"
    $AdminToken = $loginResp.access_token
} catch {
    Write-Err "Failed to login as admin to Matrix: $_"
}

# Find DM room with Manager
$DmRoomId = ""
try {
    $joinedRooms = Invoke-RestMethod -Uri "$baseUrl/_matrix/client/v3/joined_rooms" -Headers @{ Authorization = "Bearer $AdminToken" }
    foreach ($rid in $joinedRooms.joined_rooms) {
        $encodedRid = [System.Uri]::EscapeDataString($rid)
        try {
            $members = Invoke-RestMethod -Uri "$baseUrl/_matrix/client/v3/rooms/$encodedRid/members" -Headers @{ Authorization = "Bearer $AdminToken" }
            $memberIds = $members.chunk | ForEach-Object { $_.state_key }
            $hasManager = $memberIds -contains "@manager:${MatrixDomain}"
            $hasAdmin = $memberIds -contains "@${AdminUser}:${MatrixDomain}"
            if ($hasManager -and $hasAdmin -and $memberIds.Count -le 2) {
                $DmRoomId = $rid
                break
            }
        } catch { continue }
    }
} catch {}

if (-not $DmRoomId) {
    try {
        $createBody = @{ is_direct = $true; invite = @("@manager:${MatrixDomain}"); preset = "trusted_private_chat" } | ConvertTo-Json -Compress
        $createResp = Invoke-RestMethod -Uri "$baseUrl/_matrix/client/v3/createRoom" -Method Post -Body $createBody -ContentType "application/json" -Headers @{ Authorization = "Bearer $AdminToken" }
        $DmRoomId = $createResp.room_id
    } catch {
        Write-Warn "Could not find or create DM room with Manager."
    }
}

if ($DmRoomId) {
    if ($ImageTag) {
        $Message = "@manager:${MatrixDomain} Worker ${Name} 的所有配置已通过导入脚本创建完毕（Matrix 账号、Room、MinIO、Higress、openclaw.json、workers-registry 均已就绪）。请使用镜像 ${ImageTag} 启动此 Worker 的容器。"
    } else {
        $Message = "@manager:${MatrixDomain} Worker ${Name} 的所有配置已通过导入脚本创建完毕（Matrix 账号、Room、MinIO、Higress、openclaw.json、workers-registry 均已就绪）。请使用标准 Worker 镜像启动此 Worker 的容器。"
    }

    if ($Proxy) {
        $NoProxyList = "*.hiclaw.io,127.0.0.1,localhost"
        if ($NoProxy) { $NoProxyList += ",$NoProxy" }
        $Message += " 容器环境变量需设置 HTTP_PROXY=$Proxy HTTPS_PROXY=$Proxy NO_PROXY=$NoProxyList"
    }

    # Append cron job info if present
    $cronFile = Join-Path $TmpDir "crons/jobs.json"
    if (Test-Path $cronFile) {
        try {
            $cronJobs = Get-Content -Raw $cronFile | ConvertFrom-Json
            if ($cronJobs.Count -gt 0) {
                $cronSummary = $cronJobs | ForEach-Object {
                    $sched = if ($_.schedule.cron) { $_.schedule.cron } elseif ($_.schedule.every) { $_.schedule.every } else { "unknown" }
                    $payload = if ($_.payload.agentTurn.parts[0].text) { $_.payload.agentTurn.parts[0].text.Substring(0, [Math]::Min(80, $_.payload.agentTurn.parts[0].text.Length)) } else { "N/A" }
                    $name = if ($_.name) { $_.name } else { $_.id }
                    "- ${name}: schedule=${sched}, payload=${payload}"
                }
                $Message += "`n`n此 Worker 从源环境迁移了 $($cronJobs.Count) 个定时任务，请为 ${Name} 创建对应的 scheduled tasks:`n$($cronSummary -join "`n")"
            }
        } catch {}
    }

    $txnId = "migrate-$([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())-$$"
    $encodedDmRid = [System.Uri]::EscapeDataString($DmRoomId)
    $msgBody = @{ msgtype = "m.text"; body = $Message } | ConvertTo-Json -Compress

    try {
        Invoke-RestMethod -Uri "$baseUrl/_matrix/client/v3/rooms/$encodedDmRid/send/m.room.message/$txnId" `
            -Method Put -Body $msgBody -ContentType "application/json" `
            -Headers @{ Authorization = "Bearer $AdminToken" } | Out-Null
        Write-Log "  Message sent to Manager"
    } catch {
        Write-Warn "Failed to send message to Manager: $_"
    }
}

# ============================================================
# Cleanup and done
# ============================================================

Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
if ($DownloadedZip -and (Test-Path $DownloadedZip)) { Remove-Item $DownloadedZip -Force -ErrorAction SilentlyContinue }

Write-Host ""
Write-Host (Get-Msg "done.title")
Write-Host "  Worker: $Name"
Write-Host "  Image: $(if ($ImageTag) { $ImageTag } else { 'standard' })"
Write-Host "  Room: $RoomId"
Write-Host ""
Write-Host (Get-Msg "done.hint")
Write-Host ""
