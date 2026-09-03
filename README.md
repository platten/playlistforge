# Playlist Forge

**Turn a sentence into a researched playlist, review every track, and hand it to your streaming service.**

Playlist Forge is a local, single-user desktop application. You describe the
playlist you want; it uses OpenAI's Responses API (`gpt-5.6-sol`) with web search
to assemble a coherent, intentionally ordered tracklist of real recordings. You
review and revise it track by track, then generate a temporary
[Soundiiz](https://soundiiz.com) import link to transfer it to TIDAL, Qobuz,
Spotify, Apple Music, or another service Soundiiz supports.

The React/TypeScript interface is embedded in a single Go binary that runs on the
operating system's native webview. There is no server, no bundled runtime, and no
database service — just an executable and a local SQLite file.

---

## Highlights

- **Prompt to playlist.** A natural-language brief becomes a titled, ordered
  playlist of 20–100 tracks, each with a rationale and recording/version notes.
- **Every edit is a revision.** Remove a track, request a single replacement, or
  refine the whole playlist with another prompt. Nothing is overwritten;
  history stays browsable.
- **Grounded, not guessed.** The model is instructed to verify tracks and
  requested versions with web search and to avoid invented recordings.
- **Transparent cost.** Each request shows an estimated USD cost derived from
  reported token and web-search usage against a versioned rate card.
- **Private by default.** The OpenAI key lives in your OS credential store; it is
  never written to the playlist database and never returned to the frontend.
  Soundiiz receives only titles and artist names — never streaming credentials.
- **Local-first.** No account, no telemetry, no network listener. Data lives in
  your OS-standard application directory.

## How it works

1. **Describe** the playlist, pick a track count and reasoning effort, and
   optionally choose earlier playlists as inspiration.
2. **Generate.** Playlist Forge researches and orders the tracklist, then shows
   the title, description, per-track rationale, and cost estimate.
3. **Revise.** Remove, replace, or refine. Each operation writes an immutable
   local revision.
4. **Hand off.** Create a Soundiiz import link and complete the transfer on
   Soundiiz, which performs catalog matching and writes to your streaming
   service.

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for package responsibilities, security
invariants, and the files that must change together when an API or persisted
model evolves.

## Install

Download the build for your platform and launch **Playlist Forge**. It opens its
own window and does not listen on a network port. Every platform is built for
both x86-64 and ARM64.

| Platform | Artifact | Notes |
| --- | --- | --- |
| Windows | `playlist-forge-<version>-windows-<arch>-setup.exe` | NSIS installer for x64 or ARM64; suitable for Winget. |
| Windows (portable) | `playlist-forge-<version>-windows-<arch>.exe` | The standalone application. No installation — download and run it. |
| macOS | `playlist-forge.app` (universal) | Intel and Apple Silicon in one bundle. |
| Linux | `.deb`, `.rpm`, `.AppImage` (per architecture) | Needs GTK 3 and WebKitGTK 4.1; the `.deb`/`.rpm` declare this. The `.AppImage` is portable. |

Release builds are currently unsigned. Configure Windows code-signing and an
Apple Developer ID / notarization identity before distributing them as a
warning-free public release.

## First run: connect an OpenAI API key

Open **Settings**, paste a key into **API key**, and choose **Save key**.
Playlist Forge validates that the key can access `gpt-5.6-sol` before storing it.

To create a key:

1. [Create an OpenAI Platform account or sign in](https://platform.openai.com/signup).
2. Open [Billing settings](https://platform.openai.com/settings/organization/billing/overview)
   and add a payment method or credits. API calls are billed by usage.
3. On the [API keys page](https://platform.openai.com/api-keys), choose **Create
   new secret key**, name it (e.g. `Playlist Forge`), and copy the value.
4. Paste it into Playlist Forge and save.

Treat the key like a password: never share it, commit it, put it in a screenshot,
or paste it into an untrusted page. You can revoke it from the API keys page at
any time.

### Where the key is stored

`OPENAI_API_KEY`, if set, takes precedence and is shown in Settings as a
read-only, environment-managed credential. Otherwise Playlist Forge uses the OS
credential store — Windows Credential Manager, macOS Keychain, or the Linux
Secret Service. If that store is unavailable, Settings offers an explicit opt-in
to a permission-restricted `config.json` fallback in the application directory.
The key is never written to `playlists.db`.

## Using Playlist Forge

1. Describe the playlist, choose 20 / 30 / 40 / 50 / 60 / 100 tracks, and select
   a reasoning effort (Medium is the default; higher efforts take longer).
2. Optionally select up to ten earlier playlists as inspiration.
3. Review the generated title, description, ordering, recording and version
   notes, and per-track rationale.
4. Remove tracks, request individual replacements, or refine the whole playlist
   with another prompt. Each operation creates an immutable local revision.
5. From the playlist preview, choose **Open Soundiiz handoff** to create a
   temporary public import link (valid for roughly 24 hours).

Soundiiz receives only the accepted playlist title, description, track titles,
and artist names. Playlist Forge never receives your streaming credentials.

## Completing a transfer with Soundiiz

Playlist Forge prepares a temporary import page; Soundiiz handles authorization
and writes the playlist to the streaming service.

### One-time setup

1. Sign in to the destination streaming service (in its own app or website) on
   the exact profile where the playlist should be created. Some services require
   a paid subscription for third-party playlist writes.
2. [Create a Soundiiz account](https://soundiiz.com/register) or sign in. Use a
   consistent sign-in method so you do not create a second Soundiiz account.
3. In Soundiiz, open the left panel, select the streaming service (or **Connect
   more services**), choose **Connect**, and complete its authorization screen.
   The icon turns green on success. Manage connections later under **Settings >
   Platforms**. See the
   [Soundiiz connection guide](https://support.soundiiz.com/hc/en-us/articles/360024694393-How-to-connect-your-music-accounts-to-Soundiiz).
4. Check the service-specific prerequisite:

   | Service | Before authorizing Soundiiz |
   | --- | --- |
   | TIDAL | Use the same TIDAL sign-in method that owns the intended library. Google, Apple, Facebook, and email/password sign-ins can resolve to different TIDAL profiles even with a shared email address. |
   | Qobuz | Sign in to the intended Qobuz account in the same browser first. To switch accounts, disconnect Qobuz under **Settings > Platforms**, sign out, sign in to the other account, and reconnect. |
   | Spotify | Approve Soundiiz's playlist read/write permissions. If Spotify later expires the grant, reconnect under **Settings > Platforms** and retry. |
   | Apple Music | Use the Apple Account with an active Apple Music subscription, enable **Sync Library**, and allow pop-ups and cookies during authorization. See the [Apple Music checklist](https://support.soundiiz.com/hc/en-us/articles/360009609854-I-can-t-connect-my-Apple-Music-account). |

### Each transfer

1. In Playlist Forge, accept the playlist and choose **Open Soundiiz handoff**.
2. Sign in to Soundiiz if prompted. Review the imported title and tracks,
   inspect the catalog matches, and correct or skip any mismatch.
3. Select the connected destination account and start the transfer. When
   Soundiiz reports completion, open the streaming service and verify the
   playlist and track versions.
4. To send the same playlist elsewhere, connect and select that service in
   Soundiiz. Create a fresh handoff from Playlist Forge only if the link has
   expired.

The free Soundiiz plan supports one transfer at a time of up to 200 tracks,
which covers Playlist Forge's 100-track maximum; see
[Soundiiz plans](https://soundiiz.com/pricing) for current limits. Catalogs vary
by service and region, so a track may be unavailable or resolve to a different
edition.

## Cost estimates

Each request's preview shows an estimated cost derived from reported input,
cached-input, output, reasoning, and web-search usage. The embedded rate card is
versioned `2026-09-01`: `$4.00` / M input tokens, `$0.40` / M cached input
tokens, `$20.00` / M output tokens, and `$0.01` per web-search call.
Search-result content counts toward input tokens.

This is an estimate, not an invoice. Prices change, long-context or regional
processing may use different rates, and credits or discounts are not reflected.
Your OpenAI dashboard is authoritative.

## Building from source

**Requirements:** Go 1.27+, Node.js 24, pnpm 11.19. On Linux, the WebKitGTK
build dependencies (`libgtk-3-dev`, `libwebkit2gtk-4.1-dev` on Debian/Ubuntu).

This is a [Wails v3](https://v3.wails.io) application. Wails v3 embeds the built
frontend with a plain `//go:embed`, so a build is: build the frontend, then
`go build`. There is no `wails build` step.

Run the desktop build for the current OS:

```sh
bash scripts/build-desktop.sh          # macOS / Linux
pwsh -File scripts/build-desktop.ps1   # Windows
```

Artifacts land in `build/bin`. On Linux the build targets the host architecture
and produces the `.deb`, `.rpm`, and `.AppImage`; macOS produces the universal
`playlist-forge.app`; Windows cross-compiles both architectures and produces
the standalone `playlist-forge-<arch>.exe` plus NSIS installers with silent
`/S` install and uninstall support (the release job also publishes the exe as
`playlist-forge-<version>-windows-<arch>.exe`). Pass `-SkipInstaller` when NSIS
packaging is not needed. The product version comes
from the `VERSION` file. `build/appicon.svg` is the icon
master; `build/appicon.png`, `build/darwin/icons.icns`, and
`build/windows/icon.ico` are generated from it.

For an iteration loop without packaging:

```sh
pnpm --dir web run build
CGO_ENABLED=1 go run -tags gtk3 .   # Linux (GTK3 / WebKitGTK 4.1)
go run .                            # macOS / Windows
```

## Development and testing

`scripts/test.sh` (Bash) and `scripts/test.ps1` (PowerShell) are the canonical
quality gate. Each installs the locked frontend dependencies and runs the full
frontend and Go checks — formatting, linting, type-checking, unit tests, the Go
race detector (when CGO is available on Linux or macOS), and the embedded
frontend build — then enforces coverage thresholds.

```sh
bash scripts/test.sh
pwsh -File scripts/test.ps1
```

Pass `--skip-tests` / `-SkipTests` to a build script only when the matching test
script has already passed for the same source state.

The individual commands, for troubleshooting a single stage:

```sh
# Go
gofmt -l main.go internal
go vet -tags gtk3 ./...      # drop -tags gtk3 off Linux
go test -tags gtk3 -race ./...

# Frontend (from web/)
pnpm run format:check
pnpm run lint
pnpm run typecheck
pnpm test
pnpm run build
```

**Coverage.** CI requires ≥ 95% statement coverage across the business and
external-boundary packages (`app`, `playlist`, `openaiapi`, `soundiiz`). The
frontend adapter, job polling, and delayed-overlay logic are held to 95% for
statements, branches, functions, and lines; page-level tests additionally cover
rendering and XSS-safe handling of user text.

**Releases.** Pushing a `v*` tag runs `.github/workflows/release.yml`: it writes
the tag version to `VERSION`, builds the native Linux, Windows, and macOS
artifacts (Linux on both an x86-64 and an ARM64 runner), writes
`SHA256SUMS.txt`, and creates or updates the GitHub Release.

## Security

- The OpenAI base URL is fixed to `https://api.openai.com/v1`; no environment
  variable can redirect it.
- The Soundiiz endpoint is fixed to `https://soundiiz.com/go/import-playlist`.
  Redirects are not followed, and a returned handoff URL must use the exact
  HTTPS Soundiiz host and documented path or it is rejected.
- User text is rendered without raw HTML. The desktop API validates every
  external URL before opening it and validates domain input before any paid or
  persisted operation.
- Model instructions are a trusted policy boundary; user prompts and stored
  playlists are supplied as data and cannot override them.
- Console logs redact OpenAI-key-shaped values. Prompts and tracklists are
  logged only at debug level.
- `OPENAI_API_KEY` is read at runtime, never returned to the frontend, and
  cannot be changed or removed through the interface while it is set.

## Troubleshooting

| Symptom | Resolution |
| --- | --- |
| Credential store unavailable | Enable the config-file fallback in Settings, or configure Keychain / Credential Manager / Secret Service. |
| OpenAI validation failed | Confirm the key is active, billing is enabled, and the project can access `gpt-5.6-sol`. |
| Operation is slow | After three seconds the app shows live progress and a Cancel button. Higher efforts and larger playlists take longer. |
| Soundiiz link expired | Create a fresh handoff from the saved playlist preview. |

## License

Playlist Forge is distributed under the MIT License; see [`LICENSE`](LICENSE).
Copyright © 2026 Paul Pietkiewicz.
