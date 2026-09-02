[CmdletBinding()]
# Build the Wails v3 desktop application for Windows.
# Usage: pwsh -File scripts/build-desktop.ps1 [-SkipTests] [-SkipInstaller]
#
# Wails v3 embeds the frontend through //go:embed in main.go, so the build is:
# build the frontend, generate the Windows resource object (icon + manifest),
# then `go build`. There is no `wails build` step.
param(
    [switch]$SkipTests,
    [switch]$SkipInstaller
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot

function Assert-LastExitCode {
    param([Parameter(Mandatory)][string]$Operation)
    if ($LASTEXITCODE -ne 0) {
        throw "$Operation failed with exit code $LASTEXITCODE."
    }
}

function Find-Nsis {
    $command = Get-Command makensis.exe -CommandType Application -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    $candidates = @()
    if (${env:ProgramFiles(x86)}) {
        $candidates += Join-Path ${env:ProgramFiles(x86)} 'NSIS\makensis.exe'
    }
    if ($env:ProgramFiles) {
        $candidates += Join-Path $env:ProgramFiles 'NSIS\makensis.exe'
    }
    if ($env:ChocolateyToolsLocation) {
        $candidates += Join-Path $env:ChocolateyToolsLocation 'nsis\makensis.exe'
    }
    $executable = $candidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    if (-not $executable) {
        throw 'NSIS is required to build the Windows installers. Install it with: choco install nsis'
    }
    return $executable
}

Get-Command go -ErrorAction Stop | Out-Null
Get-Command pnpm -ErrorAction Stop | Out-Null

$wailsModule = Select-String -Path (Join-Path $projectRoot 'go.mod') -Pattern 'wailsapp/wails/v3 (v\S+)'
$wailsVersion = if ($wailsModule) { $wailsModule.Matches[0].Groups[1].Value } else { 'latest' }
$version = (Get-Content (Join-Path $projectRoot 'VERSION') -Raw).Trim().TrimStart('v')
if ($version -notmatch '^\d+\.\d+\.\d+$') {
    throw "Windows installer versions must use MAJOR.MINOR.PATCH: $version"
}

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

# WebView2 on Windows uses go-webview2, which is pure Go, so both architectures
# cross-compile from any host with CGO disabled.
$architectures = @('amd64', 'arm64')
$nsis = $null
$webViewBootstrapper = $null
if (-not $SkipInstaller) {
    $nsis = Find-Nsis
    Write-Host "==> Using NSIS at $nsis"
    $webViewBootstrapper = Join-Path ([IO.Path]::GetTempPath()) "playlist-forge-webview2-$PID.exe"
    Write-Host '==> Downloading the Microsoft Edge WebView2 evergreen bootstrapper'
    Invoke-WebRequest -Uri 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $webViewBootstrapper
}

Push-Location $projectRoot
try {
    $env:CGO_ENABLED = '0'
    foreach ($arch in $architectures) {
        $syso = "wails_windows_$arch.syso"
        Write-Host "==> Generating the Windows resource object ($arch)"
        & go run "github.com/wailsapp/wails/v3/cmd/wails3@$wailsVersion" generate syso `
            -arch $arch `
            -icon build/windows/icon.ico `
            -manifest build/windows/wails.exe.manifest `
            -info build/windows/info.json `
            -out $syso
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "wails3 generate syso failed for $arch; building without embedded icon/manifest."
        }

        Write-Host "==> Building Playlist Forge desktop for windows/$arch"
        $env:GOARCH = $arch
        & go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$version" `
            -o (Join-Path $binDir "playlist-forge-$arch.exe") .
        Assert-LastExitCode "go build (windows/$arch)"

        Remove-Item -Path (Join-Path $projectRoot $syso) -ErrorAction SilentlyContinue

        if (-not $SkipInstaller) {
            $binary = Join-Path $binDir "playlist-forge-$arch.exe"
            $installer = Join-Path $binDir "playlist-forge-$version-windows-$arch-setup.exe"
            Write-Host "==> Building NSIS installer for windows/$arch"
            $nsisArguments = @(
                '-WX',
                "-DAPP_VERSION=$version",
                "-DAPP_ARCH=$arch",
                "-DAPP_BINARY=$binary",
                "-DAPP_ICON=$(Join-Path $projectRoot 'build/windows/icon.ico')",
                "-DWEBVIEW_BOOTSTRAPPER=$webViewBootstrapper",
                "-DOUTPUT_FILE=$installer",
                (Join-Path $projectRoot 'build/windows/installer.nsi')
            )
            & $nsis @nsisArguments
            Assert-LastExitCode "NSIS installer (windows/$arch)"
            if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
                throw "NSIS completed without producing $installer"
            }
            Write-Host "==> Windows installer: $installer"
        }
    }
}
finally {
    Remove-Item -Path (Join-Path $projectRoot 'wails_windows_amd64.syso') -ErrorAction SilentlyContinue
    Remove-Item -Path (Join-Path $projectRoot 'wails_windows_arm64.syso') -ErrorAction SilentlyContinue
    if ($webViewBootstrapper) {
        Remove-Item -LiteralPath $webViewBootstrapper -ErrorAction SilentlyContinue
    }
    $env:GOARCH = $null
    Pop-Location
}

Write-Host "==> Desktop artifacts are in $binDir (Wails $wailsVersion)"
