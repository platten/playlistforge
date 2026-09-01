#!/usr/bin/env bash

# Usage: bash scripts/build.sh [--skip-tests] [--output DIRECTORY]
# The frontend must be built first because Go embeds internal/webui/dist.

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "$script_dir/.." && pwd)"
output_dir="$project_root/outputs"
skip_tests=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-tests)
      skip_tests=true
      shift
      ;;
    --output)
      [[ $# -ge 2 ]] || { echo "--output requires a directory." >&2; exit 2; }
      output_dir="$2"
      shift 2
      ;;
    *)
      echo "Usage: bash scripts/build.sh [--skip-tests] [--output DIRECTORY]" >&2
      exit 2
      ;;
  esac
done

if [[ "$output_dir" != /* ]]; then
  output_dir="$(pwd -P)/$output_dir"
fi

command -v go >/dev/null 2>&1 || { echo "Go is required." >&2; exit 1; }
command -v pnpm >/dev/null 2>&1 || { echo "pnpm is required." >&2; exit 1; }

if [[ "$skip_tests" == false ]]; then
  bash "$script_dir/test.sh"
else
  echo "==> Building the embedded frontend"
  (
    cd "$project_root/web"
    pnpm install --frozen-lockfile
    pnpm run build
  )
fi

if [[ ! -d "$output_dir" ]]; then
  mkdir -p -- "$output_dir"
fi

targets=(
  "windows amd64 playlist-forge-windows-amd64.exe"
  "windows arm64 playlist-forge-windows-arm64.exe"
  "linux amd64 playlist-forge-linux-amd64"
  "linux arm64 playlist-forge-linux-arm64"
  "darwin amd64 playlist-forge-darwin-amd64"
  "darwin arm64 playlist-forge-darwin-arm64"
)
artifacts=()

for target in "${targets[@]}"; do
  read -r target_os target_arch filename <<<"$target"
  echo "==> Building ${target_os}/${target_arch}"
  (
    cd "$project_root"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
      go build -trimpath -ldflags="-s -w" -o "$output_dir/$filename" ./cmd/playlistforge
  )
  artifacts+=("$filename")
done

echo "==> Writing SHA-256 checksums"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$output_dir" && sha256sum "${artifacts[@]}") >"$output_dir/SHA256SUMS.txt"
elif command -v shasum >/dev/null 2>&1; then
  (cd "$output_dir" && shasum -a 256 "${artifacts[@]}") >"$output_dir/SHA256SUMS.txt"
else
  echo "sha256sum or shasum is required to write checksums." >&2
  exit 1
fi

echo "==> Release binaries are in $output_dir"
