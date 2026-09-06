[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'honeybee-protocol-v3-recovery-common.ps1')

function New-Event {
    param([string]$Type, [string]$Kind = '')
    return [pscustomobject]@{ type = $Type; payload = [pscustomobject]@{ kind = $Kind } }
}

$Empty = @(Select-Protocol3FaultEvents -Events @() -ReadySignalPresent $true)
if ($Empty.Count -ne 1 -or $Empty[0].ready) { throw 'Empty fault selection contract failed.' }

$FaultEvents = @(
    New-Event 'workspace.acquired'
    New-Event 'editor.ownership-established'
    New-Event 'editor.bridge-bound'
    New-Event 'capability.started' 'warm-test'
    New-Event 'capability.process-started' 'warm-test'
)
$Selected = Select-Protocol3FaultEvents -Events $FaultEvents -ReadySignalPresent $true
if (-not $Selected.ready -or $Selected.counts.warmProcess -ne 1) { throw 'Single exact fault selection failed.' }
$Duplicate = Select-Protocol3FaultEvents -Events @($FaultEvents + (New-Event 'capability.process-started' 'warm-test')) -ReadySignalPresent $true
if ($Duplicate.ready -or $Duplicate.counts.warmProcess -ne 2) { throw 'Duplicate fault event was not rejected.' }

$FailedTerminal = Get-Protocol3TerminalEvidence -Events @(
    New-Event 'capability.started' 'warm-test'
    New-Event 'capability.process-started' 'warm-test'
    New-Event 'capability.failed' 'warm-test'
    New-Event 'workspace.released'
    New-Event 'workflow.failed'
)
if ($FailedTerminal.warmFailed -ne 1 -or $FailedTerminal.workspaceReleased -ne 1 -or $FailedTerminal.workflowFailed -ne 1) {
    throw 'Failed terminal evidence contract failed.'
}

if ((Get-Protocol3CleanupState $true $true $true $false) -ne 'released') { throw 'Released cleanup classification failed.' }
if ((Get-Protocol3CleanupState $false $true $true $true) -ne 'preserved') { throw 'Preserved cleanup classification failed.' }
if ((Get-Protocol3CleanupState $false $false $true $true) -ne 'uncertain') { throw 'Uncertain cleanup classification failed.' }

$Process = [pscustomobject]@{ processId = 42; processIdentity = 'win32:123'; executablePath = 'C:\tools\testplay.exe' }
if (-not (Test-Protocol3ProcessIdentity $Process $Process 'C:\tools\testplay.exe')) { throw 'Exact process identity was rejected.' }
$Reused = [pscustomobject]@{ processId = 42; processIdentity = 'win32:999'; executablePath = 'C:\tools\testplay.exe' }
if (Test-Protocol3ProcessIdentity $Process $Reused 'C:\tools\testplay.exe') { throw 'Reused PID was accepted.' }

$WorkspaceEvent = [pscustomobject]@{ payload = [pscustomobject]@{ workspaceId = 'workspace-1'; leaseId = 'lease-1' } }
$Journal = [pscustomobject]@{ workspaceId = 'workspace-1'; leaseId = 'lease-1'; runId = 'run-1'; ownershipToken = 'token-1'; workspacePath = 'C:\workspaces\workspace-1'; mountPath = 'C:\workspaces\workspace-1\Library' }
$Marker = [pscustomobject]@{ workspaceId = 'workspace-1'; leaseId = 'lease-1'; runId = 'run-1'; ownershipToken = 'token-1' }
if (-not (Test-Protocol3LeaseIdentity $WorkspaceEvent $Journal $Marker 'C:\workspaces')) { throw 'Exact workspace/lease/token binding was rejected.' }
$WrongMarker = [pscustomobject]@{ workspaceId = 'workspace-1'; leaseId = 'lease-1'; runId = 'run-1'; ownershipToken = 'wrong-token' }
if (Test-Protocol3LeaseIdentity $WorkspaceEvent $Journal $WrongMarker 'C:\workspaces') { throw 'Wrong ownership token was accepted.' }
$WrongWorkspace = [pscustomobject]@{ payload = [pscustomobject]@{ workspaceId = 'workspace-escape'; leaseId = 'lease-1' } }
if (Test-Protocol3LeaseIdentity $WrongWorkspace $Journal $Marker 'C:\workspaces') { throw 'Wrong workspace identity was accepted.' }

$Pointer = [pscustomobject]@{ schemaVersion = 1; statePath = 'C:\tmp\state.json'; stateSHA256 = 'AA'; harnessPath = 'C:\tmp\harness.ps1'; harnessSHA256 = 'BB' }
if (-not (Test-Protocol3RebootPointer $Pointer 'C:\tmp\state.json' 'aa' 'C:\tmp\harness.ps1' 'bb')) { throw 'Exact reboot pointer was rejected.' }
if (Test-Protocol3RebootPointer $Pointer 'C:\tmp\other.json' 'aa' 'C:\tmp\harness.ps1' 'bb') { throw 'Mismatched reboot pointer was accepted.' }

function Assert-ArrayRoundTrip {
    param([object[]]$Values, [int]$ExpectedCount)
    $Json = [ordered]@{ values = @($Values) } | ConvertTo-Json -Depth 4 | ConvertFrom-Json
    if (@($Json.values).Count -ne $ExpectedCount) { throw "Array JSON contract failed: expected=$ExpectedCount" }
}
Assert-ArrayRoundTrip -Values @() -ExpectedCount 0
Assert-ArrayRoundTrip -Values @('one') -ExpectedCount 1
Assert-ArrayRoundTrip -Values @('one', 'two') -ExpectedCount 2

Write-Output 'HONEYBEE_PROTOCOL3_RECOVERY_SELF_CHECK=PASS'
