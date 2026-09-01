#!/usr/bin/env bash

# Build the Wails desktop application for the current operating system.
# Usage: bash scripts/build-desktop.sh [--skip-tests]

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "$script_dir/.." && pwd)"
skip_tests=false

if [[ "${1:-}" == "--skip-tests" ]]; then
  skip_tests=true
elif [[ $# -gt 0 ]]; then
  echo "Usage: bash scripts/build-desktop.sh [--skip-tests]" >&2
  exit 2
fi

command -v go >/dev/null 2>&1 || { echo "Go is required." >&2; exit 1; }
command -v pnpm >/dev/null 2>&1 || { echo "pnpm is required." >&2; exit 1; }

if [[ "$skip_tests" == false ]]; then
  bash "$script_dir/test.sh"
fi

host_os="$(go env GOOS)"
wails=(go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0 build -clean -trimpath)
case "$host_os" in
  linux)
    wails+=(-tags webkit2_41)
    ;;
  darwin)
    wails+=(-platform darwin/universal)
    ;;
  windows)
    wails+=(-webview2 download)
    if command -v makensis >/dev/null 2>&1; then
      wails+=(-nsis)
    fi
    ;;
  *)
    echo "Unsupported desktop build host: $host_os" >&2
    exit 1
    ;;
esac

echo "==> Building Playlist Forge desktop for $host_os"
(cd "$project_root" && "${wails[@]}")
if [[ "$host_os" == "linux" ]]; then
  bash "$script_dir/package-linux.sh"
fi
echo "==> Desktop artifacts are in $project_root/build/bin"
