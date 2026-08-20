[CmdletBinding()]
param(
    [switch]$SecurityApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Test-Administrator {
    $principal = [Security.Principal.WindowsPrincipal](
        [Security.Principal.WindowsIdentity]::GetCurrent()
    )
    return $principal.IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator
    )
}

function Write-Utf8NoBom {
    param([string]$LiteralPath, [object]$Value)
    $json = $Value | ConvertTo-Json -Depth 16
    [IO.File]::WriteAllText(
        $LiteralPath,
        $json + [Environment]::NewLine,
        [Text.UTF8Encoding]::new($false)
    )
}

function Invoke-NativeCapture {
    param(
        [string]$LiteralPath,
        [string[]]$ArgumentList,
        [string]$OutputPath
    )
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $lines = @(& $LiteralPath @ArgumentList 2>&1)
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    [IO.File]::WriteAllLines(
        $OutputPath,
        [string[]]@($lines | ForEach-Object { $_.ToString() }),
        [Text.UTF8Encoding]::new($false)
    )
    return [pscustomobject]@{ ExitCode = $exitCode; Lines = @($lines) }
}

function Invoke-Probe {
    param(
        [string]$Name,
        [string[]]$Arguments
    )
    $path = Join-Path $ArtifactRoot "$Name.json"
    $capture = Invoke-NativeCapture -LiteralPath $ExecutablePath `
        -ArgumentList (@('storage', 'broker-probe') + $Arguments) `
        -OutputPath $path
    if ($capture.ExitCode -ne 0) {
        throw "$Name probe process failed: exit=$($capture.ExitCode)"
    }
    return Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
}

function Get-FileBackedDisks {
    return @(
        Get-Disk -ErrorAction SilentlyContinue |
            Where-Object { $_.BusType -eq 'File Backed Virtual' } |
            Select-Object Number, FriendlyName, BusType, PartitionStyle
    )
}

function Invoke-ServiceAccountProbe {
    param(
        [string]$TaskName,
        [string]$UserSid,
        [string]$OutputName
    )
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
        throw "Unique scheduled task already exists: $TaskName"
    }
    $outputPath = Join-Path $ScratchRoot "$OutputName.json"
    $arguments = '-NoProfile -ExecutionPolicy Bypass -File "{0}" -ExecutablePath "{1}" -OutputPath "{2}"' -f `
        $TaskProbePath, $ScratchExecutablePath, $outputPath
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $arguments
    $principal = New-ScheduledTaskPrincipal -UserId $UserSid `
        -LogonType ServiceAccount -RunLevel Highest
    Register-ScheduledTask -TaskName $TaskName -Action $action `
        -Principal $principal | Out-Null
    $script:CreatedTasks += $TaskName
    Start-ScheduledTask -TaskName $TaskName

    $deadline = (Get-Date).AddSeconds(30)
    while (-not (Test-Path -LiteralPath $outputPath -PathType Leaf)) {
        if ((Get-Date) -ge $deadline) {
            $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
            $info = Get-ScheduledTaskInfo -TaskName $TaskName -ErrorAction SilentlyContinue
            throw "Timed out waiting for $TaskName; state=$($task.State) result=$($info.LastTaskResult)"
        }
        Start-Sleep -Milliseconds 200
    }
    $destination = Join-Path $ArtifactRoot "$OutputName.json"
    Copy-Item -LiteralPath $outputPath -Destination $destination
    return Get-Content -LiteralPath $destination -Raw | ConvertFrom-Json
}

if (-not $SecurityApproved) {
    throw 'Pass -SecurityApproved after reviewing the exact service-account probe and cleanup contract.'
}
if (-not (Test-Administrator)) {
    throw 'Administrator PowerShell is required.'
}

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-native-security-$Stamp"
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffNativeSecurity-$Stamp"
$ScratchRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffSecurityProbe-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-security.exe'
$ScratchExecutablePath = Join-Path $ScratchRoot 'testplay-vhdx-diff-security.exe'
$TaskProbePath = Join-Path $ScratchRoot 'invoke-probe.ps1'
$TranscriptPath = Join-Path $ArtifactRoot 'terminal-transcript.txt'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"
$TaskPrefix = "TestPlay-VHDXDiff-Security-$Stamp"
$SystemTask = "$TaskPrefix-SYSTEM"
$LocalServiceTask = "$TaskPrefix-LOCAL-SERVICE"

foreach ($path in @($ArtifactRoot, $StoreRoot, $ScratchRoot)) {
    if (Test-Path -LiteralPath $path) {
        throw "Unique path already exists: $path"
    }
}
if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) {
    throw 'TestPlayStorageBroker already exists; this harness will not replace it.'
}
if (Test-Path -LiteralPath $ReceiptPath) {
    throw "An install receipt already exists; this harness will not replace it: $ReceiptPath"
}
if (Test-Path -LiteralPath $WorkspaceRoot) {
    throw "Workspace root already exists; this harness will not alter it: $WorkspaceRoot"
}

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
New-Item -ItemType Directory -Path $ScratchRoot | Out-Null
$PreDisks = @(Get-FileBackedDisks)
$Installed = $false
$Uninstalled = $false
$CreatedTasks = @()
$Failure = $null
$Started = Get-Date
$Allowed = $null
$ClaimedMismatch = $null
$RootInjection = $null
$Traversal = $null
$SystemProbe = $null
$LocalServiceProbe = $null

Start-Transcript -Path $TranscriptPath -Force | Out-Null
try {
    Push-Location $RepositoryRoot
    try {
        & go build -o $ExecutablePath .\cmd\testplay
        if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    }
    finally {
        Pop-Location
    }

    Copy-Item -LiteralPath $ExecutablePath -Destination $ScratchExecutablePath
    $taskProbe = @'
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ExecutablePath,
    [Parameter(Mandatory = $true)][string]$OutputPath
)
$ErrorActionPreference = 'Continue'
$lines = @(& $ExecutablePath storage broker-probe --operation hello 2>&1)
$exitCode = $LASTEXITCODE
$temporaryPath = "$OutputPath.tmp"
[IO.File]::WriteAllLines(
    $temporaryPath,
    [string[]]@($lines | ForEach-Object { $_.ToString() }),
    [Text.UTF8Encoding]::new($false)
)
Move-Item -LiteralPath $temporaryPath -Destination $OutputPath
exit $exitCode
'@
    [IO.File]::WriteAllText(
        $TaskProbePath,
        $taskProbe,
        [Text.UTF8Encoding]::new($false)
    )
    $aclResult = @(& icacls.exe $ScratchRoot '/inheritance:r' `
        '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' `
        '*S-1-5-19:(OI)(CI)M' 2>&1)
    $aclExit = $LASTEXITCODE
    [IO.File]::WriteAllLines(
        (Join-Path $ArtifactRoot 'probe-scratch-acl.txt'),
        [string[]]@($aclResult | ForEach-Object { $_.ToString() }),
        [Text.UTF8Encoding]::new($false)
    )
    if ($aclExit -ne 0) { throw "probe scratch ACL failed: exit=$aclExit" }

    $install = Invoke-NativeCapture -LiteralPath $ExecutablePath `
        -ArgumentList @('storage', 'install', '--root', $StoreRoot) `
        -OutputPath (Join-Path $ArtifactRoot 'storage-install.txt')
    if ($install.ExitCode -ne 0) { throw "storage install failed: exit=$($install.ExitCode)" }
    $Installed = $true

    $deadline = (Get-Date).AddSeconds(10)
    do {
        $Allowed = Invoke-Probe -Name 'allowed-user' -Arguments @('--operation', 'hello')
        if ($Allowed.status -eq 'PASS' -and $Allowed.response.ok) { break }
        Start-Sleep -Milliseconds 200
    } while ((Get-Date) -lt $deadline)
    if ($Allowed.status -ne 'PASS' -or -not $Allowed.response.ok) {
        throw 'Installed user could not complete the authenticated hello request.'
    }

    $ClaimedMismatch = Invoke-Probe -Name 'claimed-sid-mismatch' `
        -Arguments @('--operation', 'hello', '--claimed-user-sid', 'S-1-5-18')
    if ($ClaimedMismatch.status -ne 'REJECTED' -or
        $ClaimedMismatch.response.error.code -ne 'unauthorized-client' -or
        $ClaimedMismatch.transportAccessDenied) {
        throw 'Claimed SID mismatch was not rejected by broker authorization.'
    }

    $RootInjection = Invoke-Probe -Name 'workspace-root-injection' `
        -Arguments @('--operation', 'hello', '--workspace-root', 'C:\Windows\System32')
    if ($RootInjection.status -ne 'REJECTED' -or
        $RootInjection.response.error.operation -ne 'validate-client-workspace-root') {
        throw 'Client-selected workspace root was not explicitly rejected.'
    }

    $Traversal = Invoke-Probe -Name 'workspace-id-traversal' `
        -Arguments @('--operation', 'hello', '--workspace-id', '..\escape')
    if ($Traversal.status -ne 'REJECTED' -or
        $Traversal.response.error.operation -ne 'validate-workspace-id') {
        throw 'Workspace identifier traversal was not rejected.'
    }

    $SystemProbe = Invoke-ServiceAccountProbe -TaskName $SystemTask `
        -UserSid 'S-1-5-18' -OutputName 'system-token-probe'
    if ($SystemProbe.callerSid -ne 'S-1-5-18' -or
        $SystemProbe.status -ne 'REJECTED' -or
        $SystemProbe.transportAccessDenied -or
        $SystemProbe.response.error.code -ne 'unauthorized-client') {
        throw 'SYSTEM token was not authenticated and rejected at the broker authorization boundary.'
    }

    $LocalServiceProbe = Invoke-ServiceAccountProbe -TaskName $LocalServiceTask `
        -UserSid 'S-1-5-19' -OutputName 'local-service-token-probe'
    if ($LocalServiceProbe.callerSid -ne 'S-1-5-19' -or
        $LocalServiceProbe.status -ne 'REJECTED' -or
        -not $LocalServiceProbe.transportAccessDenied) {
        throw 'LOCAL SERVICE token was not rejected by the named-pipe DACL.'
    }

    $status = Invoke-NativeCapture -LiteralPath $ExecutablePath `
        -ArgumentList @('storage', 'status', '--json') `
        -OutputPath (Join-Path $ArtifactRoot 'storage-status.json')
    if ($status.ExitCode -ne 0) { throw "storage status failed: exit=$($status.ExitCode)" }
    $statusJSON = Get-Content -LiteralPath (Join-Path $ArtifactRoot 'storage-status.json') -Raw | ConvertFrom-Json
    if ($statusJSON.parentCount -ne 0 -or $statusJSON.activeChildCount -ne 0 -or
        $statusJSON.retainedChildCount -ne 0 -or $statusJSON.pendingCount -ne 0 -or
        $statusJSON.quarantineCount -ne 0) {
        throw 'Security probes mutated broker storage state.'
    }
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    foreach ($taskName in @($CreatedTasks)) {
        try {
            Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
            Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction Stop
        }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
        }
    }
    if ($Installed) {
        try {
            $uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath `
                -ArgumentList @('storage', 'uninstall') `
                -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt')
            $Uninstalled = $uninstall.ExitCode -eq 0
            if (-not $Uninstalled -and $null -eq $Failure) {
                $Failure = "storage uninstall failed: exit=$($uninstall.ExitCode)"
            }
        }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
        }
    }
    try {
        if (Test-Path -LiteralPath $ScratchRoot) {
            Remove-Item -LiteralPath $ScratchRoot -Recurse -Force
        }
    }
    catch {
        if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
    }
    Stop-Transcript | Out-Null
}

$PostDisks = @(Get-FileBackedDisks)
$PreIDs = @($PreDisks | ForEach-Object { $_.Number })
$NewDisks = @($PostDisks | Where-Object { $PreIDs -notcontains $_.Number })
$RemainingTasks = @(
    Get-ScheduledTask -ErrorAction SilentlyContinue |
        Where-Object { $_.TaskName -in @($SystemTask, $LocalServiceTask) }
)
$ResidualZero = (
    $NewDisks.Count -eq 0 -and
    $RemainingTasks.Count -eq 0 -and
    -not (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) -and
    -not (Test-Path -LiteralPath $ReceiptPath) -and
    -not (Test-Path -LiteralPath $StoreRoot) -and
    -not (Test-Path -LiteralPath $ScratchRoot) -and
    -not (Test-Path -LiteralPath $WorkspaceRoot)
)
$Passed = (
    $null -eq $Failure -and $Uninstalled -and $ResidualZero -and
    $null -ne $Allowed -and $null -ne $ClaimedMismatch -and
    $null -ne $RootInjection -and $null -ne $Traversal -and
    $null -ne $SystemProbe -and $null -ne $LocalServiceProbe
)
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_BROKER_SECURITY_NATIVE_PASS' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    repository = $RepositoryRoot
    storeRoot = $StoreRoot
    allowedUserHello = $Allowed
    claimedSidMismatch = $ClaimedMismatch
    workspaceRootInjection = $RootInjection
    workspaceIdTraversal = $Traversal
    systemTokenProbe = $SystemProbe
    localServiceTokenProbe = $LocalServiceProbe
    installed = $Installed
    uninstalled = $Uninstalled
    residualZero = $ResidualZero
    remainingScheduledTasks = @($RemainingTasks)
    preFileBackedDisks = @($PreDisks)
    postFileBackedDisks = @($PostDisks)
    newFileBackedDisks = @($NewDisks)
    failure = $Failure
    notMeasured = @(
        'remote client attempt from a second machine',
        'GNF single/multi worker',
        'forced termination recovery',
        'broker restart recovery',
        'Windows reboot recovery',
        'quota/LRU native behavior',
        'production readiness',
        'release readiness'
    )
}
Write-Utf8NoBom -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash

Write-Output "VHDX_DIFF_SECURITY_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_SECURITY_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_SECURITY_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
