# Playlist Forge architecture

This document records the boundaries and invariants that are easy to lose when changing the application. Code comments explain local decisions; this document explains how the pieces fit together.

## Design goals

The build is organized around a few deliberate constraints:

- Ship one executable with no runtime Node.js process, external database, or asset directory.
- Run as a private, single-user application reached through `127.0.0.1`; the container may bind its isolated interface only when Docker publishes it back to host loopback.
- Keep business rules independent from HTTP, OpenAI, Soundiiz, SQLite, and React.
- Make every paid or externally visible operation cancellable and testable behind an interface.
- Preserve playlist history as immutable revisions.
- Keep the browser edition cross-compilable without CGO for Windows, Linux, and macOS on AMD64 and ARM64.
- Offer a native Wails shell without coupling business rules to Wails or removing the loopback browser/container distribution.

## Technology stack

The backend uses Go 1.27, Wails 2.15, the standard `net/http` server, Zap logging, the official OpenAI Go SDK, a platform-keyring adapter, and the pure-Go `modernc.org/sqlite` driver. The frontend uses React 19, TypeScript 6, and Vite 8. Vitest, Testing Library, jest-axe, ESLint, and Prettier provide frontend quality gates.

Exact dependency versions belong in `go.mod`, `go.sum`, `web/package.json`, and `web/pnpm-lock.yaml`; those files are authoritative when this overview and a dependency manifest disagree.

## How the executable is built

The build has two compilation stages:

```text
web/src + web/index.html
        │
        │ TypeScript compiler + Vite
        ▼
internal/webui/dist
        │
        │ //go:embed dist
        ▼
internal/webui.Assets (embed.FS)
        │
        │ Go compiler and linker
        ▼
playlist-forge executable
```

1. pnpm installs the exact frontend dependency graph from `web/pnpm-lock.yaml`.
2. TypeScript type-checks the frontend, and Vite emits hashed production assets into `internal/webui/dist`.
3. `internal/webui/embed.go` embeds that directory into an `embed.FS` at Go compile time.
4. `cmd/playlistforge/main.go` becomes the composition root. It constructs logging, OS-specific data paths, SQLite, credential storage, provider adapters, the application service, and the HTTP server.
5. The Go linker produces a self-contained executable with `-trimpath -ldflags="-s -w"`. No frontend files are read from disk at runtime.

The frontend build must therefore finish before `go build`. A missing `internal/webui/dist` is a build error by design; it prevents accidentally shipping a binary without its interface.

The browser edition uses a pure-Go SQLite implementation and builds with `CGO_ENABLED=0`, which makes ordinary Go cross-compilation sufficient for all supported targets. Wails desktop builds use each target platform's native webview toolchain.

The `Dockerfile` repeats the same two build stages in isolated Node and Go builders. It cross-compiles the requested Linux architecture and copies only the static executable into a distroless image containing CA certificates. The runtime is non-root, declares `/config` as its writable data volume, and does not contain a shell, compiler, package manager, Node.js, or pnpm.

## Runtime shape

Playlist Forge has two presentation adapters over the same local service:

```text
Wails window ──► internal/desktop ──┐
Browser ──────► internal/httpapi ───┴──► internal/app ─────► internal/playlist
                                           │                  domain model and ports
                                           ├────► internal/openaiapi ──► OpenAI Responses API
                                           ├────► internal/soundiiz ───► Soundiiz import endpoint
                                           └────► internal/storage ────► local SQLite database
```

`internal/bootstrap` composes storage, credentials, providers, and the application service. The repository-root Wails entrypoint embeds the same Vite distribution and binds `internal/desktop.API`; `cmd/playlistforge` retains the loopback HTTP edition.

The frontend selects its transport at startup. Wails injects `window.go.desktop.API`, while an ordinary browser uses the HTTP client. Page components depend only on the shared `BackendAPI` contract.

The native server binds to the IPv4 loopback literal `127.0.0.1`. The container explicitly sets `PLAYLIST_FORGE_HOST=0.0.0.0` inside its network namespace so Docker can forward traffic, but the supported publication remains `127.0.0.1:<host-port>:8787`. Host validation still accepts only the literal loopback Host header. The application is intentionally unauthenticated and must not be published to a LAN, public interface, or external reverse proxy without adding a real authentication and authorization design.

## Startup and shutdown

`cmd/playlistforge/main.go` performs startup in this order:

1. Parse flags and environment-backed defaults.
2. Build a console or JSON Zap logger with global secret redaction.
3. Resolve `PLAYLIST_FORGE_CONFIG_DIR` when explicitly set, otherwise create `playlist-forge` beneath the OS-standard user configuration directory.
4. Open `playlists.db`, limit SQLite to one connection, and apply the idempotent embedded schema.
5. Construct the keyring, OpenAI, Soundiiz, application-service, and HTTP adapters.
6. Listen on the validated bind address and optionally open the system browser. Only `127.0.0.1` and the container-oriented `0.0.0.0` value are accepted.
7. On SIGINT or SIGTERM, cancel application jobs, stop accepting requests, and perform a bounded graceful HTTP shutdown.

Native runtime files default to `os.UserConfigDir()/playlist-forge`; container files default to `/config`. The SQLite database never contains the OpenAI API key. `OPENAI_API_KEY` takes precedence and is read-only to the web interface. The optional `config.json` credential fallback is created only after explicit user consent when the platform keyring is unavailable.

## Package responsibilities

- `internal/playlist` owns dependency-free models, validation, cost estimation, and the interfaces implemented by infrastructure.
- `internal/app` implements use cases, background-job state, cancellation, paid-operation serialization, and revision orchestration.
- `internal/httpapi` owns routing, strict JSON decoding, same-origin defenses, security headers, and SPA delivery. It must not contain playlist business rules.
- `internal/openaiapi` owns the trusted curator instructions, strict output schemas, the fixed OpenAI endpoint, usage extraction, and provider adaptation.
- `internal/soundiiz` owns the documented public import payload and strict validation of the returned handoff URL.
- `internal/storage` owns schema and transactions. Playlist edits append immutable revisions.
- `internal/credentials` owns environment, keyring, and explicitly authorized config-file credential precedence.
- `internal/logging` owns Zap construction and mandatory secret redaction.
- `web/src/api.ts` is the only browser HTTP adapter. React pages consume its typed methods rather than calling `fetch` directly.
- `web/src/ApiKeyHelpDialog.tsx` owns accessible API-account onboarding, official Platform links, focus trapping, and focus restoration.

Dependencies point inward: adapters depend on `internal/playlist`, while the domain package does not import adapters.

## Dependency construction

The application uses constructor injection rather than global clients. `app.Service` receives the domain `Repository` and `Generator` interfaces plus a small Soundiiz importer interface. HTTP key validation and credential storage are also interfaces. Production constructors supply real adapters; tests supply deterministic fakes or `httptest` servers.

This arrangement gives the dependency direction:

```text
cmd/playlistforge
  ├── httpapi ──► app ──► playlist interfaces
  ├── storage ───────────► playlist models
  ├── openaiapi ─────────► playlist models
  ├── soundiiz ──────────► playlist models
  ├── credentials
  └── logging
```

No provider SDK type crosses into the domain or browser contract.

## Main request flows

### Generate or refine a playlist

1. React calls the typed adapter in `web/src/api.ts`.
2. `internal/httpapi` validates method, host, origin, protection header, content type, body size, unknown JSON fields, and domain input.
3. `app.Service` creates an in-memory job and returns HTTP 202 immediately.
4. The browser polls `/api/jobs/{id}`. A modal status appears only after three seconds.
5. A one-slot application gate serializes paid work. Cancellation propagates through `context.Context`.
6. `internal/openaiapi` reads the key, calls the fixed Responses API endpoint with response storage disabled, enables web search, and requires strict JSON schema output.
7. The domain validates count, required metadata, ordering, and duplicate title/artist keys.
8. `internal/storage` commits a new immutable revision and advances the aggregate's current-revision pointer in one transaction.
9. The completed job contains the playlist ID; React reloads the saved representation.

Track replacement follows the same asynchronous path. Track removal is local and synchronous, but still appends a revision rather than mutating history.

### Create a Soundiiz handoff

1. The user requests a generic Soundiiz handoff from the playlist preview.
2. The backend loads the accepted revision and sends only playlist title, description, track titles, and artists to Soundiiz.
3. Redirects are disabled. The returned URL must use HTTPS, the exact `soundiiz.com` host, and the documented import path.
4. The temporary link and expiry are saved locally.
5. React opens the validated generic URL. Matching, destination selection, authentication, and final transfers happen on Soundiiz.

### Save an OpenAI API key

1. At startup and for every provider call, a non-empty `OPENAI_API_KEY` wins over all persisted credentials.
2. Environment-managed credentials are exposed only as configured/read-only status; the browser cannot replace or delete them.
3. Without the environment value, the browser sends a submitted key only to the loopback API and the backend validates model access before storage.
4. The platform credential store is attempted first.
5. A synchronized, atomically replaced config file is used only when keyring storage fails and the user opted into that fallback.
6. Status responses expose only `configured`, storage type, and read-only state, never the key.

## Persistence model

SQLite stores four related concepts:

- `playlists` is the aggregate root and points to the active revision.
- `revisions` contains immutable title, prompt, model, effort, usage, and timestamp snapshots.
- `tracks` stores ordered recording candidates scoped to a revision.
- `revision_references` records which previous playlists influenced generation.

Usage is stored as versioned JSON because provider counters can evolve independently from the relational playlist model. Times are written as UTC RFC 3339 values. Writes that create or activate revisions use SQL transactions.

The current schema is embedded with `go:embed` and is idempotent for a new database. A future incompatible change must introduce an explicit versioned migration instead of editing existing-column meaning in place.

## HTTP and frontend boundary

The JSON API is intentionally small:

| Route | Methods | Responsibility |
| --- | --- | --- |
| `/api/config` | GET | Non-secret runtime options and rate-card metadata |
| `/api/config/openai-key` | PUT, DELETE | Validate, save, or remove the API key |
| `/api/playlists` | GET, POST | List playlists or queue generation |
| `/api/playlists/{id}` | GET | Load the active revision |
| `/api/playlists/{id}/refine` | POST | Queue full-playlist refinement |
| `/api/playlists/{id}/tracks/{track}` | DELETE | Append a revision without one track |
| `/api/playlists/{id}/tracks/{track}/replace` | POST | Queue one replacement |
| `/api/playlists/{id}/soundiiz` | POST | Queue generic Soundiiz handoff creation |
| `/api/jobs/{id}` | GET, DELETE | Poll or cancel process-local work |

Unknown frontend routes fall back to embedded `index.html`, allowing the History API router to resolve direct navigation. Hashed assets receive immutable cache headers; the HTML shell and all API responses use `no-store` where appropriate.

## Important invariants

1. OpenAI and Soundiiz base URLs are fixed in code. Environment variables must not redirect requests carrying user data or credentials.
2. OpenAI responses are requested with storage disabled and validated before persistence.
3. A replacement may not duplicate another title/artist pair in the active playlist.
4. Removing or changing tracks creates a revision; historical rows are not mutated.
5. At least one track must remain, and Soundiiz receives no more than 200 tracks.
6. A generic Soundiiz handoff does not choose or perform the final transfer; Soundiiz owns matching and destination selection.
7. API keys never enter SQLite, API responses, prompts, container layers, or normal logs. Key-shaped values are redacted by the logging core.
8. Browser mutations require the custom protection header and a safe same-origin context.
9. Long-running paid jobs are serialized and are cancelled during process shutdown.
10. Pricing is versioned. A displayed estimate is not an invoice.

## Changing an external integration

Keep provider changes behind the existing ports. Update the adapter, its `httptest` contract tests, validation, documented endpoint, and cost/version metadata together. Do not expose provider SDK types through `internal/playlist` or `internal/app`.

When changing the OpenAI schema, update all of the following:

- `internal/openaiapi/client.go`
- `internal/openaiapi/client_test.go`
- `internal/playlist/model.go`
- `web/src/types.ts`
- preview rendering and tests, if a field is user-visible

When changing persisted fields, update `schema.sql`, repository reads/writes, lifecycle tests, API types, and this document. Introduce an explicit migration before making a non-additive schema change.

## Frontend maintenance

The UI deliberately uses a small History API router and no state framework. Keep server state in the API adapter and page state in the owning component. Extract a page or hook when it becomes independently reusable or its tests require excessive parent setup.

React renders all user and provider strings as text. Do not add raw HTML rendering. Preserve semantic labels, live status regions, focus handling, reduced-motion behavior, and the three-second delayed busy overlay.

## Security boundaries

The process assumes that the local operating-system account is trusted. It does not assume that arbitrary websites open in the same browser are trusted.

- Native listener defaults and Host validation accept the literal IPv4 loopback address. The wildcard listener is an explicit container mechanism and does not relax Host validation.
- Mutations require a non-simple `X-Playlist-Forge` header and same-origin browser context.
- A restrictive Content Security Policy blocks third-party scripts, framing, plugins, and unexpected connections.
- JSON is size-limited, requires the correct media type, rejects unknown fields, and accepts exactly one object.
- Fixed provider URLs, disabled Soundiiz redirects, and returned-URL validation constrain outbound trust.
- OpenAI-key-shaped text is redacted from Zap messages, strings, errors, and reflected values.
- Prompts and tracklists are debug-only logs. Production defaults to info level.

Exposing the service beyond loopback, supporting multiple users, or adding remote browser access changes the threat model and requires authentication, authorization, session management, TLS, CSRF tokens, and revised credential isolation.

The supplied Compose definition keeps the host port on loopback, runs the image without capabilities or privilege escalation, makes the root filesystem read-only, and limits writes to `/config` plus a small temporary filesystem. Runtime environment variables and mounted configuration are deployment inputs, not image build inputs.

## Cross-platform release pipeline

`scripts/build.sh` and `scripts/build.ps1` implement equivalent release workflows. They run their corresponding test script by default, build the frontend, set `CGO_ENABLED=0`, compile six targets, and write `SHA256SUMS.txt`:

| Operating system | Architectures | Artifact pattern |
| --- | --- | --- |
| Windows | AMD64, ARM64 | `playlist-forge-windows-*.exe` |
| Linux | AMD64, ARM64 | `playlist-forge-linux-*` |
| macOS | AMD64, ARM64 | `playlist-forge-darwin-*` |

GitHub Actions invokes the Bash quality gate on Ubuntu, including the race detector when CGO is available, then calls the Bash build script and uploads the complete artifact directory. CI also builds Linux AMD64 and ARM64 container images; pull requests build without pushing, while default-branch commits receive branch and commit tags in GitHub Container Registry. The PowerShell scripts provide the same native workflow for local Windows maintainers.

Native desktop builds run on a runner for each target operating system. The Linux Wails stage links WebKitGTK 4.1, then `scripts/package-linux.sh` uses pinned nFPM and AppImage tooling to produce deb, rpm, and AppImage packages. Each package carries the desktop entry, application icon, and AppStream metadata; deb and rpm metadata use distribution-specific GTK/WebKit dependency names.

Pushing a `v*` tag runs the release workflow after a fresh quality gate. The workflow builds all six native binaries, publishes a multi-platform GHCR image with semantic-version and `latest` tags, emits provenance and an OCI SBOM attestation, scans the published image into an SPDX JSON SBOM, includes that file in `SHA256SUMS.txt`, and attaches the binaries, checksums, and SBOM to an idempotently created or updated GitHub Release.

## Quality gates

Run either platform-equivalent test entry point:

```text
bash scripts/test.sh
pwsh -File scripts/test.ps1
```

The scripts check formatting, ESLint, TypeScript, frontend tests and coverage, Go Vet, Go tests, supported-host race detection, and at least 95% aggregate coverage across business and external-API boundary packages. Instrumented Go packages run sequentially and their native coverage data is merged; this is portable across Linux CI, PowerShell, and Git Bash on Windows.

Build scripts run tests by default, rebuild the embedded frontend, cross-compile all supported targets with `CGO_ENABLED=0`, and write SHA-256 checksums:

```text
bash scripts/build.sh
pwsh -File scripts/build.ps1
```

Use `--skip-tests` or `-SkipTests` only when the corresponding test script has already succeeded for the same source state.
