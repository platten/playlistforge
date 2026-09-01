[CmdletBinding()]
# Build the Wails desktop application for the current operating system.
# Usage: pwsh -File scripts/build-desktop.ps1 [-SkipTests]
param([switch]$SkipTests)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot

function Assert-LastExitCode {
    param([Parameter(Mandatory)][string]$Operation)
    if ($LASTEXITCODE -ne 0) {
        throw "$Operation failed with exit code $LASTEXITCODE."
    }
}

Get-Command go -ErrorAction Stop | Out-Null
Get-Command pnpm -ErrorAction Stop | Out-Null

if (-not $SkipTests) {
    & (Join-Path $PSScriptRoot 'test.ps1')
}

$arguments = @(
    'run',
    'github.com/wailsapp/wails/v2/cmd/wails@v2.15.0',
    'build',
    '-clean',
    '-trimpath',
    '-webview2',
    'download'
)
if (Get-Command makensis -ErrorAction SilentlyContinue) {
    $arguments += '-nsis'
}

Write-Host '==> Building Playlist Forge desktop for Windows'
Push-Location $projectRoot
try {
    & go @arguments
    Assert-LastExitCode 'Wails desktop build'
}
finally {
    Pop-Location
}
Write-Host "==> Desktop artifacts are in $(Join-Path $projectRoot 'build/bin')"
