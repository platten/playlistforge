#!/usr/bin/env bash

# Package an existing Wails Linux binary as deb, rpm, and AppImage for the
# current Go target architecture (amd64 or arm64).
# Usage: bash scripts/package-linux.sh [--version VERSION]

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "$script_dir/.." && pwd)"
version=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || { echo "--version requires a value." >&2; exit 2; }
      version="$2"
      shift 2
      ;;
    *)
      echo "Usage: bash scripts/package-linux.sh [--version VERSION]" >&2
      exit 2
      ;;
  esac
done

if [[ "$(go env GOOS)" != "linux" ]]; then
  echo "Linux packages must be built on Linux." >&2
  exit 1
fi

goarch="$(go env GOARCH)"
case "$goarch" in
  amd64)
    deb_arch="amd64"; rpm_arch="x86_64"; appimage_arch="x86_64"
    appimagetool_sha="ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0"
    runtime_sha="2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d"
    ;;
  arm64)
    deb_arch="arm64"; rpm_arch="aarch64"; appimage_arch="aarch64"
    appimagetool_sha="f0837e7448a0c1e4e650a93bb3e85802546e60654ef287576f46c71c126a9158"
    runtime_sha="00cbdfcf917cc6c0ff6d3347d59e0ca1f7f45a6df1a428a0d6d8a78664d87444"
    ;;
  *)
    echo "Linux packaging supports amd64 and arm64 only (got $goarch)." >&2
    exit 1
    ;;
esac

if [[ -z "$version" ]]; then
  version="$(tr -d '[:space:]' <"$project_root/VERSION")"
fi
version="${version#v}"
if [[ ! "$version" =~ ^[0-9]+([.][0-9A-Za-z]+)*([+~-][0-9A-Za-z.-]+)?$ ]]; then
  echo "Invalid package version: $version" >&2
  exit 1
fi

binary="$project_root/build/bin/playlist-forge"
[[ -x "$binary" ]] || { echo "Build the Linux Wails executable before packaging." >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required." >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo "sha256sum is required." >&2; exit 1; }

output_dir="$project_root/build/bin"
deb="$output_dir/playlist-forge_${version}_${deb_arch}.deb"
rpm="$output_dir/playlist-forge-${version}-1.${rpm_arch}.rpm"
appimage="$output_dir/playlist-forge-${version}-${appimage_arch}.AppImage"

echo "==> Building deb and rpm packages with nFPM v2.47.0 ($deb_arch)"
(
  cd "$project_root"
  export PACKAGE_VERSION="$version"
  export PACKAGE_ARCH="$goarch"
  go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0 package --config build/linux/nfpm.yaml --packager deb --target "$deb"
  go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0 package --config build/linux/nfpm.yaml --packager rpm --target "$rpm"
)

work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT
app_dir="$work_dir/PlaylistForge.AppDir"
mkdir -p -- \
  "$app_dir/usr/bin" \
  "$app_dir/usr/share/applications" \
  "$app_dir/usr/share/metainfo" \
  "$app_dir/usr/share/pixmaps"
install -m 0755 "$binary" "$app_dir/usr/bin/playlist-forge"
install -m 0644 "$project_root/build/linux/com.playlistforge.app.desktop" "$app_dir/com.playlistforge.app.desktop"
install -m 0644 "$project_root/build/linux/com.playlistforge.app.desktop" "$app_dir/usr/share/applications/com.playlistforge.app.desktop"
install -m 0644 "$project_root/build/linux/com.playlistforge.app.metainfo.xml" "$app_dir/usr/share/metainfo/com.playlistforge.app.appdata.xml"
install -m 0644 "$project_root/build/appicon.png" "$app_dir/playlist-forge.png"
install -m 0644 "$project_root/build/appicon.png" "$app_dir/usr/share/pixmaps/playlist-forge.png"
install -m 0755 "$project_root/build/linux/AppRun" "$app_dir/AppRun"

echo "==> Building AppImage with checksum-pinned appimagetool 1.9.1 ($appimage_arch)"
appimagetool="$work_dir/appimagetool-${appimage_arch}.AppImage"
curl --fail --location --retry 3 --silent --show-error \
  "https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-${appimage_arch}.AppImage" \
  --output "$appimagetool"
echo "${appimagetool_sha}  $appimagetool" | sha256sum --check --status
chmod 0755 "$appimagetool"
runtime="$work_dir/runtime-${appimage_arch}"
curl --fail --location --retry 3 --silent --show-error \
  "https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-${appimage_arch}" \
  --output "$runtime"
echo "${runtime_sha}  $runtime" | sha256sum --check --status
(
  cd "$work_dir"
  "$appimagetool" --appimage-extract >/dev/null
)
ARCH="$appimage_arch" VERSION="$version" "$work_dir/squashfs-root/AppRun" --runtime-file "$runtime" "$app_dir" "$appimage"
chmod 0755 "$appimage"

echo "==> Linux packages"
printf '  %s\n' "$deb" "$rpm" "$appimage"
