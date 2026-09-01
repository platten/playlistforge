#!/usr/bin/env bash

# Usage: bash scripts/test.sh [--skip-race]
# This is the canonical CI quality gate; keep test.ps1 behavior equivalent.

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "$script_dir/.." && pwd)"
skip_race=false

if [[ "${1:-}" == "--skip-race" ]]; then
  skip_race=true
elif [[ $# -gt 0 ]]; then
  echo "Usage: bash scripts/test.sh [--skip-race]" >&2
  exit 2
fi

command -v go >/dev/null 2>&1 || { echo "Go is required." >&2; exit 1; }
command -v pnpm >/dev/null 2>&1 || { echo "pnpm is required." >&2; exit 1; }

echo "==> Installing and verifying the frontend"
(
  cd "$project_root/web"
  pnpm install --frozen-lockfile
  pnpm run format:check
  pnpm run lint
  pnpm run typecheck
  pnpm test
  pnpm run build
)

echo "==> Checking Go formatting"
unformatted="$(cd "$project_root" && gofmt -l main.go cmd internal)"
if [[ -n "$unformatted" ]]; then
  echo "The following Go files require gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> Running Go Vet and unit tests"
(
  cd "$project_root"
  go vet ./...
  go test -count=1 ./...
)

host_goos="$(go env GOOS)"
cgo_enabled="$(go env CGO_ENABLED)"
if [[ "$skip_race" == false && "$cgo_enabled" == "1" && ( "$host_goos" == "linux" || "$host_goos" == "darwin" ) ]]; then
  echo "==> Running the Go race detector"
  (cd "$project_root" && go test -race -count=1 ./...)
else
  echo "==> Race detector skipped (requires CGO on Linux or macOS, or --skip-race was supplied)"
fi

coverage_root="$(mktemp -d)"
trap 'rm -rf -- "$coverage_root"' EXIT

# Go tools launched from Git Bash need native Windows paths inside -key=value
# arguments because MSYS cannot rewrite path lists embedded after an equals sign.
go_path() {
  if [[ "$host_goos" == "windows" ]] && command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}

echo "==> Enforcing 95% business/API coverage"
coverage_packages=(app playlist openaiapi soundiiz)
coverage_inputs=()
for package_name in "${coverage_packages[@]}"; do
  package_output="$coverage_root/$package_name"
  mkdir -- "$package_output"
  package_output_go="$(go_path "$package_output")"
  coverage_inputs+=("$package_output_go")
  (
    cd "$project_root"
    go test \
      -coverpkg=playlistforge/internal/app,playlistforge/internal/playlist,playlistforge/internal/openaiapi,playlistforge/internal/soundiiz \
      "./internal/$package_name" \
      -args "-test.gocoverdir=$package_output_go"
  )
done

merged_output="$coverage_root/merged"
mkdir -- "$merged_output"
merged_output_go="$(go_path "$merged_output")"
coverage_input_list="$(IFS=,; echo "${coverage_inputs[*]}")"
coverage_profile="$coverage_root/coverage.out"
coverage_profile_go="$(go_path "$coverage_profile")"
(
  cd "$project_root"
  go tool covdata merge "-i=$coverage_input_list" "-o=$merged_output_go"
  go tool covdata textfmt "-i=$merged_output_go" "-o=$coverage_profile_go"
)
coverage="$(cd "$project_root" && go tool cover -func="$coverage_profile_go" | awk '/^total:/ {gsub("%", "", $3); print $3}')"
echo "Core coverage: ${coverage}%"
awk -v coverage="$coverage" 'BEGIN { if (coverage + 0 < 95) exit 1 }'

echo "==> All checks passed"
