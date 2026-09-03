# Playlist Forge architecture

This document records the boundaries and invariants that are easy to lose when changing the application. Playlist Forge is distributed only as a native Wails desktop application.

## Design goals

- Ship a native desktop application with no runtime Node.js process or database service.
- Keep business rules independent from Wails, OpenAI, Soundiiz, SQLite, and React.
- Make every paid or externally visible operation cancellable and testable behind an interface.
- Preserve playlist history as immutable revisions.
- Build and package on Windows, Linux, and macOS using each platform's native toolchain.

## Technology stack

The backend uses Go 1.27, Wails v3 (`v3.0.0-beta.16`), Zap logging, the official OpenAI Go SDK, a platform-keyring adapter, and the pure-Go `modernc.org/sqlite` driver. The frontend uses React 19, TypeScript 6, and Vite 8. Vitest, Testing Library, jest-axe, ESLint, and Prettier provide frontend quality gates.

Exact dependency versions belong in `go.mod`, `go.sum`, `web/package.json`, and `web/pnpm-lock.yaml`; those files are authoritative when this overview and a dependency manifest disagree.

## Desktop build and runtime

The build has two compilation stages:

```text
web/src + web/index.html
        │
        │ TypeScript compiler + Vite
        ▼
internal/webui/dist
        │
        │ root main.go //go:embed
        ▼
Wails desktop executable
```

1. pnpm installs the locked frontend dependency graph.
2. TypeScript checks the frontend and Vite emits hashed assets into `internal/webui/dist`.
3. The repository-root `main.go` embeds those assets, constructs the application runtime, registers `internal/desktop.API` as a Wails v3 service, and starts the application.
4. `go build` links the executable against the target platform's native webview; `scripts/build-desktop.*` then wrap it in platform packages.

The frontend build must finish before Go compilation. A missing `internal/webui/dist` is a build error by design, preventing an application without its interface from being shipped.

At runtime, React calls the typed adapter in `web/src/api.ts`. That adapter delegates exclusively to `internal/desktop.API` through the Wails v3 runtime (`@wailsio/runtime` `Call.ByName`); there is no HTTP server or browser transport.

```text
React ──► Wails bindings ──► internal/desktop ──► internal/app
                                                      │
                           ┌──────────────────────────┼──────────────┐
                           ▼                          ▼              ▼
                     internal/storage         internal/openaiapi  internal/soundiiz
```

Application data defaults to `os.UserConfigDir()/playlist-forge`. The SQLite database never contains the OpenAI API key. `OPENAI_API_KEY` takes precedence over persisted credentials and is exposed to the UI only as configured/read-only status. The optional restricted `config.json` fallback is created only after explicit user consent when the platform keyring is unavailable.

## Package responsibilities

- `internal/playlist` owns dependency-free models, validation, cost estimation, same-music matching (`SameMusic`, ISRC-first then title/artist), and infrastructure interfaces.
- `internal/app` implements use cases, background jobs, cancellation, paid-operation serialization, and revision orchestration.
- `internal/desktop` exposes the narrow presentation contract bound into Wails and validates external URLs before opening them.
- `internal/bootstrap` composes storage, credentials, providers, logging, and the application service.
- `internal/openaiapi` owns curator instructions, strict output schemas, the fixed OpenAI endpoint, usage extraction, and provider adaptation.
- `internal/soundiiz` owns the public import payload and validates returned handoff URLs.
- `internal/musicsource` is the port for reading a listener's existing playlists from a streaming service; `internal/musicsource/tidal` (OAuth device flow, approved in the system browser) and `internal/musicsource/qobuz` (web-player token captured from the embedded sign-in window) are reverse-engineered adapters using community client credentials and undocumented endpoints, and `internal/musicsource/fake` drives the import pipeline in tests.
- `internal/connections` owns the per-service streaming session store: OS keyring first, with an automatic 0600 file fallback under the application directory for hosts without a Secret Service (headless Linux, WSL).
- Streaming sessions are health-checked: each `musicsource.Provider` exposes `VerifySession` (one cheap authenticated call), `app.Service.CheckConnections` caches the per-service result, and `internal/bootstrap` runs it on a background timer. A rejected or unrefreshable session is kept but reported with `needsReauth`, which the UI surfaces as a reconnect banner rather than silently dropping the connection.
- `internal/storage` owns the SQLite schema and transactions. Playlist edits append immutable revisions.
- `internal/credentials` owns environment, keyring, and explicitly authorized config-file credential precedence.
- `internal/logging` owns Zap construction and mandatory secret redaction.
- `web/src/api.ts` owns the typed Wails adapter and job-polling behavior consumed by React.
- `web/src/ApiKeyHelpDialog.tsx` owns accessible API-account onboarding, official Platform links, focus trapping, and focus restoration.

Dependencies point inward: adapters depend on `internal/playlist`, while the domain package does not import presentation or provider adapters. Provider SDK types and generated Wails binding details do not cross into the domain.

## Main request flows

### Generate or refine a playlist

1. React calls the typed Wails adapter.
2. `internal/desktop.API` delegates to `app.Service` using domain request types.
3. The service creates an in-memory job and returns immediately.
4. React polls the job through the Wails binding. A modal status appears only after three seconds.
5. A one-slot application gate serializes paid work, and cancellation propagates through `context.Context`.
6. `internal/openaiapi` reads the credential, calls the fixed Responses API endpoint with response storage disabled, enables web search, and requires strict JSON-schema output.
7. Domain validation enforces count, required metadata, ordering, and duplicate title/artist keys.
8. `internal/storage` commits a new immutable revision and advances the aggregate's active-revision pointer in one transaction.
9. The completed job contains the playlist ID, and React reloads the saved representation.

Track replacement follows the same asynchronous path. Track removal is local and synchronous but still appends a revision.

### Create a Soundiiz handoff

1. The user requests a generic Soundiiz handoff from the playlist preview.
2. The backend sends only the accepted playlist title, description, track titles, and artists to Soundiiz.
3. Redirects are disabled. The returned URL must use HTTPS, the exact `soundiiz.com` host, and the documented import path.
4. The temporary link and expiry are saved locally.
5. `internal/desktop.API` validates the URL again before asking Wails to open it in the system browser.

### Save an OpenAI API key

1. A non-empty `OPENAI_API_KEY` wins over persisted credentials.
2. Environment-managed credentials are reported only as configured/read-only status and cannot be replaced or deleted in the UI.
3. The submitted key is validated for model access before storage.
4. The platform credential store is attempted first.
5. A synchronized, atomically replaced config file is used only when keyring storage fails and the user explicitly opts into that fallback.
6. Status values never expose the key.

## Persistence model

SQLite stores four related concepts:

- `playlists` is the aggregate root and points to the active revision.
- `revisions` contains immutable title, prompt, model, effort, usage, and timestamp snapshots.
- `tracks` stores ordered recording candidates scoped to a revision.
- `revision_references` records which previous playlists influenced generation.

Usage is stored as versioned JSON because provider counters can evolve independently from the relational playlist model. Times are UTC RFC 3339 values. Writes that create or activate revisions use SQL transactions.

The schema is embedded with `go:embed` and is idempotent for a new database. Additive column changes append an `ALTER TABLE ADD COLUMN` to the `migrations` slice in `sqlite.go`, which tolerates the duplicate-column error on an already-current database; a future incompatible change must introduce an explicit versioned migration rather than changing the meaning of an existing column.

The `Track` shape is the one contract mirrored in four places — `internal/playlist/model.go`, the `tracks` table in `internal/storage`, the generation JSON schema in `internal/openaiapi`, and `web/src/types.ts` — and all four move together. `Track.ISRC` is nullable: imported playlists carry the authoritative code, the model supplies it opportunistically, and generated tracks usually leave it null.

## Desktop security boundary

The process assumes the local operating-system account is trusted. The Wails frontend does not expose a listening network service.

- The frontend receives only the methods explicitly bound from `internal/desktop.API`.
- The external URL method accepts only the exact HTTPS Soundiiz import origin and path.
- Fixed provider URLs, disabled Soundiiz redirects, and returned-URL validation constrain outbound trust.
- OpenAI-key-shaped text is redacted from Zap messages, strings, errors, and reflected values.
- Prompts and tracklists are debug-only logs; production defaults to info level.
- React renders user and provider text without raw HTML.

Adding remote access or another presentation transport changes the threat model and requires a new authentication, authorization, session, transport-security, and credential-isolation design.

## Cross-platform release pipeline

Native desktop builds run on a runner for each target operating system, and every OS ships both x86-64 and ARM64:

| Operating system | Desktop artifact |
| --- | --- |
| Windows | `amd64` and `arm64` executables, each zipped (cross-compiled on one x64 runner; WebView2 is pure Go) |
| macOS | One universal application ZIP for Intel and Apple Silicon (`lipo`) |
| Linux | `amd64` and `arm64` deb, rpm, and AppImage packages, each built on a native runner (`ubuntu-latest` / `ubuntu-24.04-arm`) |

The Linux build links WebKitGTK 4.1 through the `gtk3` build tag (Wails v3 otherwise defaults to GTK4 / WebKitGTK 6.0) and requires the matching architecture's GTK/WebKit development libraries. `scripts/package-linux.sh` maps `GOARCH` to the deb/rpm/AppImage architecture names, and uses pinned nFPM and per-architecture AppImage tooling to package the desktop entry, application icon, AppStream metadata, and distribution-specific GTK/WebKit dependencies.

Pushing a `v*` tag runs a fresh quality gate and all three native builds. The publish job downloads only desktop artifacts, writes `SHA256SUMS.txt`, and creates or updates the GitHub Release.

## Quality gates

Run either platform-equivalent test entrypoint:

```text
bash scripts/test.sh
pwsh -File scripts/test.ps1
```

The scripts check formatting, ESLint, TypeScript, frontend tests and coverage, Go Vet, Go tests, supported-host race detection, and at least 95% aggregate coverage across business and external-API boundary packages.

Build the desktop application with:

```text
bash scripts/build-desktop.sh
pwsh -File scripts/build-desktop.ps1
```

Use `--skip-tests` or `-SkipTests` only when the corresponding test script has already succeeded for the same source state. The build scripts write artifacts under `build/bin`; Linux `deb`/`rpm`/AppImage packaging is included by `scripts/package-linux.sh`. Windows builds produce amd64 and arm64 NSIS installers by default; `-SkipInstaller` builds only the portable executables.
