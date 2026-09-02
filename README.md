# Playlist Forge

Playlist Forge is a local, single-user desktop application that turns a natural-language brief into a researched playlist. It uses OpenAI's Responses API with `gpt-5.6-sol`, lets you review and revise every track, and creates a temporary Soundiiz handoff link for TIDAL, Qobuz, Spotify, Apple Music, or another service Soundiiz supports.

Created by Paul Pietkiewicz (`773636+platten@users.noreply.github.com`) and distributed under the MIT License.

The React/TypeScript frontend is embedded into the Go binary. The Wails desktop application uses the operating system's native webview and calls Go directly. It does not need Node.js or a database service at runtime.

See `ARCHITECTURE.md` for package responsibilities, security invariants, extension guidance, and the files that must change together when an API or persisted model evolves.

## Run the desktop application

Download the desktop artifact for your platform and launch **Playlist Forge**. The desktop edition opens its own window and does not listen on a local TCP port.

Every platform is built for both x86-64 and ARM64:

- Windows uses WebView2 and is distributed as a standalone executable (`playlist-forge-amd64.exe`, `playlist-forge-arm64.exe`).
- macOS is a single universal application bundle for Intel and Apple Silicon.
- Linux builds produce `.deb`, `.rpm`, and AppImage packages for `amd64` and `arm64`. They use GTK3 and WebKitGTK 4.1; install your distribution's WebKitGTK runtime package if it is not already present. The deb and rpm packages declare these dependencies for their package managers.

This is a [Wails v3](https://v3.wails.io) application. Wails v3 embeds the built
frontend through a plain `//go:embed` directive, so desktop development is a
frontend build followed by `go build`/`go run` — there is no `wails build` step.
Install your platform's WebView prerequisites (on Linux, `libgtk-3-dev` and
`libwebkit2gtk-4.1-dev`), then run:

```sh
pnpm --dir web run build
# Linux uses the GTK3 / WebKitGTK 4.1 stack, selected with the gtk3 build tag:
CGO_ENABLED=1 go run -tags gtk3 .
# macOS / Windows need no build tag:
go run .
```

Build a production desktop artifact for the current operating system with:

```sh
bash scripts/build-desktop.sh
# Windows PowerShell:
pwsh -File scripts/build-desktop.ps1
```

Artifacts are written under `build/bin`. On Linux the build targets the host
architecture and includes `playlist-forge_VERSION_<amd64|arm64>.deb`,
`playlist-forge-VERSION-1.<x86_64|aarch64>.rpm`, and
`playlist-forge-VERSION-<x86_64|aarch64>.AppImage`. macOS builds produce a
universal `playlist-forge.app` bundle; Windows builds cross-compile both
`playlist-forge-amd64.exe` and `playlist-forge-arm64.exe`. The product version
comes from the `VERSION` file. The shared icon master is
`build/appicon.png`; the Linux packaging stage derives its native resources from
it.

CI currently produces unsigned desktop artifacts. Configure Windows code-signing and an Apple Developer ID/notarization identity before treating downloads as a warning-free public release.

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

Run the native desktop build for the current operating system:

```text
bash scripts/build-desktop.sh
# Windows PowerShell:
pwsh -File scripts/build-desktop.ps1
```

Wails desktop builds are native: use Windows for the Windows executables, macOS for the universal application, and Linux for the deb, rpm, and AppImage packages. GitHub Actions runs each build on its corresponding operating system, and Linux runs on both an x86-64 and an ARM64 runner.

## Development and verification

The repository provides equivalent Bash and PowerShell entry points. Both install the locked frontend dependencies, run frontend and Go quality gates, enforce 95% core coverage, and rebuild the embedded frontend:

```text
bash scripts/test.sh
pwsh -File scripts/test.ps1
```

The race detector runs by default when CGO is available on Linux or macOS. Use `--skip-race` or `-SkipRace` only when intentionally testing on an unsupported host.

Build the desktop application after running the tests:

```text
bash scripts/build-desktop.sh --skip-tests
pwsh -File scripts/build-desktop.ps1 -SkipTests
```

Use `--skip-tests` or `-SkipTests` only when the corresponding test script already completed for the same source state. Desktop artifacts are written under `build/bin`.

The individual commands run by the scripts are documented below for troubleshooting.

Run backend checks:

```text
gofmt -w main.go internal
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

CI enforces at least 95% statement coverage across the business and external-API boundary packages (`app`, `playlist`, `openaiapi`, and `soundiiz`). Frontend Wails bindings, job-polling, and slow-operation state are held to 95% for statements, branches, functions, and lines; page-level tests additionally cover rendering and XSS-safe treatment of user text. Persistence, credential fallback, cancellation, logging, and responsive rendering have dedicated tests.

Pushing a tag such as `v1.2.3` runs `.github/workflows/release.yml`. After the quality gate, it writes the tag version to `VERSION`, builds the native Linux, Windows, and macOS desktop artifacts, writes SHA-256 checksums, and creates or updates the GitHub Release.

## Security notes

- The fixed OpenAI base URL is `https://api.openai.com/v1`; environment variables cannot redirect it.
- The fixed Soundiiz endpoint is `https://soundiiz.com/go/import-playlist`. Redirects are not followed, and returned handoff URLs must use the exact HTTPS Soundiiz host and documented path.
- Text is rendered without raw HTML, and the desktop API validates domain input before paid or persisted operations.
- Console logs redact OpenAI-key-shaped values. Prompts and tracklists appear only at debug level.
- `OPENAI_API_KEY` is read at runtime, never returned to the frontend, and cannot be changed or deleted through the desktop interface while present.

## Troubleshooting

- **Credential store unavailable:** enable the config-file fallback in Settings, or configure Keychain/Credential Manager/Secret Service.
- **OpenAI validation failed:** confirm the key is active, billing is enabled, and the project can access `gpt-5.6-sol`.
- **Operation is taking a while:** after three seconds the app shows live job progress and a Cancel button. Higher reasoning efforts and larger playlists take longer.
- **Soundiiz link expired:** create a fresh handoff from the saved playlist preview.

## License

Copyright © 2026 Paul Pietkiewicz. Playlist Forge is distributed under the MIT License; see `LICENSE`.
