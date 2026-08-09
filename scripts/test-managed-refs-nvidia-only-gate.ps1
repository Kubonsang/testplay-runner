[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding

function Test-Administrator {
  ([Security.Principal.WindowsPrincipal]::new(
    [Security.Principal.WindowsIdentity]::GetCurrent()
  )).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-ProblemCode([string]$InstanceId) {
  $property = Get-PnpDeviceProperty -InstanceId $InstanceId -KeyName 'DEVPKEY_Device_ProblemCode' -ErrorAction SilentlyContinue
  if ($null -eq $property) { return $null }
  [int]$property.Data
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if ($env:TESTPLAY_REFS_NVIDIA_MONITORS_CONFIRMED -ne 'YES') {
  throw 'Set TESTPLAY_REFS_NVIDIA_MONITORS_CONFIRMED=YES only after every test display is physically connected to NVIDIA.'
}

$base = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_HARDWARE_GATE_ARTIFACT_ROOT)) {
  Join-Path $env:TEMP 'testplay-refs-nvidia-only-gate'
} else {
  [IO.Path]::GetFullPath($env:TESTPLAY_REFS_HARDWARE_GATE_ARTIFACT_ROOT)
}
$stamp = [DateTime]::Now.ToString('yyyyMMdd-HHmmss-fff')
$artifactRoot = "$base-$stamp"
$zipPath = "$artifactRoot.zip"
if ((Test-Path -LiteralPath $artifactRoot) -or (Test-Path -LiteralPath $zipPath)) { throw 'fresh hardware-gate artifact path required' }
New-Item -ItemType Directory -Path $artifactRoot | Out-Null

$adapters = @(
  Get-PnpDevice -Class Display -ErrorAction Stop |
    ForEach-Object {
      [pscustomobject]@{
        friendlyName = $_.FriendlyName
        instanceId = $_.InstanceId
        status = [string]$_.Status
        problemCode = Get-ProblemCode $_.InstanceId
        disableCommand = "Disable-PnpDevice -InstanceId '$($_.InstanceId.Replace("'", "''"))' -Confirm:`$false"
        enableCommand = "Enable-PnpDevice -InstanceId '$($_.InstanceId.Replace("'", "''"))' -Confirm:`$false"
      }
    }
)
$amd = @($adapters | Where-Object { $_.friendlyName -match 'AMD|Radeon' })
$nvidia = @($adapters | Where-Object { $_.friendlyName -match 'NVIDIA|GeForce|RTX' })
$disks = @(Get-Disk -ErrorAction Stop | Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } | Select-Object Number, FriendlyName, SerialNumber)
$processes = @(
  Get-Process -ErrorAction SilentlyContinue |
    Where-Object { $_.ProcessName -match '^(Unity|Unity Hub|UnityHub|Unity\.Licensing\.Client|testplay-refs-probe|testplay-refs-unity|testplay-refs-gnf)' } |
    Select-Object Id, ProcessName, StartTime
)
$amdDisabled = $amd.Count -gt 0 -and @($amd | Where-Object { $_.problemCode -ne 22 }).Count -eq 0
$nvidiaReady = $nvidia.Count -gt 0 -and @($nvidia | Where-Object { $_.problemCode -ne 0 -and $null -ne $_.problemCode }).Count -eq 0
$ready = $amdDisabled -and $nvidiaReady -and $disks.Count -eq 0 -and $processes.Count -eq 0
$evidence = [ordered]@{
  schemaVersion = 1
  status = if ($ready) { 'NVIDIA_ONLY_READY' } else { 'BLOCKED' }
  measuredAt = [DateTime]::UtcNow
  elevated = $true
  monitorConnectionConfirmed = $true
  adapters = @($adapters)
  amdAdapters = @($amd)
  nvidiaAdapters = @($nvidia)
  amdDisabled = $amdDisabled
  nvidiaReady = $nvidiaReady
  fileBackedDisks = @($disks)
  relatedProcesses = @($processes)
}
$evidence | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'hardware-gate.json') -Encoding utf8
Compress-Archive -Path (Join-Path $artifactRoot '*') -DestinationPath $zipPath
$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
Write-Output "NVIDIA_ONLY_STATUS=$($evidence.status)"
Write-Output "NVIDIA_ONLY_ARTIFACT_ZIP=$zipPath"
Write-Output "NVIDIA_ONLY_ARTIFACT_SHA256=$hash"
if (-not $ready) { exit 1 }
