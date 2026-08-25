param(
    [string]$Namespace = 'agentteams',
    [Parameter(Mandatory = $true)]
    [string]$WorkerName,
    [int]$ReplyTimeoutSeconds = 420
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-FreeTcpPort {
    $Listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $Listener.Start()
    try { return ([Net.IPEndPoint]$Listener.LocalEndpoint).Port }
    finally { $Listener.Stop() }
}

function Wait-TcpPort([int]$Port) {
    $Deadline = [DateTime]::UtcNow.AddSeconds(20)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Client = [Net.Sockets.TcpClient]::new()
        try {
            $Connected = $Client.ConnectAsync('127.0.0.1', $Port)
            if ($Connected.Wait(250) -and $Client.Connected) { return }
        }
        catch { }
        finally { $Client.Dispose() }
        Start-Sleep -Milliseconds 100
    }
    throw "Timed out waiting for Matrix port-forward on $Port"
}

function Get-SecretText([object]$Secret, [string]$Key) {
    $Property = $Secret.data.PSObject.Properties[$Key]
    if ($null -eq $Property) { throw "Secret is missing $Key" }
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([string]$Property.Value))
}

function Get-PodEnv([object]$Pod, [string]$Name) {
    $Entry = @($Pod.spec.containers[0].env) | Where-Object { $_.name -eq $Name } | Select-Object -First 1
    if ($null -eq $Entry -or [string]::IsNullOrWhiteSpace([string]$Entry.value)) {
        throw "Pod is missing literal env $Name"
    }
    return [string]$Entry.value
}

function Invoke-MatrixJson([string]$Method, [string]$Url, [string]$Token, [object]$Body = $null) {
    $Headers = @{ Authorization = "Bearer $Token" }
    if ($null -eq $Body) {
        return Invoke-RestMethod -Method $Method -Uri $Url -Headers $Headers
    }
    return Invoke-RestMethod -Method $Method -Uri $Url -Headers $Headers -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Depth 10 -Compress)
}

function Get-WorkerPod {
    $Items = (& kubectl get pods -n $Namespace -l "agentteams.io/worker=$WorkerName" -o json | ConvertFrom-Json).items
    return @($Items) | Select-Object -First 1
}

function Wait-ForReplacementPod([string]$PreviousUid) {
    $Deadline = [DateTime]::UtcNow.AddSeconds(240)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Pod = Get-WorkerPod
        if ($null -ne $Pod -and [string]$Pod.metadata.uid -ne $PreviousUid) {
            $Statuses = @($Pod.status.containerStatuses)
            if ($Pod.status.phase -eq 'Running' -and $Statuses.Count -gt 0 -and @($Statuses | Where-Object { -not $_.ready }).Count -eq 0) {
                $Logs = (& kubectl logs $Pod.metadata.name -n $Namespace --tail=80 | Out-String)
                if ($Logs.Contains("ready worker=$WorkerName")) { return $Pod }
            }
        }
        Start-Sleep -Seconds 3
    }
    throw 'Controller did not recreate a Ready DSH Worker Pod within 240 seconds'
}

function Send-And-WaitForReply(
    [string]$Marker,
    [string]$RoomId,
    [string]$WorkerUserId,
    [string]$MatrixBase,
    [string]$AdminToken,
    [Collections.Generic.List[string]]$CleanupEventIds
) {
    $EncodedRoom = [uri]::EscapeDataString($RoomId)
    $Transaction = [guid]::NewGuid().ToString('N')
    $Request = Invoke-MatrixJson 'PUT' "$MatrixBase/_matrix/client/v3/rooms/$EncodedRoom/send/m.room.message/$Transaction" $AdminToken @{
        msgtype = 'm.text'
        body = "Reply with exactly $Marker and nothing else."
    }
    $RequestEventId = [string]$Request.event_id
    if ([string]::IsNullOrWhiteSpace($RequestEventId)) { throw 'Matrix send returned no event_id' }
    $CleanupEventIds.Add($RequestEventId)

    $Deadline = [DateTime]::UtcNow.AddSeconds($ReplyTimeoutSeconds)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $Messages = Invoke-MatrixJson 'GET' "$MatrixBase/_matrix/client/v3/rooms/$EncodedRoom/messages?dir=b&limit=100" $AdminToken
        $Replies = @(@($Messages.chunk) | Where-Object {
            if ($_.type -ne 'm.room.message' -or $_.sender -ne $WorkerUserId) { return $false }
            $RelatesTo = $_.content.PSObject.Properties['m.relates_to']
            if ($null -eq $RelatesTo) { return $false }
            $InReplyTo = $RelatesTo.Value.PSObject.Properties['m.in_reply_to']
            if ($null -eq $InReplyTo) { return $false }
            $RelatedEvent = $InReplyTo.Value.PSObject.Properties['event_id']
            return $null -ne $RelatedEvent -and [string]$RelatedEvent.Value -eq $RequestEventId
        })
        if ($Replies.Count -gt 0) {
            $Reply = $Replies | Select-Object -First 1
            $CleanupEventIds.Add([string]$Reply.event_id)
            $Actual = ([string]$Reply.content.body).Trim()
            if ($Actual -ne $Marker) {
                throw "DSH replied to the event, but not with the requested marker: $Actual"
            }
            return [string]$Reply.event_id
        }
        Start-Sleep -Seconds 5
    }
    throw "Timed out waiting for DSH reply to $RequestEventId"
}

$Worker = & kubectl get worker $WorkerName -n $Namespace -o json | ConvertFrom-Json
if ($Worker.status.phase -ne 'Running') { throw "Worker $WorkerName is not Running" }
$WorkerUserId = [string]$Worker.status.matrixUserID
$Pod = Get-WorkerPod
if ($null -eq $Pod) { throw "Worker $WorkerName has no Pod" }
$RoomId = Get-PodEnv $Pod 'AGENTTEAMS_WORKER_ROOM_ID'

$RuntimeSecret = & kubectl get secret agentteams-runtime-env -n $Namespace -o json | ConvertFrom-Json
$AdminUser = Get-SecretText $RuntimeSecret 'AGENTTEAMS_ADMIN_USER'
$AdminPassword = Get-SecretText $RuntimeSecret 'AGENTTEAMS_ADMIN_PASSWORD'
$MatrixPort = Get-FreeTcpPort
$MatrixBase = "http://127.0.0.1:$MatrixPort"
$Forward = $null
$AdminToken = ''
$CleanupEventIds = [Collections.Generic.List[string]]::new()

try {
    $Forward = Start-Process -FilePath (Get-Command kubectl -ErrorAction Stop).Source -ArgumentList @(
        'port-forward', '-n', $Namespace, 'service/agentteams-tuwunel', "${MatrixPort}:6167"
    ) -PassThru -WindowStyle Hidden
    Wait-TcpPort $MatrixPort

    $Login = Invoke-RestMethod -Method POST -Uri "$MatrixBase/_matrix/client/v3/login" -ContentType 'application/json' -Body (@{
        type = 'm.login.password'
        identifier = @{ type = 'm.id.user'; user = $AdminUser }
        password = $AdminPassword
    } | ConvertTo-Json -Depth 5 -Compress)
    $AdminToken = [string]$Login.access_token
    if ([string]::IsNullOrWhiteSpace($AdminToken)) { throw 'Matrix admin login returned no access token' }

    $FirstMarker = 'DSH_CONTROLLER_E2E_' + [guid]::NewGuid().ToString('N')
    Send-And-WaitForReply $FirstMarker $RoomId $WorkerUserId $MatrixBase $AdminToken $CleanupEventIds | Out-Null
    Write-Output 'first Matrix -> DSH model -> Matrix round trip: PASS'

    & kubectl exec $Pod.metadata.name -n $Namespace -- bash -lc 'worker_home="${AGENTTEAMS_WORKER_HOME:-/root/agentteams-fs/agents/${AGENTTEAMS_WORKER_NAME}}"; test -s "$worker_home/runtime/matrix-next-batch" && mc stat "${AGENTTEAMS_STORAGE_PREFIX%/}/agents/${AGENTTEAMS_WORKER_NAME}/runtime/matrix-next-batch" >/dev/null'
    if ($LASTEXITCODE -ne 0) { throw 'Matrix sync cursor was not persisted to object storage' }
    Write-Output 'Matrix sync cursor persistence: PASS'

    $PreviousUid = [string]$Pod.metadata.uid
    & kubectl delete pod $Pod.metadata.name -n $Namespace --wait=true | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Deleting the DSH Worker Pod failed' }
    $Pod = Wait-ForReplacementPod $PreviousUid
    Write-Output "Controller recovery: PASS newPodUid=$($Pod.metadata.uid)"

    $SecondMarker = 'DSH_CONTROLLER_RECOVERY_' + [guid]::NewGuid().ToString('N')
    Send-And-WaitForReply $SecondMarker $RoomId $WorkerUserId $MatrixBase $AdminToken $CleanupEventIds | Out-Null

    $EncodedRoom = [uri]::EscapeDataString($RoomId)
    $Messages = Invoke-MatrixJson 'GET' "$MatrixBase/_matrix/client/v3/rooms/$EncodedRoom/messages?dir=b&limit=100" $AdminToken
    foreach ($Marker in @($FirstMarker, $SecondMarker)) {
        $Count = @($Messages.chunk | Where-Object {
            if ($_.sender -ne $WorkerUserId) { return $false }
            $Body = $_.content.PSObject.Properties['body']
            return $null -ne $Body -and ([string]$Body.Value).Trim() -eq $Marker
        }).Count
        if ($Count -ne 1) { throw "Expected one reply for $Marker after recovery, found $Count" }
    }
    Write-Output 'post-recovery Matrix round trip without replay: PASS'
}
finally {
    if (-not [string]::IsNullOrWhiteSpace($AdminToken) -and -not [string]::IsNullOrWhiteSpace($RoomId)) {
        $EncodedRoom = [uri]::EscapeDataString($RoomId)
        foreach ($EventId in $CleanupEventIds) {
            try {
                $EncodedEvent = [uri]::EscapeDataString($EventId)
                $Transaction = [guid]::NewGuid().ToString('N')
                Invoke-MatrixJson 'PUT' "$MatrixBase/_matrix/client/v3/rooms/$EncodedRoom/redact/$EncodedEvent/$Transaction" $AdminToken @{ reason = 'AgentTeams DSH lifecycle smoke cleanup' } | Out-Null
            }
            catch { }
        }
        try {
            Invoke-MatrixJson 'POST' "$MatrixBase/_matrix/client/v3/logout" $AdminToken | Out-Null
        }
        catch { }
    }
    if ($null -ne $Forward -and -not $Forward.HasExited) {
        Stop-Process -Id $Forward.Id -Force
        $Forward.WaitForExit()
    }
}
