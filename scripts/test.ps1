[CmdletBinding()]
# Usage: pwsh -File scripts/test.ps1 [-SkipRace]
# Keep this quality gate behavior equivalent to test.sh.
param(
    [switch]$SkipRace
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

Write-Host '==> Installing and verifying the frontend'
Push-Location (Join-Path $projectRoot 'web')
try {
    & pnpm install --frozen-lockfile
    Assert-LastExitCode 'pnpm install'
    & pnpm run format:check
    Assert-LastExitCode 'frontend formatting check'
    & pnpm run lint
    Assert-LastExitCode 'frontend lint'
    & pnpm run typecheck
    Assert-LastExitCode 'frontend typecheck'
    & pnpm test
    Assert-LastExitCode 'frontend tests'
    & pnpm run build
    Assert-LastExitCode 'frontend build'
}
finally {
    Pop-Location
}

Write-Host '==> Checking Go formatting'
Push-Location $projectRoot
try {
    $unformatted = @(& gofmt -l cmd internal)
    Assert-LastExitCode 'gofmt check'
    if ($unformatted.Count -gt 0) {
        throw "The following Go files require gofmt:`n$($unformatted -join "`n")"
    }

    Write-Host '==> Running Go Vet and unit tests'
    & go vet ./...
    Assert-LastExitCode 'go vet'
    & go test -count=1 ./...
    Assert-LastExitCode 'Go unit tests'

    $hostGoOS = (& go env GOOS).Trim()
    Assert-LastExitCode 'go env GOOS'
    $cgoEnabled = (& go env CGO_ENABLED).Trim()
    Assert-LastExitCode 'go env CGO_ENABLED'
    if (-not $SkipRace -and $cgoEnabled -eq '1' -and $hostGoOS -in @('linux', 'darwin')) {
        Write-Host '==> Running the Go race detector'
        & go test -race -count=1 ./...
        Assert-LastExitCode 'Go race tests'
    }
    else {
        Write-Host '==> Race detector skipped (requires CGO on Linux or macOS, or -SkipRace was supplied)'
    }

    $coverageRoot = Join-Path ([System.IO.Path]::GetTempPath()) "playlist-forge-coverage-$([guid]::NewGuid())"
    New-Item -ItemType Directory -Path $coverageRoot | Out-Null
    try {
        Write-Host '==> Enforcing 95% business/API coverage'
        $coveragePackages = @('app', 'playlist', 'openaiapi', 'soundiiz')
        $coverageInputs = [System.Collections.Generic.List[string]]::new()
        foreach ($packageName in $coveragePackages) {
            $packageOutput = Join-Path $coverageRoot $packageName
            New-Item -ItemType Directory -Path $packageOutput | Out-Null
            & go test '-coverpkg=playlistforge/internal/app,playlistforge/internal/playlist,playlistforge/internal/openaiapi,playlistforge/internal/soundiiz' "./internal/$packageName" -args "-test.gocoverdir=$packageOutput"
            Assert-LastExitCode "Go coverage tests for $packageName"
            [void]$coverageInputs.Add($packageOutput)
        }

        $mergedOutput = Join-Path $coverageRoot 'merged'
        New-Item -ItemType Directory -Path $mergedOutput | Out-Null
        & go tool covdata merge "-i=$($coverageInputs -join ',')" "-o=$mergedOutput"
        Assert-LastExitCode 'merge Go coverage data'
        $coverageProfile = Join-Path $coverageRoot 'coverage.out'
        & go tool covdata textfmt "-i=$mergedOutput" "-o=$coverageProfile"
        Assert-LastExitCode 'format Go coverage data'
        $coverageOutput = & go tool cover "-func=$coverageProfile"
        Assert-LastExitCode 'Go coverage report'
        $totalLine = $coverageOutput | Where-Object { $_ -match '^total:' } | Select-Object -Last 1
        if (-not $totalLine -or $totalLine -notmatch '([0-9]+(?:\.[0-9]+)?)%') {
            throw 'Could not read total Go coverage.'
        }
        $coverage = [double]::Parse($Matches[1], [Globalization.CultureInfo]::InvariantCulture)
        Write-Host "Core coverage: $coverage%"
        if ($coverage -lt 95) {
            throw "Core coverage $coverage% is below the required 95%."
        }
    }
    finally {
        $temporaryRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
        $resolvedCoverageRoot = [System.IO.Path]::GetFullPath($coverageRoot)
        if ($resolvedCoverageRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -and
            (Split-Path -Leaf $resolvedCoverageRoot).StartsWith('playlist-forge-coverage-', [StringComparison]::Ordinal)) {
            Remove-Item -LiteralPath $resolvedCoverageRoot -Recurse -Force
        }
    }
}
finally {
    Pop-Location
}

Write-Host '==> All checks passed'
