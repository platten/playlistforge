#!/usr/bin/env bash

# Package an existing Wails Linux binary as deb, rpm, and AppImage.
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
if [[ "$(go env GOARCH)" != "amd64" ]]; then
  echo "Linux packaging currently supports amd64 only." >&2
  exit 1
fi
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
deb="$output_dir/playlist-forge_${version}_amd64.deb"
rpm="$output_dir/playlist-forge-${version}-1.x86_64.rpm"
appimage="$output_dir/playlist-forge-${version}-x86_64.AppImage"

echo "==> Building deb and rpm packages with nFPM v2.47.0"
(
  cd "$project_root"
  export PACKAGE_VERSION="$version"
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

echo "==> Building AppImage with checksum-pinned appimagetool 1.9.1"
appimagetool="$work_dir/appimagetool-x86_64.AppImage"
curl --fail --location --retry 3 --silent --show-error \
  "https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage" \
  --output "$appimagetool"
echo "ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0  $appimagetool" | sha256sum --check --status
chmod 0755 "$appimagetool"
runtime="$work_dir/runtime-x86_64"
curl --fail --location --retry 3 --silent --show-error \
  "https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64" \
  --output "$runtime"
echo "2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d  $runtime" | sha256sum --check --status
(
  cd "$work_dir"
  "$appimagetool" --appimage-extract >/dev/null
)
ARCH=x86_64 VERSION="$version" "$work_dir/squashfs-root/AppRun" --runtime-file "$runtime" "$app_dir" "$appimage"
chmod 0755 "$appimage"

echo "==> Linux packages"
printf '  %s\n' "$deb" "$rpm" "$appimage"
