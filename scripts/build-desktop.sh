#!/usr/bin/env bash

# Build the Wails v3 desktop application for the current operating system.
# Usage: bash scripts/build-desktop.sh [--skip-tests]
#
# Wails v3 embeds the built frontend through a plain //go:embed directive in
# main.go, so a desktop build is: build the frontend, then `go build`. There is
# no `wails build` step and no generated bindings — web/src/api.ts talks to the
# bound Go service directly through @wailsio/runtime.

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

wails_version="$(sed -nE 's#.*wailsapp/wails/v3 (v[0-9][^ ]*).*#\1#p' "$project_root/go.mod" | head -n 1)"
version="$(tr -d '[:space:]' <"$project_root/VERSION")"
version="${version#v}"

if [[ "$skip_tests" == false ]]; then
  bash "$script_dir/test.sh"
fi

echo "==> Building the embedded frontend"
(
  cd "$project_root/web"
  pnpm install --frozen-lockfile
  pnpm run build
)

host_os="$(go env GOOS)"
bin_dir="$project_root/build/bin"
mkdir -p -- "$bin_dir"
ldflags="-s -w -X main.version=${version}"

echo "==> Building Playlist Forge desktop for $host_os"
case "$host_os" in
  linux)
    (
      cd "$project_root"
      CGO_ENABLED=1 go build -tags gtk3 -trimpath -ldflags "$ldflags" \
        -o "$bin_dir/playlist-forge" .
    )
    bash "$script_dir/package-linux.sh" --version "$version"
    ;;
  darwin)
    # Universal binary, then a minimal .app bundle around it.
    (
      cd "$project_root"
      for arch in amd64 arm64; do
        CGO_ENABLED=1 GOARCH="$arch" go build -trimpath -ldflags "$ldflags" \
          -o "$bin_dir/playlist-forge-$arch" .
      done
      lipo -create -output "$bin_dir/playlist-forge" \
        "$bin_dir/playlist-forge-amd64" "$bin_dir/playlist-forge-arm64"
      rm -f "$bin_dir/playlist-forge-amd64" "$bin_dir/playlist-forge-arm64"
    )
    app="$bin_dir/playlist-forge.app"
    rm -rf -- "$app"
    mkdir -p -- "$app/Contents/MacOS" "$app/Contents/Resources"
    install -m 0755 "$bin_dir/playlist-forge" "$app/Contents/MacOS/playlist-forge"
    sed -e "s/{{VERSION}}/$version/g" "$project_root/build/darwin/Info.plist" \
      >"$app/Contents/Info.plist"
    [[ -f "$project_root/build/darwin/icons.icns" ]] &&
      install -m 0644 "$project_root/build/darwin/icons.icns" \
        "$app/Contents/Resources/icons.icns"
    echo "==> Bundled $app"
    ;;
  windows)
    echo "Use scripts/build-desktop.ps1 on Windows." >&2
    exit 1
    ;;
  *)
    echo "Unsupported desktop build host: $host_os" >&2
    exit 1
    ;;
esac

echo "==> Desktop artifacts are in $bin_dir (Wails $wails_version)"
