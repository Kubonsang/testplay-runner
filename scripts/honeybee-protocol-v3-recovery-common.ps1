Set-StrictMode -Version Latest

function Select-Protocol3FaultEvents {
    param([object[]]$Events, [bool]$ReadySignalPresent)
    $Workspace = @($Events | Where-Object type -eq 'workspace.acquired')
    $Ownership = @($Events | Where-Object type -eq 'editor.ownership-established')
    $Binding = @($Events | Where-Object type -eq 'editor.bridge-bound')
    $WarmStarted = @($Events | Where-Object { $_.type -eq 'capability.started' -and $_.payload.kind -eq 'warm-test' })
    $WarmProcess = @($Events | Where-Object { $_.type -eq 'capability.process-started' -and $_.payload.kind -eq 'warm-test' })
    $Ready = $ReadySignalPresent -and $Workspace.Count -eq 1 -and $Ownership.Count -eq 1 -and
        $Binding.Count -eq 1 -and $WarmStarted.Count -eq 1 -and $WarmProcess.Count -eq 1
    return [ordered]@{
        ready = $Ready
        workspace = if ($Workspace.Count -eq 1) { $Workspace[0] } else { $null }
        ownership = if ($Ownership.Count -eq 1) { $Ownership[0] } else { $null }
        binding = if ($Binding.Count -eq 1) { $Binding[0] } else { $null }
        warmStarted = if ($WarmStarted.Count -eq 1) { $WarmStarted[0] } else { $null }
        warmProcess = if ($WarmProcess.Count -eq 1) { $WarmProcess[0] } else { $null }
        counts = [ordered]@{
            workspace = $Workspace.Count
            ownership = $Ownership.Count
            binding = $Binding.Count
            warmStarted = $WarmStarted.Count
            warmProcess = $WarmProcess.Count
        }
    }
}

function Get-Protocol3TerminalEvidence {
    param([object[]]$Events)
    return [ordered]@{
        workspaceReleased = @($Events | Where-Object type -eq 'workspace.released').Count
        workflowCompleted = @($Events | Where-Object type -eq 'workflow.completed').Count
        workflowFailed = @($Events | Where-Object type -eq 'workflow.failed').Count
        warmStarted = @($Events | Where-Object { $_.type -eq 'capability.started' -and $_.payload.kind -eq 'warm-test' }).Count
        warmProcessStarted = @($Events | Where-Object { $_.type -eq 'capability.process-started' -and $_.payload.kind -eq 'warm-test' }).Count
        warmCompleted = @($Events | Where-Object { $_.type -eq 'capability.completed' -and $_.payload.kind -eq 'warm-test' }).Count
        warmFailed = @($Events | Where-Object { $_.type -eq 'capability.failed' -and $_.payload.kind -eq 'warm-test' }).Count
        eventCount = $Events.Count
    }
}

function Get-Protocol3CleanupState {
    param(
        [bool]$PoolRemoved,
        [bool]$IdentityCertain,
        [bool]$AttachmentsAbsent,
        [bool]$AuthoritativeStatePreserved
    )
    if ($PoolRemoved -and $IdentityCertain -and $AttachmentsAbsent) { return 'released' }
    if ($IdentityCertain -and $AttachmentsAbsent -and $AuthoritativeStatePreserved) { return 'preserved' }
    return 'uncertain'
}

function Test-Protocol3ProcessIdentity {
    param([object]$Expected, [object]$Actual, [string]$ExpectedPath)
    if ($null -eq $Expected -or $null -eq $Actual -or [int]$Expected.processId -le 0) { return $false }
    return [int]$Expected.processId -eq [int]$Actual.processId -and
        [string]$Expected.processIdentity -eq [string]$Actual.processIdentity -and
        [string]::Equals([IO.Path]::GetFullPath([string]$Actual.executablePath), [IO.Path]::GetFullPath($ExpectedPath), [StringComparison]::OrdinalIgnoreCase)
}

function Test-Protocol3LeaseIdentity {
    param([object]$WorkspaceEvent, [object]$Journal, [object]$Marker, [string]$WorkspaceRoot)
    if ($null -eq $WorkspaceEvent -or $null -eq $Journal -or $null -eq $Marker) { return $false }
    $ExpectedWorkspacePath = Join-Path $WorkspaceRoot ([string]$WorkspaceEvent.payload.workspaceId)
    $ExpectedMountPath = Join-Path $ExpectedWorkspacePath 'Library'
    return [string]$WorkspaceEvent.payload.workspaceId -eq [string]$Journal.workspaceId -and
        [string]$WorkspaceEvent.payload.leaseId -eq [string]$Journal.leaseId -and
        [string]$Marker.workspaceId -eq [string]$Journal.workspaceId -and
        [string]$Marker.leaseId -eq [string]$Journal.leaseId -and
        [string]$Marker.runId -eq [string]$Journal.runId -and
        [string]$Marker.ownershipToken -eq [string]$Journal.ownershipToken -and
        [string]::Equals([IO.Path]::GetFullPath([string]$Journal.workspacePath), [IO.Path]::GetFullPath($ExpectedWorkspacePath), [StringComparison]::OrdinalIgnoreCase) -and
        [string]::Equals([IO.Path]::GetFullPath([string]$Journal.mountPath), [IO.Path]::GetFullPath($ExpectedMountPath), [StringComparison]::OrdinalIgnoreCase)
}

function Test-Protocol3RebootPointer {
    param(
        [object]$Pointer,
        [string]$StatePath,
        [string]$StateSHA256,
        [string]$HarnessPath,
        [string]$HarnessSHA256
    )
    if ($null -eq $Pointer -or [int]$Pointer.schemaVersion -ne 1) { return $false }
    return [string]::Equals([IO.Path]::GetFullPath([string]$Pointer.statePath), [IO.Path]::GetFullPath($StatePath), [StringComparison]::OrdinalIgnoreCase) -and
        [string]::Equals([string]$Pointer.stateSHA256, $StateSHA256, [StringComparison]::OrdinalIgnoreCase) -and
        [string]::Equals([IO.Path]::GetFullPath([string]$Pointer.harnessPath), [IO.Path]::GetFullPath($HarnessPath), [StringComparison]::OrdinalIgnoreCase) -and
        [string]::Equals([string]$Pointer.harnessSHA256, $HarnessSHA256, [StringComparison]::OrdinalIgnoreCase)
}
