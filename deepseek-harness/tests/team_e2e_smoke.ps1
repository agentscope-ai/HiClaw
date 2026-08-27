param(
    [string]$Namespace = 'agentteams',
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$Image,
    [int]$ReplyTimeoutSeconds = 420
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lib/test-helpers.ps1')

function Get-Worker([string]$Name) {
    return & kubectl get worker $Name -n $Namespace -o json | ConvertFrom-Json
}

function Get-WorkerPod([string]$Name) {
    $Items = (& kubectl get pods -n $Namespace -l "agentteams.io/worker=$Name" -o json | ConvertFrom-Json).items
    return @($Items) | Select-Object -First 1
}

function Wait-WorkerRunning([string]$Name) {
    $Deadline = [DateTime]::UtcNow.AddSeconds(300)
    while ([DateTime]::UtcNow -lt $Deadline) {
        try {
            $Worker = Get-Worker $Name
            $Pod = Get-WorkerPod $Name
            if ($Worker.status.phase -eq 'Running' -and $null -ne $Pod -and $Pod.status.phase -eq 'Running') {
                $Statuses = @($Pod.status.containerStatuses)
                if ($Statuses.Count -gt 0 -and @($Statuses | Where-Object { -not $_.ready }).Count -eq 0) {
                    $Logs = (& kubectl logs $Pod.metadata.name -n $Namespace --tail=100 | Out-String)
                    if ($Logs.Contains("ready worker=$Name")) { return $Worker }
                }
            }
        }
        catch { }
        Start-Sleep -Seconds 3
    }
    throw "Worker $Name did not become Ready"
}

function Wait-TeamActive([string]$Name) {
    $Deadline = [DateTime]::UtcNow.AddSeconds(300)
    while ([DateTime]::UtcNow -lt $Deadline) {
        try {
            $Team = & kubectl get team $Name -n $Namespace -o json | ConvertFrom-Json
            $Members = @($Team.status.members)
            if ($Team.status.phase -eq 'Active' -and $Team.status.leaderReady -and $Members.Count -eq 2) {
                if (@($Members | Where-Object { -not $_.ready }).Count -eq 0) { return $Team }
            }
        }
        catch { }
        Start-Sleep -Seconds 3
    }
    throw "Team $Name did not become Active"
}

function Wait-TeamRuntime([string]$WorkerName) {
    $Deadline = [DateTime]::UtcNow.AddSeconds(90)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Pod = Get-WorkerPod $WorkerName
        if ($null -ne $Pod) {
            $RuntimePath = "/root/agentteams-fs/agents/$WorkerName/runtime/runtime.yaml"
            & kubectl exec $Pod.metadata.name -n $Namespace -- grep -q '^team:' $RuntimePath 2>$null
            if ($LASTEXITCODE -eq 0) { return $Pod }
        }
        Start-Sleep -Seconds 2
    }
    throw "Worker $WorkerName did not refresh its Team runtime config"
}

function Wait-ReplacementPod([string]$WorkerName, [string]$PreviousUid) {
    $Deadline = [DateTime]::UtcNow.AddSeconds(300)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Pod = Get-WorkerPod $WorkerName
        if ($null -ne $Pod -and [string]$Pod.metadata.uid -ne $PreviousUid -and $Pod.status.phase -eq 'Running') {
            $Statuses = @($Pod.status.containerStatuses)
            if ($Statuses.Count -gt 0 -and @($Statuses | Where-Object { -not $_.ready }).Count -eq 0) {
                $Logs = (& kubectl logs $Pod.metadata.name -n $Namespace --tail=100 | Out-String)
                if ($Logs.Contains("ready worker=$WorkerName")) { return $Pod }
            }
        }
        Start-Sleep -Seconds 3
    }
    throw "Worker $WorkerName did not get a Ready replacement Pod"
}

function Send-Text([string]$RoomId, [string]$Body, [string]$MatrixBase, [string]$Token) {
    $Room = [uri]::EscapeDataString($RoomId)
    $Txn = [guid]::NewGuid().ToString('N')
    $Response = Invoke-MatrixJson 'PUT' "$MatrixBase/_matrix/client/v3/rooms/$Room/send/m.room.message/$Txn" $Token @{
        msgtype = 'm.text'
        body = $Body
    }
    $EventId = [string]$Response.event_id
    if ([string]::IsNullOrWhiteSpace($EventId)) { throw 'Matrix send returned no event_id' }
    return $EventId
}

function Test-ReplyRelation([object]$Event, [string]$RequestEventId) {
    if ($Event.type -ne 'm.room.message') { return $false }
    $Relation = $Event.content.PSObject.Properties['m.relates_to']
    if ($null -eq $Relation) { return $false }
    $InReplyTo = $Relation.Value.PSObject.Properties['m.in_reply_to']
    if ($null -eq $InReplyTo) { return $false }
    $EventId = $InReplyTo.Value.PSObject.Properties['event_id']
    return $null -ne $EventId -and [string]$EventId.Value -eq $RequestEventId
}

function Get-RoomMessages([string]$RoomId, [string]$MatrixBase, [string]$Token) {
    $Room = [uri]::EscapeDataString($RoomId)
    return @(Invoke-MatrixJson 'GET' "$MatrixBase/_matrix/client/v3/rooms/$Room/messages?dir=b&limit=200" $Token).chunk
}

function Wait-TextReply(
    [string]$RoomId,
    [string]$Sender,
    [string]$RequestEventId,
    [string]$Expected,
    [string]$MatrixBase,
    [string]$Token
) {
    $Deadline = [DateTime]::UtcNow.AddSeconds($ReplyTimeoutSeconds)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Replies = @(Get-RoomMessages $RoomId $MatrixBase $Token | Where-Object {
            $_.sender -eq $Sender -and (Test-ReplyRelation $_ $RequestEventId) -and $_.content.msgtype -eq 'm.text'
        })
        if ($Replies.Count -gt 0) {
            $Reply = $Replies | Select-Object -First 1
            $Actual = ([string]$Reply.content.body).Trim()
            if ($Actual -ne $Expected) { throw "Expected '$Expected' from $Sender, got '$Actual'" }
            return $Reply
        }
        Start-Sleep -Seconds 5
    }
    throw "Timed out waiting for $Sender to reply to $RequestEventId"
}

function Upload-MatrixFile([string]$Path, [string]$MatrixBase, [string]$Token) {
    $Headers = @{ Authorization = "Bearer $Token" }
    $Filename = [uri]::EscapeDataString([IO.Path]::GetFileName($Path))
    foreach ($Prefix in @('/_matrix/client/v1/media/upload', '/_matrix/media/v3/upload')) {
        try {
            $Response = Invoke-RestMethod -Method POST -Uri "$MatrixBase${Prefix}?filename=$Filename" -Headers $Headers -ContentType 'text/plain' -InFile $Path
            if (-not [string]::IsNullOrWhiteSpace([string]$Response.content_uri)) { return [string]$Response.content_uri }
        }
        catch {
            if ($Prefix -eq '/_matrix/media/v3/upload') { throw }
        }
    }
    throw 'Matrix media upload returned no content_uri'
}

function Send-File([string]$RoomId, [string]$Path, [string]$MatrixBase, [string]$Token) {
    $ContentUri = Upload-MatrixFile $Path $MatrixBase $Token
    $Room = [uri]::EscapeDataString($RoomId)
    $Txn = [guid]::NewGuid().ToString('N')
    $Info = Get-Item -LiteralPath $Path
    $Response = Invoke-MatrixJson 'PUT' "$MatrixBase/_matrix/client/v3/rooms/$Room/send/m.room.message/$Txn" $Token @{
        msgtype = 'm.file'
        body = $Info.Name
        filename = $Info.Name
        url = $ContentUri
        info = @{ mimetype = 'text/plain'; size = $Info.Length }
    }
    return [string]$Response.event_id
}

function Download-MatrixMedia([string]$MxcUrl, [string]$Destination, [string]$MatrixBase, [string]$Token) {
    $Parsed = [uri]$MxcUrl
    $Server = [uri]::EscapeDataString($Parsed.Host)
    $MediaId = [uri]::EscapeDataString($Parsed.AbsolutePath.Trim('/'))
    $Headers = @{ Authorization = "Bearer $Token" }
    foreach ($Prefix in @('/_matrix/client/v1/media/download', '/_matrix/media/v3/download')) {
        try {
            Invoke-WebRequest -Uri "$MatrixBase$Prefix/$Server/$MediaId" -Headers $Headers -OutFile $Destination | Out-Null
            return
        }
        catch {
            if ($Prefix -eq '/_matrix/media/v3/download') { throw }
        }
    }
}

function Wait-ReturnedFile(
    [string]$RoomId,
    [string]$Sender,
    [string]$RequestEventId,
    [string]$ExpectedContent,
    [string]$Destination,
    [string]$MatrixBase,
    [string]$Token
) {
    $Deadline = [DateTime]::UtcNow.AddSeconds($ReplyTimeoutSeconds)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Replies = @(Get-RoomMessages $RoomId $MatrixBase $Token | Where-Object {
            $_.sender -eq $Sender -and (Test-ReplyRelation $_ $RequestEventId) -and $_.content.msgtype -in @('m.file', 'm.image')
        })
        if ($Replies.Count -gt 0) {
            $Reply = $Replies | Select-Object -First 1
            Download-MatrixMedia ([string]$Reply.content.url) $Destination $MatrixBase $Token
            $Actual = ([IO.File]::ReadAllText($Destination)).Trim()
            if ($Actual -ne $ExpectedContent) { throw "Returned file contains '$Actual', expected '$ExpectedContent'" }
            return $Reply
        }
        Start-Sleep -Seconds 5
    }
    throw "Timed out waiting for returned file from $Sender"
}

$Suffix = [guid]::NewGuid().ToString('N').Substring(0, 6)
$LeaderName = "dsh-e2e-l-$Suffix"
$WorkerName = "dsh-e2e-w-$Suffix"
$TeamName = "dsh-e2e-t-$Suffix"
$Forward = $null
$AdminToken = ''
$TempRoot = Join-Path ([IO.Path]::GetTempPath()) "agentteams-dsh-team-$Suffix"
$Created = $false

try {
    $Yaml = @"
apiVersion: agentteams.io/v1beta1
kind: Worker
metadata:
  name: $LeaderName
  namespace: $Namespace
  labels:
    agentteams.io/controller: agentteams-controller
spec:
  runtime: deepseek-harness
  image: $Image
  model: deepseek-v4-flash
  identity: DSH Team E2E Leader
---
apiVersion: agentteams.io/v1beta1
kind: Worker
metadata:
  name: $WorkerName
  namespace: $Namespace
  labels:
    agentteams.io/controller: agentteams-controller
spec:
  runtime: deepseek-harness
  image: $Image
  model: deepseek-v4-flash
  identity: DSH Team E2E Worker
"@
    $Yaml | & kubectl apply -f - | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Creating DSH Workers failed' }
    $Created = $true
    $Leader = Wait-WorkerRunning $LeaderName
    $Worker = Wait-WorkerRunning $WorkerName

    $TeamYaml = @"
apiVersion: agentteams.io/v1beta1
kind: Team
metadata:
  name: $TeamName
  namespace: $Namespace
  labels:
    agentteams.io/controller: agentteams-controller
spec:
  description: DeepSeek Harness real Team E2E
  workerMembers:
    - name: $LeaderName
      role: team_leader
    - name: $WorkerName
      role: worker
"@
    $TeamYaml | & kubectl apply -f - | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Creating DSH Team failed' }
    $Team = Wait-TeamActive $TeamName
    $LeaderPod = Wait-TeamRuntime $LeaderName
    Wait-TeamRuntime $WorkerName | Out-Null
    Write-Output 'Controller-created DSH Leader + Worker Team runtime: PASS'

    $RuntimeSecret = & kubectl get secret agentteams-runtime-env -n $Namespace -o json | ConvertFrom-Json
    $AdminUser = Get-SecretText $RuntimeSecret 'AGENTTEAMS_ADMIN_USER'
    $AdminPassword = Get-SecretText $RuntimeSecret 'AGENTTEAMS_ADMIN_PASSWORD'
    $MatrixPort = Get-FreeTcpPort
    $MatrixBase = "http://127.0.0.1:$MatrixPort"
    $Forward = Start-Process -FilePath (Get-Command kubectl -ErrorAction Stop).Source -ArgumentList @(
        'port-forward', '-n', $Namespace, 'service/agentteams-tuwunel', "${MatrixPort}:6167"
    ) -PassThru -WindowStyle Hidden
    Wait-TcpPort $MatrixPort 30 'Matrix port-forward'
    $Login = Invoke-RestMethod -Method POST -Uri "$MatrixBase/_matrix/client/v3/login" -ContentType 'application/json' -Body (@{
        type = 'm.login.password'
        identifier = @{ type = 'm.id.user'; user = $AdminUser }
        password = $AdminPassword
    } | ConvertTo-Json -Depth 5 -Compress)
    $AdminToken = [string]$Login.access_token

    $LeaderUser = [string]$Leader.status.matrixUserID
    $WorkerUser = [string]$Worker.status.matrixUserID
    $TeamRoom = [string]$Team.status.teamRoomID
    $RoleRequest = Send-Text $TeamRoom 'Read the current TeamHarness context. Reply with exactly three whitespace-separated fields: TEAM_ROLE_OK, then member.runtimeName, then member.role. Do not add punctuation.' $MatrixBase $AdminToken
    Wait-TextReply $TeamRoom $LeaderUser $RoleRequest "TEAM_ROLE_OK $LeaderName team_leader" $MatrixBase $AdminToken | Out-Null
    Wait-TextReply $TeamRoom $WorkerUser $RoleRequest "TEAM_ROLE_OK $WorkerName worker" $MatrixBase $AdminToken | Out-Null
    Write-Output 'Team room reached both DSH roles with correct runtime context: PASS'

    $SecretWord = 'DSH_MEMORY_' + [guid]::NewGuid().ToString('N')
    $LeaderRoom = [string]$Leader.status.roomID
    $RememberRequest = Send-Text $LeaderRoom "Remember this exact token for my next message: $SecretWord. Reply exactly MEMORY_STORED." $MatrixBase $AdminToken
    Wait-TextReply $LeaderRoom $LeaderUser $RememberRequest 'MEMORY_STORED' $MatrixBase $AdminToken | Out-Null

    $PreviousUid = [string]$LeaderPod.metadata.uid
    & kubectl delete pod $LeaderPod.metadata.name -n $Namespace --wait=true | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Deleting Leader Pod failed' }
    $LeaderPod = Wait-ReplacementPod $LeaderName $PreviousUid
    Wait-TeamRuntime $LeaderName | Out-Null
    $RecallRequest = Send-Text $LeaderRoom 'What exact token did I ask you to remember in my previous message? Reply with that token only.' $MatrixBase $AdminToken
    Wait-TextReply $LeaderRoom $LeaderUser $RecallRequest $SecretWord $MatrixBase $AdminToken | Out-Null
    $RememberReplies = @(Get-RoomMessages $LeaderRoom $MatrixBase $AdminToken | Where-Object {
        $_.sender -eq $LeaderUser -and (Test-ReplyRelation $_ $RememberRequest) -and $_.content.msgtype -eq 'm.text'
    })
    if ($RememberReplies.Count -ne 1) { throw "Expected one pre-restart reply, found $($RememberReplies.Count)" }
    Write-Output 'Room-scoped DSH session resumed after Leader Pod replacement without duplicate reply: PASS'

    New-Item -ItemType Directory -Path $TempRoot | Out-Null
    $InputPath = Join-Path $TempRoot 'attachment-task.txt'
    $OutputPath = Join-Path $TempRoot 'returned-result.txt'
    $AttachmentMarker = 'DSH_ATTACHMENT_' + [guid]::NewGuid().ToString('N')
    [IO.File]::WriteAllText(
        $InputPath,
        "Create the file outbox/returned-result.txt containing exactly this token and no newline: $AttachmentMarker. Then reply briefly."
    )
    $WorkerRoom = [string]$Worker.status.roomID
    $FileRequest = Send-File $WorkerRoom $InputPath $MatrixBase $AdminToken
    Wait-ReturnedFile $WorkerRoom $WorkerUser $FileRequest $AttachmentMarker $OutputPath $MatrixBase $AdminToken | Out-Null
    Write-Output 'Matrix file -> Workspace inbox -> DSH -> Workspace outbox -> Matrix file: PASS'
}
finally {
    if (-not [string]::IsNullOrWhiteSpace($AdminToken)) {
        try { Invoke-MatrixJson 'POST' "$MatrixBase/_matrix/client/v3/logout" $AdminToken | Out-Null } catch { }
    }
    if ($null -ne $Forward -and -not $Forward.HasExited) {
        Stop-Process -Id $Forward.Id -Force
        $Forward.WaitForExit()
    }
    if ($Created) {
        & kubectl delete team $TeamName -n $Namespace --ignore-not-found=true --wait=true --timeout=180s | Out-Null
        & kubectl delete worker $LeaderName $WorkerName -n $Namespace --ignore-not-found=true --wait=true --timeout=180s | Out-Null
    }
    if (Test-Path -LiteralPath $TempRoot) {
        $Resolved = [IO.Path]::GetFullPath($TempRoot)
        $TempBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
        if (-not $Resolved.StartsWith($TempBase, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to clean non-temp path: $Resolved"
        }
        Remove-Item -LiteralPath $Resolved -Recurse -Force
    }
}
