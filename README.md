# Playlist Forge

Playlist Forge is a local, single-user desktop and web application that turns a natural-language brief into a researched playlist. It uses OpenAI's Responses API with `gpt-5.6-sol`, lets you review and revise every track, and creates a temporary Soundiiz handoff link for TIDAL, Qobuz, Spotify, Apple Music, or another service Soundiiz supports.

Created by Paul Pietkiewicz (`773636+platten@users.noreply.github.com`) and distributed under the MIT License.

The React/TypeScript frontend is embedded into the Go binary. The Wails desktop edition uses the operating system's native webview and calls Go directly; the browser edition retains its loopback HTTP adapter. Neither edition needs Node.js or a database service at runtime.

See `ARCHITECTURE.md` for package responsibilities, security invariants, extension guidance, and the files that must change together when an API or persisted model evolves.

## Run the desktop application

Download the desktop artifact for your platform and launch **Playlist Forge**. The desktop edition opens its own window and does not listen on a local TCP port.

- Windows uses WebView2 and is distributed as an NSIS installer.
- macOS is built as a universal application for Intel and Apple Silicon.
- Linux builds produce `.deb`, `.rpm`, and AppImage packages. They use GTK3 and WebKitGTK 4.1; install your distribution's WebKitGTK runtime package if it is not already present. The deb and rpm packages declare these dependencies for their package managers.

For desktop development, install Wails' platform prerequisites, then run:

```sh
go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0 dev
```

Build a production desktop artifact for the current operating system with:

```sh
bash scripts/build-desktop.sh
# Windows PowerShell:
pwsh -File scripts/build-desktop.ps1
```

Artifacts are written under `build/bin`. Windows builds require NSIS and produce `playlist-forge-amd64-installer.exe` alongside the application executable; pass `-SkipInstaller` only when an unpackaged executable is intentional. Linux builds include `playlist-forge_VERSION_amd64.deb`, `playlist-forge-VERSION-1.x86_64.rpm`, and `playlist-forge-VERSION-x86_64.AppImage`. The shared icon master is `build/appicon.png`; Wails and the Linux packaging stage derive their native resources from it.

Tagged releases publish the Windows installer directly as `playlist-forge-VERSION-windows-amd64-setup.exe`. This version-specific GitHub Release URL is suitable for a WinGet `InstallerUrl` with `InstallerType: nullsoft`; NSIS supplies unattended install and uninstall through its standard `/S` switch. Use the matching entry in `SHA256SUMS.txt` as the WinGet `InstallerSha256`.

CI currently produces unsigned desktop artifacts. Configure Windows code-signing and an Apple Developer ID/notarization identity before treating downloads as a warning-free public release.

## Run the browser application

Download the executable for your operating system and architecture, then run it:

```text
playlist-forge
```

The browser edition listens on `http://127.0.0.1:8787` by default and opens that page in your default browser. Keep this loopback default for ordinary local use.

Useful options:

```text
playlist-forge --port 9000
playlist-forge --open-browser=false
playlist-forge --log-level debug
playlist-forge --log-format json
playlist-forge --config-dir /custom/private/path
```

Equivalent environment variables are `PLAYLIST_FORGE_PORT`, `PLAYLIST_FORGE_OPEN_BROWSER`, `PLAYLIST_FORGE_CONFIG_DIR`, `PLAYLIST_FORGE_LOG_LEVEL`, and `PLAYLIST_FORGE_LOG_FORMAT`. `PLAYLIST_FORGE_HOST=0.0.0.0` exists for the container boundary only; publishing that port beyond host loopback is unsupported because this single-user application has no login screen.

Open **Settings**, enter an OpenAI API key, and save it. The “How do I get an API key?” popup provides the same setup instructions as the next section. Playlist Forge validates access to `gpt-5.6-sol` before saving.

## Getting an OpenAI API key

1. [Create an OpenAI Platform account or sign in](https://platform.openai.com/signup).
2. Open the organization's [Billing settings](https://platform.openai.com/settings/organization/billing/overview) and add a payment method or credits when prompted. OpenAI API calls are charged according to usage.
3. Open the [API keys page](https://platform.openai.com/api-keys), choose **Create new secret key**, give it a recognizable name such as `Playlist Forge`, and copy the generated value.
4. Return to Playlist Forge, open **Settings**, paste the value into **API key**, and choose **Save key**. The application validates that the key can access `gpt-5.6-sol` before storing it.

Treat an API key like a password. Do not share it, commit it to source control, include it in screenshots, or paste it into an untrusted page. You can revoke a key from the API keys page at any time. OpenAI's [API authentication documentation](https://developers.openai.com/api/reference/overview#authentication) also recommends keeping keys secret and using a credential-management service.

`OPENAI_API_KEY`, when set, has highest precedence and is reported as a read-only environment-managed credential in Settings. Otherwise, Playlist Forge first uses the operating system's credential store:

- Windows Credential Manager
- macOS Keychain
- Linux Secret Service

If that store is unavailable, the UI can explicitly opt into a restricted `config.json` fallback under the OS-standard user config directory. The app creates a separate `playlists.db` SQLite database in that application directory. API keys are never stored in the playlist database.

## Run with Docker

The multi-stage `Dockerfile` builds the Vite frontend and Go executable, then copies only the executable and CA certificates into a non-root distroless runtime. Build it locally with:

```sh
docker build -t playlist-forge:local .
```

Run it with an environment-provided key and a persistent named volume:

```sh
docker run --rm --name playlist-forge \
  -p 127.0.0.1:8787:8787 \
  -e OPENAI_API_KEY="$OPENAI_API_KEY" \
  -v playlist-forge-data:/config \
  playlist-forge:local
```

PowerShell uses `$env:OPENAI_API_KEY` in place of `$OPENAI_API_KEY`. Open `http://127.0.0.1:8787` after startup. The host side of the port mapping must remain `127.0.0.1`; do not use `-p 8787:8787`, which exposes the unauthenticated app on every host interface. Pass the key only at runtime—never use it as a Docker build argument, copy it into the image, or commit it to `.env`.

Docker Compose provides the same hardened defaults:

```sh
cp .env.example .env
# Edit .env and replace the placeholder, then:
docker compose up --build
```

The Compose service drops Linux capabilities, prevents privilege escalation, uses a read-only root filesystem, and persists `/config` in the `playlist-forge-data` volume. The image sets `PLAYLIST_FORGE_CONFIG_DIR=/config`, so `/config/config.json` and `/config/playlists.db` are the container defaults.

To use the config-file credential instead of `OPENAI_API_KEY`, omit that environment variable. Start the container with the `/config` volume, open Settings, enter the key, enable the restricted config-file fallback, and save it. A pre-created file may instead be mounted at `/config/config.json`; its JSON shape is shown in `deploy/config.example.json`. Ensure the container's non-root user (UID 65532) can read the file and write its containing directory. Credential precedence is `OPENAI_API_KEY`, then the OS keyring where available, then `config.json`.

Published images use `ghcr.io/<owner>/<repository>:<tag>`. CI publishes default-branch and commit tags, while a `v*` release tag publishes Linux `amd64` and `arm64` images with semantic-version and `latest` tags.

## Playlist workflow

1. Describe the playlist, choose 20, 30, 40, 50, 60, or 100 tracks, and select the reasoning effort. Medium is the default.
2. Optionally select up to ten previous playlists as inspiration.
3. Review the generated title, description, ordering, recording/version notes, and per-track rationale.
4. Remove tracks, ask for individual replacements, or refine the entire playlist with another prompt. Each operation creates an immutable local revision.
5. Create a generic Soundiiz handoff link from the playlist preview.
6. Finish matching, choose the destination service, and transfer on Soundiiz. The public import link expires after roughly 24 hours.

Soundiiz receives only the accepted playlist title, description, track titles, and artist names. Playlist Forge does not receive Spotify, TIDAL, Qobuz, or Apple Music credentials.

## Complete the loop with Soundiiz

Playlist Forge prepares a temporary public import page; Soundiiz performs the authorization and writes the accepted playlist to the streaming service. Set up the destination once before your first transfer:

1. Create an account with your chosen streaming service in its official app or website, or use an existing account. Sign in to the exact profile where the playlist should be created. Some services require a paid subscription for third-party playlist writes.
2. [Create a Soundiiz account](https://soundiiz.com/register), or sign in if you already have one. Keep using the same Soundiiz sign-in method—email, Google, Apple, Spotify, or another provider—so that you do not accidentally create a separate Soundiiz account.
3. In the Soundiiz web app, open the left panel, select the streaming service (or **Connect more services**), choose **Connect**, and complete that service's authorization screen. The service icon turns green when the connection succeeds. You can later connect, disconnect, or switch services under **Settings > Platforms**. See Soundiiz's [official connection guide](https://support.soundiiz.com/hc/en-us/articles/360024694393-How-to-connect-your-music-accounts-to-Soundiiz).
4. Check the service-specific prerequisite:

   | Service | Before authorizing Soundiiz |
   | --- | --- |
   | TIDAL | Use the same TIDAL sign-in method that owns the intended library. Google, Apple, Facebook, and email/password sign-ins can resolve to different TIDAL profiles even when they share an email address. |
   | Qobuz | Sign in to the intended Qobuz account in the same browser first. To switch accounts, disconnect Qobuz under Soundiiz **Settings > Platforms**, sign out of Qobuz, sign in to the other account, and reconnect. |
   | Spotify | Approve Soundiiz's requested playlist read/write permissions. If Spotify later expires the authorization, reconnect it under **Settings > Platforms** and retry the transfer. |
   | Apple Music | Use the Apple Account that has an active Apple Music subscription, turn on **Sync Library**, and allow pop-ups and cookies during authorization. See Soundiiz's [Apple Music connection checklist](https://support.soundiiz.com/hc/en-us/articles/360009609854-I-can-t-connect-my-Apple-Music-account). |

To complete each transfer:

1. In Playlist Forge, accept the playlist and choose **Open Soundiiz handoff**.
2. Sign in to Soundiiz if asked. Review the imported title and tracks, inspect Soundiiz's catalog matches, and correct or skip any mismatch before confirming the transfer.
3. Select the connected destination account and start the transfer. Wait for Soundiiz to report completion, then open the destination service and verify the playlist and track versions.
4. To send the same playlist to another service, connect and select that service in Soundiiz. Create a fresh handoff from Playlist Forge only if the current link has expired.

The free Soundiiz plan currently supports one playlist transfer at a time with up to 200 selected tracks, so it covers Playlist Forge's 100-track maximum; see [Soundiiz plans](https://soundiiz.com/pricing) for current limits. Catalogs differ by service and region, so a track can be unavailable or resolve to another edition. A Playlist Forge handoff expires after roughly 24 hours; create a new one from the saved playlist if needed. Streaming credentials stay with Soundiiz and the streaming service and are never sent to Playlist Forge.

## Cost estimate

The preview shows an estimate for each OpenAI request from reported input, cached-input, output, reasoning, and web-search usage. The embedded rate card is versioned `2026-09-01`: GPT-5.6 Sol standard short-context rates are $4/M input tokens, $0.40/M cached input tokens, and $20/M output tokens; web search is $0.01 per call. Search-result content tokens are included in the API's input-token count.

This is an estimate, not an invoice. OpenAI can change prices, long-context or regional processing may use different rates, and account credits or discounts are not represented. The OpenAI dashboard remains authoritative.

## Build from source

Requirements:

- Go 1.27 or newer
- Node.js 24
- pnpm 11.19

Build the frontend before the Go executable because `go:embed` packages the generated assets:

```text
cd web
pnpm install --frozen-lockfile
pnpm run build
cd ..
go build -trimpath -ldflags="-s -w" -o playlist-forge ./cmd/playlistforge
```

The browser executable remains pure Go and supports ordinary cross-compilation (`CGO_ENABLED=0`). For example:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o playlist-forge-linux-arm64 ./cmd/playlistforge
```

GitHub Actions builds browser binaries for Windows, Linux, and macOS on `amd64` and `arm64`, validates the multi-platform container, and builds native Wails desktop artifacts on Windows, Linux, and macOS runners.

## Development and verification

The repository provides equivalent Bash and PowerShell entry points. Both install the locked frontend dependencies, run frontend and Go quality gates, enforce 95% core coverage, and rebuild the embedded frontend:

```text
bash scripts/test.sh
pwsh -File scripts/test.ps1
```

The race detector runs by default when CGO is available on Linux or macOS. Use `--skip-race` or `-SkipRace` only when intentionally testing on an unsupported host.

Build all six release binaries and `SHA256SUMS.txt` after running the tests:

```text
bash scripts/build.sh
pwsh -File scripts/build.ps1
```

Use `--skip-tests` or `-SkipTests` when the test script already completed. Use `--output DIRECTORY` or `-OutputDirectory DIRECTORY` to select another artifact directory.

The individual commands run by the scripts are documented below for troubleshooting.

Run backend checks:

```text
gofmt -w cmd internal
go vet ./...
go test -race ./...
```

Run frontend checks:

```text
cd web
pnpm run format:check
pnpm run lint
pnpm run typecheck
pnpm test
pnpm run build
```

CI enforces at least 95% statement coverage across the business and external-API boundary packages (`app`, `playlist`, `openaiapi`, and `soundiiz`). Frontend API, job-polling, and slow-operation state are held to 95% for statements, branches, functions, and lines; page-level tests additionally cover rendering and XSS-safe treatment of user text. Persistence, HTTP routing, origin/host protections, credential fallback, cancellation, logging, and responsive rendering have dedicated tests.

Pushing a tag such as `v1.2.3` runs `.github/workflows/release.yml`. After the quality gate, it builds all six binaries, publishes the multi-platform image to GitHub Container Registry, adds provenance and an OCI SBOM attestation, generates a downloadable SPDX JSON SBOM from the published image, refreshes SHA-256 checksums, and creates or updates the GitHub Release with the binaries, checksums, and SBOM.

## Security notes

- The fixed OpenAI base URL is `https://api.openai.com/v1`; environment variables cannot redirect it.
- The fixed Soundiiz endpoint is `https://soundiiz.com/go/import-playlist`. Redirects are not followed, and returned handoff URLs must use the exact HTTPS Soundiiz host and documented path.
- Mutating local API calls require a custom header and same-origin checks. DNS-rebinding hosts and cross-site browser requests are rejected.
- JSON request bodies are size-limited, unknown fields are rejected, text is rendered without raw HTML, and the embedded site uses a restrictive Content Security Policy.
- Console logs redact OpenAI-key-shaped values. Prompts and tracklists appear only at debug level.
- `OPENAI_API_KEY` is read at runtime, never returned by the API, and cannot be changed or deleted through the web interface while present.
- The container runs as UID 65532 with no Linux capabilities. Its root filesystem can be read-only; only `/config` and the temporary filesystem need write access.

## Troubleshooting

- **Port occupied:** use `--port` with another number. Native runs retain the `127.0.0.1` default.
- **Container cannot write its database:** use the named volume from the examples or grant UID 65532 read/write access to a bind-mounted config directory.
- **Browser did not open:** visit the URL printed in the console or use `--open-browser=false`.
- **Credential store unavailable:** enable the config-file fallback in Settings, or configure Keychain/Credential Manager/Secret Service.
- **OpenAI validation failed:** confirm the key is active, billing is enabled, and the project can access `gpt-5.6-sol`.
- **Operation is taking a while:** after three seconds the app shows live job progress and a Cancel button. Higher reasoning efforts and larger playlists take longer.
- **Soundiiz link expired:** create a fresh handoff from the saved playlist preview.

## License

Copyright © 2026 Paul Pietkiewicz. Playlist Forge is distributed under the MIT License; see `LICENSE`.
