#!/usr/bin/env bash

# Upload a release binary to VirusTotal API v3.
# Usage: VIRUSTOTAL_API_KEY=... bash scripts/upload-virustotal.sh PATH

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: VIRUSTOTAL_API_KEY=... bash scripts/upload-virustotal.sh PATH" >&2
  exit 2
fi

artifact=$1
if [[ ! -f "$artifact" ]]; then
  echo "VirusTotal artifact not found: $artifact" >&2
  exit 1
fi
if [[ -z "${VIRUSTOTAL_API_KEY:-}" ]]; then
  echo "VIRUSTOTAL_API_KEY is required. Configure it as a GitHub Actions repository secret." >&2
  exit 1
fi

command -v curl >/dev/null 2>&1 || { echo "curl is required." >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required." >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo "sha256sum is required." >&2; exit 1; }

response_file=$(mktemp)
upload_url_file=$(mktemp)
trap 'rm -f -- "$response_file" "$upload_url_file"' EXIT

upload_url=https://www.virustotal.com/api/v3/files
artifact_size=$(stat --format='%s' "$artifact")
if ((artifact_size > 32 * 1024 * 1024)); then
  echo "==> Requesting a VirusTotal large-file upload URL"
  status=$(curl --silent --show-error \
    --output "$upload_url_file" \
    --write-out '%{http_code}' \
    --header "x-apikey: $VIRUSTOTAL_API_KEY" \
    https://www.virustotal.com/api/v3/files/upload_url)
  if [[ "$status" -lt 200 || "$status" -ge 300 ]]; then
    echo "VirusTotal upload URL request failed with HTTP $status." >&2
    jq -r '.error.message? // empty' "$upload_url_file" >&2 || true
    exit 1
  fi
  upload_url=$(jq -er '.data' "$upload_url_file")
fi

echo "==> Uploading $(basename "$artifact") to VirusTotal"
status=$(curl --silent --show-error \
  --output "$response_file" \
  --write-out '%{http_code}' \
  --request POST \
  --header "x-apikey: $VIRUSTOTAL_API_KEY" \
  --form "file=@$artifact" \
  "$upload_url")
if [[ "$status" -lt 200 || "$status" -ge 300 ]]; then
  echo "VirusTotal upload failed with HTTP $status." >&2
  jq -r '.error.message? // empty' "$response_file" >&2 || true
  exit 1
fi

analysis_id=$(jq -er '.data.id' "$response_file")
artifact_sha256=$(sha256sum "$artifact" | awk '{print $1}')
report_url="https://www.virustotal.com/gui/file/$artifact_sha256"

echo "==> VirusTotal accepted the upload (analysis ID: $analysis_id)"
echo "==> VirusTotal report: $report_url"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "### VirusTotal submission"
    echo
    echo "- Artifact: \`$(basename "$artifact")\`"
    echo "- SHA-256: \`$artifact_sha256\`"
    echo "- [VirusTotal report]($report_url)"
  } >>"$GITHUB_STEP_SUMMARY"
fi
