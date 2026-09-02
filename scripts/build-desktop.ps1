[CmdletBinding()]
# Build the Wails desktop application for the current operating system.
# Usage: pwsh -File scripts/build-desktop.ps1 [-SkipTests] [-SkipInstaller]
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

function Enable-Nsis {
    $command = Get-Command makensis.exe -CommandType Application -ErrorAction SilentlyContinue
    if (-not $command) {
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
            throw 'NSIS is required to build the Windows installer. Install it with: choco install nsis'
        }

        $nsisDirectory = Split-Path -Parent $executable
        $env:PATH = "$nsisDirectory$([IO.Path]::PathSeparator)$env:PATH"
        $command = Get-Command makensis.exe -CommandType Application -ErrorAction Stop
    }

    Write-Host "==> Using NSIS at $($command.Source)"
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
if (-not $SkipInstaller) {
    Enable-Nsis
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

if (-not $SkipInstaller) {
    $installers = @(Get-ChildItem -LiteralPath (Join-Path $projectRoot 'build/bin') -Filter '*-installer.exe' -File)
    if ($installers.Count -eq 0) {
        throw 'Wails completed without producing an NSIS installer.'
    }
    $installers | ForEach-Object { Write-Host "==> Windows installer: $($_.FullName)" }
}
Write-Host "==> Desktop artifacts are in $(Join-Path $projectRoot 'build/bin')"
