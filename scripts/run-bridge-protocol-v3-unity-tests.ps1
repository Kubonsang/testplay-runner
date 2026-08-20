[CmdletBinding()]
param(
    [string]$UnityPath = 'C:\Program Files\Unity\Hub\Editor\6000.3.8f1\Editor\Unity.exe',
    [string]$ArtifactRoot = (Join-Path $env:TEMP ('testplay-bridge-v3-unity-tests-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff')))
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$fixtureRoot = Join-Path $repositoryRoot 'testdata\unity-vhdx-fixture'
$projectRoot = Join-Path $ArtifactRoot 'project'
$resultsPath = Join-Path $ArtifactRoot 'results.xml'
$logPath = Join-Path $ArtifactRoot 'unity.log'

if (-not (Test-Path -LiteralPath $UnityPath -PathType Leaf)) {
    throw "Unity Editor was not found: $UnityPath"
}
if (Test-Path -LiteralPath $ArtifactRoot) {
    throw "Artifact root already exists: $ArtifactRoot"
}

New-Item -ItemType Directory -Path $projectRoot -Force | Out-Null
foreach ($name in @('Assets', 'Packages', 'ProjectSettings')) {
    Copy-Item -LiteralPath (Join-Path $fixtureRoot $name) -Destination $projectRoot -Recurse
}

$manifestPath = Join-Path $projectRoot 'Packages\manifest.json'
$manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
$packagePath = (Join-Path $repositoryRoot 'unity\com.testplay.bridge').Replace('\', '/')
$manifest.dependencies | Add-Member -NotePropertyName 'com.testplay.bridge' -NotePropertyValue ("file:$packagePath") -Force
$manifest | Add-Member -NotePropertyName 'testables' -NotePropertyValue @('com.testplay.bridge') -Force
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText(
    $manifestPath,
    ($manifest | ConvertTo-Json -Depth 20),
    $utf8NoBom)

$arguments = @(
    '-batchmode',
    '-nographics',
    '-projectPath', $projectRoot,
    '-runTests',
    '-testPlatform', 'EditMode',
    '-testFilter', 'TestPlay.Bridge.Tests',
    '-testResults', $resultsPath,
	'-logFile', $logPath
)

$startInfo = New-Object System.Diagnostics.ProcessStartInfo
$startInfo.FileName = $UnityPath
$startInfo.UseShellExecute = $false
$startInfo.Arguments = (($arguments | ForEach-Object {
    '"' + ($_ -replace '"', '\"') + '"'
}) -join ' ')
$process = New-Object System.Diagnostics.Process
$process.StartInfo = $startInfo
if (-not $process.Start()) {
    throw 'Unity process did not start.'
}
$process.WaitForExit()
$unityExitCode = [int]$process.ExitCode

$testTotal = 0
$testPassed = 0
$testFailed = 0
if (Test-Path -LiteralPath $resultsPath -PathType Leaf) {
    [xml]$results = Get-Content -Raw -LiteralPath $resultsPath
    $testTotal = [int]$results.'test-run'.total
    $testPassed = [int]$results.'test-run'.passed
    $testFailed = [int]$results.'test-run'.failed
}
$passed = $unityExitCode -eq 0 -and $testTotal -gt 0 -and $testFailed -eq 0
$summary = [ordered]@{
    schemaVersion = 1
    status = if ($passed) { 'PASS' } else { 'FAILED' }
    unityPath = $UnityPath
    exitCode = $unityExitCode
	 total = $testTotal
	 passed = $testPassed
	 failed = $testFailed
    artifactRoot = $ArtifactRoot
    resultsPath = $resultsPath
    logPath = $logPath
}
[System.IO.File]::WriteAllText(
    (Join-Path $ArtifactRoot 'summary.json'),
    ($summary | ConvertTo-Json -Depth 10),
    $utf8NoBom)
$summary | ConvertTo-Json -Compress
if ($passed) { exit 0 }
if ($unityExitCode -ne 0) { exit $unityExitCode }
exit 1
