[CmdletBinding()]
# Build the Wails v3 desktop application for Windows.
# Usage: pwsh -File scripts/build-desktop.ps1 [-SkipTests]
#
# Wails v3 embeds the frontend through //go:embed in main.go, so the build is:
# build the frontend, generate the Windows resource object (icon + manifest),
# then `go build`. There is no `wails build` step.
param(
    [switch]$SkipTests
)

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

$wailsModule = Select-String -Path (Join-Path $projectRoot 'go.mod') -Pattern 'wailsapp/wails/v3 (v\S+)'
$wailsVersion = if ($wailsModule) { $wailsModule.Matches[0].Groups[1].Value } else { 'latest' }
$version = (Get-Content (Join-Path $projectRoot 'VERSION') -Raw).Trim().TrimStart('v')

if (-not $SkipTests) {
    & (Join-Path $PSScriptRoot 'test.ps1')
}

Write-Host '==> Building the embedded frontend'
Push-Location (Join-Path $projectRoot 'web')
try {
    & pnpm install --frozen-lockfile
    Assert-LastExitCode 'pnpm install'
    & pnpm run build
    Assert-LastExitCode 'frontend build'
}
finally {
    Pop-Location
}

$binDir = Join-Path $projectRoot 'build/bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

Push-Location $projectRoot
try {
    Write-Host '==> Generating the Windows resource object'
    & go run "github.com/wailsapp/wails/v3/cmd/wails3@$wailsVersion" generate syso `
        -arch amd64 `
        -icon build/windows/icon.ico `
        -manifest build/windows/wails.exe.manifest `
        -info build/windows/info.json `
        -out wails_windows_amd64.syso
    if ($LASTEXITCODE -ne 0) {
        Write-Warning 'wails3 generate syso failed; building without embedded icon/manifest.'
    }

    Write-Host '==> Building Playlist Forge desktop for Windows'
    $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$version" `
        -o (Join-Path $binDir 'playlist-forge.exe') .
    Assert-LastExitCode 'go build'
}
finally {
    Remove-Item -Path (Join-Path $projectRoot 'wails_windows_amd64.syso') -ErrorAction SilentlyContinue
    Pop-Location
}

Write-Host "==> Desktop artifacts are in $binDir (Wails $wailsVersion)"
Write-Host '==> NSIS installer packaging is not yet ported to Wails v3 (see PR notes).'
