[CmdletBinding()]
# Usage: pwsh -File scripts/build.ps1 [-OutputDirectory PATH] [-SkipTests]
# The frontend must be built first because Go embeds internal/webui/dist.
param(
    [string]$OutputDirectory,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $projectRoot 'outputs'
}
$OutputDirectory = [System.IO.Path]::GetFullPath($OutputDirectory)

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
else {
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
}

New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$targets = @(
    @{ OS = 'windows'; Arch = 'amd64'; File = 'playlist-forge-windows-amd64.exe' },
    @{ OS = 'windows'; Arch = 'arm64'; File = 'playlist-forge-windows-arm64.exe' },
    @{ OS = 'linux'; Arch = 'amd64'; File = 'playlist-forge-linux-amd64' },
    @{ OS = 'linux'; Arch = 'arm64'; File = 'playlist-forge-linux-arm64' },
    @{ OS = 'darwin'; Arch = 'amd64'; File = 'playlist-forge-darwin-amd64' },
    @{ OS = 'darwin'; Arch = 'arm64'; File = 'playlist-forge-darwin-arm64' }
)

$previousGoOS = $env:GOOS
$previousGoArch = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    Push-Location $projectRoot
    try {
        foreach ($target in $targets) {
            $env:GOOS = $target.OS
            $env:GOARCH = $target.Arch
            $destination = Join-Path $OutputDirectory $target.File
            Write-Host "==> Building $($target.OS)/$($target.Arch)"
            & go build -trimpath '-ldflags=-s -w' -o $destination ./cmd/playlistforge
            Assert-LastExitCode "build $($target.OS)/$($target.Arch)"
        }
    }
    finally {
        Pop-Location
    }
}
finally {
    $env:GOOS = $previousGoOS
    $env:GOARCH = $previousGoArch
    $env:CGO_ENABLED = $previousCGO
}

Write-Host '==> Writing SHA-256 checksums'
$checksumLines = foreach ($target in $targets) {
    $path = Join-Path $OutputDirectory $target.File
    $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
    "$hash  $($target.File)"
}
$checksumPath = Join-Path $OutputDirectory 'SHA256SUMS.txt'
[System.IO.File]::WriteAllLines($checksumPath, $checksumLines, [System.Text.UTF8Encoding]::new($false))

Write-Host "==> Release binaries are in $OutputDirectory"
