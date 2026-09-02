/**
 * The whole interface lives in this file. It is intentionally one module: the
 * app is a handful of views over a shared shell, and there is no routing,
 * data-fetching, or state library to spread across files.
 *
 * Structure, top to bottom:
 *   - constants and small pure helpers (BrandMark, icons, `coverStyle`,
 *     `parseRoute`, cluster/badge helpers for the streaming views);
 *   - `App`: the shell. Holds the only shared state (config, history,
 *     connections, inspiration seed, current job, error banner, theme), owns a
 *     tiny History-API router, and exposes `run()` — the single lifecycle every
 *     paid operation goes through so the busy overlay, refresh, and error
 *     handling never drift between views;
 *   - the page components (`CreatePage`, `BrowsePage`, `HistoryPage`,
 *     `SettingsPage`, `PlaylistPage`) and the row-level pieces they use.
 *
 * All backend access is the typed adapter in `./api`; jobs are polled through
 * `waitForJob`. Split a page into its own file once it grows independent state
 * or is reused elsewhere.
 */
import {
  Component,
  CSSProperties,
  FormEvent,
  ReactNode,
  useCallback,
  useEffect,
  useState,
} from "react";
import { api, JobError, waitForJob } from "./api";
import { ApiKeyHelpDialog } from "./ApiKeyHelpDialog";
import { BusyOverlay } from "./BusyOverlay";
import type {
  Config,
  ConnectionStatus,
  Effort,
  Job,
  Playlist,
  Track,
} from "./types";

type Route =
  | { page: "home" | "history" | "settings" | "browse" }
  | { page: "playlist"; id: string };

type Theme = "light" | "dark";

// Streaming services offered for import, in display order. The label is what the
// UI shows; the kind is what the backend expects.
const SERVICES: { kind: string; label: string }[] = [
  { kind: "tidal", label: "TIDAL" },
  { kind: "qobuz", label: "Qobuz" },
];
const serviceLabel = (kind: string) =>
  SERVICES.find((s) => s.kind === kind)?.label ?? kind;

// The History and Browse views group playlists so a record shows once, in one
// place: things forged here, then each streaming service. A playlist with more
// than one home (forged here and also on TIDAL, say) lands in the first that
// applies and carries a badge for every source.
type ClusterKey = "created" | "tidal" | "qobuz";
const CLUSTERS: { key: ClusterKey; label: string }[] = [
  { key: "created", label: "Forged here" },
  { key: "tidal", label: "TIDAL" },
  { key: "qobuz", label: "Qobuz" },
];

function clusterOf(item: Playlist): ClusterKey {
  if (item.origin === "generated") return "created";
  return item.sources?.[0]?.kind === "qobuz" ? "qobuz" : "tidal";
}

function groupByCluster(history: Playlist[]): Record<ClusterKey, Playlist[]> {
  const groups: Record<ClusterKey, Playlist[]> = {
    created: [],
    tidal: [],
    qobuz: [],
  };
  for (const item of history) groups[clusterOf(item)].push(item);
  return groups;
}

// Every place a playlist lives, as short chips: "Forged here" for a generated
// origin plus one per linked streaming service.
function playlistBadges(item: Playlist): string[] {
  const badges: string[] = [];
  if (item.origin === "generated") badges.push("Forged here");
  for (const source of item.sources ?? [])
    badges.push(serviceLabel(source.kind));
  return badges;
}

function SourceBadges({ item }: { item: Playlist }) {
  const badges = playlistBadges(item);
  if (badges.length === 0) return null;
  return (
    <span className="source-badges">
      {badges.map((badge) => (
        <span className="source-badge" key={badge}>
          {badge}
        </span>
      ))}
    </span>
  );
}

// The tracklist of a not-yet-hydrated import can be absent; never dereference it
// directly.
const trackList = (item: Playlist): Track[] =>
  item.currentRevision?.tracks ?? [];

// An Error with an empty message must never reach the banner as a blank alert.
const errorText = (reason: unknown, fallback: string): string =>
  reason instanceof Error && reason.message.trim() !== ""
    ? reason.message
    : fallback;

/**
 * Stops a render error in one view from blanking the whole app. React needs a
 * class component for this; it is the only one in the file.
 */
class ErrorBoundary extends Component<
  { children: ReactNode },
  { message: string | null }
> {
  state = { message: null as string | null };

  static getDerivedStateFromError(error: unknown) {
    return {
      message: error instanceof Error ? error.message : "Something went wrong",
    };
  }

  render() {
    if (this.state.message === null) return this.props.children;
    return (
      <section className="page">
        <p className="eyebrow">Something broke</p>
        <h1>This view failed to render</h1>
        <p className="lede compact">{this.state.message}</p>
        <div className="button-row">
          <button
            className="button primary"
            onClick={() => window.location.reload()}
          >
            Reload
          </button>
          <button
            className="button secondary"
            onClick={() => {
              window.history.pushState({}, "", "/");
              window.location.reload();
            }}
          >
            Back to Create
          </button>
        </div>
      </section>
    );
  }
}

type ErrorNotice = { message: string; code?: string };

const OPENAI_BILLING_URL =
  "https://platform.openai.com/settings/organization/billing/overview";
const OPENAI_BILLING_CODES = new Set([
  "credit_balance_exhausted",
  "project_spend_limit_exceeded",
  "organization_spend_limit_exceeded",
  "organization_usage_limit_exceeded",
]);

// The hero ticker is decorative; the words only need to evoke the range of
// taste the curator can work with.
const GENRES = [
  "Spiritual jazz",
  "Dub techno",
  "Ambient",
  "Post-punk",
  "Neo-soul",
  "Krautrock",
  "Bossa nova",
  "Shoegaze",
  "Boom bap",
  "Highlife",
  "Balearic",
  "Fourth world",
  "Slowcore",
  "Cosmic disco",
  "Field recordings",
];

function readTheme(): Theme {
  try {
    const stored = localStorage.getItem("pf-theme");
    if (stored === "light" || stored === "dark") return stored;
    if (window.matchMedia?.("(prefers-color-scheme: light)").matches)
      return "light";
  } catch {
    // Private-mode storage or a missing matchMedia both fall back to dark,
    // which is the design's primary look.
  }
  return "dark";
}

// A small record with a sparkle, echoing the app icon. Kept deliberately plain
// so it stays legible at ~34px: solid disc in the accent colour, spindle hole
// punched to the page colour, a four-point twinkle and a speck off to one side.
function BrandMark() {
  return (
    <span className="brand-mark" aria-hidden="true">
      <svg viewBox="0 0 32 32" role="img" aria-label="Playlist Forge">
        <circle cx="14" cy="18" r="11.5" fill="currentColor" />
        <circle
          cx="14"
          cy="18"
          r="4.6"
          fill="none"
          stroke="var(--bg)"
          strokeWidth="1"
        />
        <circle cx="14" cy="18" r="1.7" fill="var(--bg)" />
        <path
          d="M25.5 1.5 C26.3 5.9 26.3 5.9 30.5 6.7 C26.3 7.5 26.3 7.5 25.5 11.9 C24.7 7.5 24.7 7.5 20.5 6.7 C24.7 5.9 24.7 5.9 25.5 1.5 Z"
          fill="currentColor"
        />
        <circle cx="29.4" cy="12.6" r="1" fill="currentColor" />
      </svg>
    </span>
  );
}

function SunIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="4" />
      <path
        strokeLinecap="round"
        d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"
      />
    </svg>
  );
}

function MoonIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"
      />
    </svg>
  );
}

// A deterministic sleeve colour so a track keeps the same artwork across
// renders without storing anything.
function coverStyle(seed: string): CSSProperties {
  let hash = 0;
  for (let i = 0; i < seed.length; i += 1) {
    hash = (hash * 31 + seed.charCodeAt(i)) >>> 0;
  }
  const hue = hash % 360;
  const partner = (hue + 35 + (hash % 90)) % 360;
  return {
    backgroundImage: `linear-gradient(140deg, hsl(${hue} 58% 46%), hsl(${partner} 62% 28%))`,
  };
}

function parseRoute(): Route {
  // A tiny History API router is sufficient for the embedded views and avoids
  // adding a routing dependency to the runtime bundle.
  const match = window.location.pathname.match(/^\/playlists\/([^/]+)$/);
  if (match) return { page: "playlist", id: decodeURIComponent(match[1]) };
  if (window.location.pathname === "/history") return { page: "history" };
  if (window.location.pathname === "/settings") return { page: "settings" };
  if (window.location.pathname === "/browse") return { page: "browse" };
  return { page: "home" };
}

/**
 * The application shell: top bar, error banner, the routed view, footer, and
 * the busy overlay. Owns every piece of cross-view state and passes `navigate`
 * and `run` down to the pages.
 */
export default function App() {
  const [route, setRoute] = useState<Route>(parseRoute);
  const [config, setConfig] = useState<Config | null>(null);
  const [history, setHistory] = useState<Playlist[]>([]);
  const [connections, setConnections] = useState<ConnectionStatus[]>([]);
  const [inspirationSeed, setInspirationSeed] = useState<string[]>([]);
  const [job, setJob] = useState<Job | null>(null);
  // A sync should surface its progress bar at once; a generation waits out the
  // delay so a quick failure doesn't flash the overlay.
  const [jobImmediate, setJobImmediate] = useState(false);
  const [error, setError] = useState<ErrorNotice | null>(null);
  const [theme, setTheme] = useState<Theme>(readTheme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem("pf-theme", theme);
    } catch {
      // Persisting the preference is best-effort.
    }
  }, [theme]);

  const refresh = useCallback(async () => {
    const [nextConfig, nextHistory, nextConnections] = await Promise.all([
      api.config(),
      api.playlists(),
      api.connections(),
    ]);
    setConfig(nextConfig);
    setHistory(nextHistory);
    setConnections(nextConnections);
  }, []);

  useEffect(() => {
    // Initial data arrives asynchronously; the callback performs the state sync.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    refresh().catch((reason: unknown) =>
      setError({ message: errorText(reason, "Could not load the app") }),
    );
    const listener = () => setRoute(parseRoute());
    window.addEventListener("popstate", listener);
    return () => window.removeEventListener("popstate", listener);
  }, [refresh]);

  function navigate(path: string) {
    window.history.pushState({}, "", path);
    setRoute(parseRoute());
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  const reportError = useCallback((message: string) => {
    setError({ message });
  }, []);

  // Reload one streaming service. The backend runs it as a cancellable job with
  // progress, so it goes through the same `run` lifecycle as a generation: the
  // busy overlay shows the progress bar, then history refreshes.
  const syncSource = (kind: string) => {
    run(() => api.syncSource(kind), undefined, { immediate: true });
  };

  // Carry a Browse selection back to the composer's reference picker.
  const applyInspiration = useCallback((ids: string[]) => {
    setInspirationSeed(ids);
    window.history.pushState({}, "", "/");
    setRoute(parseRoute());
    window.scrollTo({ top: 0, behavior: "smooth" });
  }, []);

  /**
   * The shared lifecycle for every paid operation. `operation` starts a job;
   * `run` polls it to completion (feeding the delayed busy overlay), refreshes
   * config and history, then invokes `after` with the finished job. Failures —
   * including a structured `JobError` carrying a billing code — land in the
   * error banner. Kept in one place so no page reimplements it.
   */
  async function run(
    operation: () => Promise<Job>,
    after?: (done: Job) => Promise<void> | void,
    options?: { immediate?: boolean },
  ) {
    setError(null);
    setJobImmediate(options?.immediate ?? false);
    try {
      const initial = await operation();
      const done = await waitForJob(initial, setJob);
      setJob(null);
      await refresh();
      await after?.(done);
    } catch (reason) {
      setJob(null);
      const message =
        reason instanceof Error && reason.message.trim() !== ""
          ? reason.message
          : "Something went wrong";
      setError({
        message,
        code: reason instanceof JobError ? reason.code : undefined,
      });
    }
  }

  async function cancel() {
    if (job) await api.cancelJob(job.id).catch(() => undefined);
  }

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <button
          className="brand"
          onClick={() => navigate("/")}
          aria-label="Playlist Forge home"
        >
          <BrandMark />
          <span>Playlist Forge</span>
        </button>
        <div className="topbar-right">
          <nav aria-label="Main navigation">
            <button
              onClick={() => navigate("/")}
              aria-current={route.page === "home" ? "page" : undefined}
            >
              Create
            </button>
            <button
              onClick={() => navigate("/browse")}
              aria-current={route.page === "browse" ? "page" : undefined}
            >
              Browse
            </button>
            <button
              onClick={() => navigate("/history")}
              aria-current={route.page === "history" ? "page" : undefined}
            >
              History
            </button>
            <button
              onClick={() => navigate("/settings")}
              aria-current={route.page === "settings" ? "page" : undefined}
            >
              Settings
            </button>
          </nav>
          <button
            className="theme-toggle"
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
            aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
          >
            {theme === "dark" ? <SunIcon /> : <MoonIcon />}
          </button>
        </div>
      </header>
      {error && error.message.trim() !== "" && (
        <div className="error-banner" role="alert">
          <div className="error-banner-content">
            <span>{error.message}</span>
            {error.code && OPENAI_BILLING_CODES.has(error.code) && (
              <button
                className="error-banner-action"
                onClick={() =>
                  api
                    .openExternalURL(OPENAI_BILLING_URL)
                    .catch((reason: unknown) =>
                      reportError(
                        errorText(reason, "Could not open the billing page"),
                      ),
                    )
                }
              >
                Review OpenAI billing <span aria-hidden="true">↗</span>
              </button>
            )}
          </div>
          <button
            className="error-banner-dismiss"
            onClick={() => setError(null)}
            aria-label="Dismiss error"
          >
            ×
          </button>
        </div>
      )}
      <main id="main">
        <ErrorBoundary key={route.page}>
          {route.page === "home" && (
            <CreatePage
              config={config}
              history={history}
              inspirationSeed={inspirationSeed}
              navigate={navigate}
              run={run}
            />
          )}
          {route.page === "browse" && (
            <BrowsePage
              history={history}
              connections={connections}
              syncSource={syncSource}
              onUseAsInspiration={applyInspiration}
              navigate={navigate}
            />
          )}
          {route.page === "history" && (
            <HistoryPage
              history={history}
              connections={connections}
              syncSource={syncSource}
              navigate={navigate}
            />
          )}
          {route.page === "settings" && (
            <SettingsPage
              config={config}
              connections={connections}
              refresh={refresh}
              setError={reportError}
            />
          )}
          {route.page === "playlist" && (
            <PlaylistPage
              id={route.id}
              run={run}
              navigate={navigate}
              setError={reportError}
            />
          )}
        </ErrorBoundary>
      </main>
      <footer>
        <span>
          Local-first. Your API key never enters the playlist database.
        </span>
        <span>Soundiiz completes transfers on its own site.</span>
        <span>© 2026 Paul Pietkiewicz · MIT License</span>
      </footer>
      <BusyOverlay job={job} immediate={jobImmediate} onCancel={cancel} />
    </div>
  );
}

/**
 * The landing view: an editorial hero and the brief composer. Collects the
 * prompt, track count, reasoning effort, and optional reference playlists, then
 * hands generation to `run` and navigates to the new playlist when it lands.
 * Disabled until a key is configured and the prompt is long enough.
 */
function CreatePage({
  config,
  history,
  inspirationSeed,
  navigate,
  run,
}: {
  config: Config | null;
  history: Playlist[];
  inspirationSeed: string[];
  navigate: (path: string) => void;
  run: (op: () => Promise<Job>, after?: (job: Job) => void) => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [trackCount, setTrackCount] = useState(30);
  const [effort, setEffort] = useState<Effort>("medium");
  // Seeded from a Browse selection; the picker below can still add or remove.
  const [references, setReferences] = useState<string[]>(inspirationSeed);

  const titleOf = (id: string) =>
    history.find((item) => item.id === id)?.currentRevision.title ?? "Playlist";
  const toggleReference = (id: string, on: boolean) =>
    setReferences((current) =>
      on ? [...current, id] : current.filter((ref) => ref !== id),
    );

  function submit(event: FormEvent) {
    event.preventDefault();
    run(
      () =>
        api.generate({ prompt, trackCount, effort, referenceIds: references }),
      (done) => navigate(`/playlists/${done.playlistId}`),
    );
  }

  return (
    <section className="create-layout">
      <div className="hero-copy">
        <p className="eyebrow">Taste, translated</p>
        <h1>
          Describe the feeling.
          <br />
          <em>We’ll find the music.</em>
        </h1>
        <p className="lede">
          An AI curator researches real recordings, shapes the arc, and hands
          your finished playlist to Soundiiz for TIDAL, Qobuz, Spotify, or Apple
          Music.
        </p>
        <div className="genre-ticker" aria-hidden="true">
          <div className="genre-ticker-track">
            {[...GENRES, ...GENRES].map((genre, index) => (
              <span key={`${genre}-${index}`}>{genre}</span>
            ))}
          </div>
        </div>
      </div>
      <form className="composer card" onSubmit={submit}>
        {!config?.credential.configured && (
          <div className="notice">
            <strong>Add an OpenAI API key first.</strong>
            <button type="button" onClick={() => navigate("/settings")}>
              Open settings
            </button>
          </div>
        )}
        <label htmlFor="playlist-prompt">
          What should this playlist feel like?
        </label>
        <textarea
          id="playlist-prompt"
          required
          minLength={3}
          maxLength={4000}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder="Late-night electronic soul for a rainy drive—warm bass, patient build, no festival drops…"
        />
        <div className="field-row">
          <label>
            Tracks
            <select
              value={trackCount}
              onChange={(e) => setTrackCount(Number(e.target.value))}
            >
              {(config?.trackCounts || [20, 30, 40, 50, 60, 100]).map(
                (count) => (
                  <option key={count}>{count}</option>
                ),
              )}
            </select>
          </label>
          <label>
            Reasoning
            <select
              value={effort}
              onChange={(e) => setEffort(e.target.value as Effort)}
            >
              <option value="medium">Medium · recommended</option>
              <option value="high">High</option>
              <option value="xhigh">Extra high</option>
              <option value="max">Maximum</option>
            </select>
          </label>
        </div>
        {history.length > 0 && (
          <fieldset className="references">
            <legend>
              Use existing playlists as inspiration <span>(optional)</span>
            </legend>
            {history.slice(0, 8).map((item) => (
              <label key={item.id}>
                <input
                  type="checkbox"
                  checked={references.includes(item.id)}
                  onChange={(e) => toggleReference(item.id, e.target.checked)}
                />
                <span>{item.currentRevision.title}</span>
                <SourceBadges item={item} />
              </label>
            ))}
            {references
              .filter(
                (id) => !history.slice(0, 8).some((item) => item.id === id),
              )
              .map((id) => (
                <span className="reference-chip" key={id}>
                  {titleOf(id)}
                  <button
                    type="button"
                    aria-label={`Remove ${titleOf(id)}`}
                    onClick={() => toggleReference(id, false)}
                  >
                    ×
                  </button>
                </span>
              ))}
            <button
              type="button"
              className="browse-link"
              onClick={() => navigate("/browse")}
            >
              Browse all playlists <span aria-hidden="true">→</span>
            </button>
          </fieldset>
        )}
        <button
          className="button primary"
          type="submit"
          disabled={!config?.credential.configured || prompt.trim().length < 3}
        >
          Forge playlist <span aria-hidden="true">→</span>
        </button>
        <p className="form-note">
          Uses {config?.model || "gpt-5.6-sol"} and OpenAI web search. API
          charges apply.
        </p>
      </form>
    </section>
  );
}

/**
 * The library grid. Each card links to a playlist; an empty state points back
 * to Create. Reads `history` from the shell — it is refreshed after every
 * operation.
 */
function HistoryPage({
  history,
  connections,
  syncSource,
  navigate,
}: {
  history: Playlist[];
  connections: ConnectionStatus[];
  syncSource: (kind: string) => void;
  navigate: (path: string) => void;
}) {
  const groups = groupByCluster(history);
  return (
    <section className="page">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Your library</p>
          <h1>Playlist history</h1>
          <p className="lede compact">
            Everything you can draw on: playlists forged here and read-only
            mirrors of what already lives on your streaming services.
          </p>
        </div>
        <ReloadControls connections={connections} syncSource={syncSource} />
      </div>
      {history.length === 0 ? (
        <div className="empty card">
          <h2>No playlists yet</h2>
          <p>
            Forge one from a brief, or connect a streaming service in Settings
            to mirror the playlists you already have.
          </p>
          <button className="button primary" onClick={() => navigate("/")}>
            Create one
          </button>
        </div>
      ) : (
        CLUSTERS.filter((cluster) => groups[cluster.key].length > 0).map(
          (cluster) => (
            <div className="cluster" key={cluster.key}>
              <h2 className="cluster-heading">
                {cluster.label}
                <span>{groups[cluster.key].length}</span>
              </h2>
              <div className="history-grid">
                {groups[cluster.key].map((item) => (
                  <button
                    className="history-card card"
                    key={item.id}
                    onClick={() => navigate(`/playlists/${item.id}`)}
                  >
                    <span className="history-count">
                      {trackList(item).length} tracks
                    </span>
                    <h3>{item.currentRevision.title}</h3>
                    <p>{item.currentRevision.description}</p>
                    <SourceBadges item={item} />
                    <span>
                      Revision {item.revisionCount} ·{" "}
                      {new Date(item.updatedAt).toLocaleDateString()}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          ),
        )
      )}
    </section>
  );
}

/**
 * The per-service Reload buttons shared by History and Browse. Only connected
 * services appear; the sync runs as a background job, so progress and
 * cancellation live in the shared busy overlay rather than here.
 */
function ReloadControls({
  connections,
  syncSource,
}: {
  connections: ConnectionStatus[];
  syncSource: (kind: string) => void;
}) {
  const connected = connections.filter((c) => c.connected);
  if (connected.length === 0) return null;
  return (
    <div className="reload-controls">
      {connected.map((c) => (
        <button
          key={c.kind}
          className="button secondary"
          onClick={() => syncSource(c.kind)}
        >
          Reload {serviceLabel(c.kind)}
        </button>
      ))}
    </div>
  );
}

/**
 * The clustered picker. Every playlist appears once — under "Forged here" or the
 * first service it is linked to — with a checkbox and a badge per source. Each
 * connected service gets a Reload; the sticky footer carries the current
 * selection into the composer's reference list.
 */
function BrowsePage({
  history,
  connections,
  syncSource,
  onUseAsInspiration,
  navigate,
}: {
  history: Playlist[];
  connections: ConnectionStatus[];
  syncSource: (kind: string) => void;
  onUseAsInspiration: (ids: string[]) => void;
  navigate: (path: string) => void;
}) {
  const [selected, setSelected] = useState<string[]>([]);
  const groups = groupByCluster(history);
  const toggle = (id: string) =>
    setSelected((current) =>
      current.includes(id) ? current.filter((x) => x !== id) : [...current, id],
    );

  return (
    <section className="page browse-page">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Pick your references</p>
          <h1>Browse all playlists</h1>
          <p className="lede compact">
            Choose any mix of playlists — forged here or mirrored from a service
            — to seed the composer's inspiration list.
          </p>
        </div>
        <ReloadControls connections={connections} syncSource={syncSource} />
      </div>

      {history.length === 0 ? (
        <div className="empty card">
          <h2>Nothing to browse yet</h2>
          <p>
            Forge a playlist, or connect TIDAL or Qobuz in Settings to mirror
            the ones you already have.
          </p>
          <button className="button primary" onClick={() => navigate("/")}>
            Back to Create
          </button>
        </div>
      ) : (
        <>
          <div className="browse-list">
            {CLUSTERS.filter((cluster) => groups[cluster.key].length > 0).map(
              (cluster) => (
                <div className="cluster" key={cluster.key}>
                  <h2 className="cluster-heading">
                    {cluster.label}
                    <span>{groups[cluster.key].length}</span>
                  </h2>
                  <ul className="browse-cluster">
                    {groups[cluster.key].map((item) => (
                      <li key={item.id}>
                        <label className="browse-row">
                          <input
                            type="checkbox"
                            checked={selected.includes(item.id)}
                            onChange={() => toggle(item.id)}
                          />
                          <span className="browse-row-main">
                            <span className="browse-row-title">
                              {item.currentRevision.title}
                            </span>
                            <span className="browse-row-meta">
                              {trackList(item).length} tracks · updated{" "}
                              {new Date(item.updatedAt).toLocaleDateString()}
                            </span>
                          </span>
                          <SourceBadges item={item} />
                        </label>
                      </li>
                    ))}
                  </ul>
                </div>
              ),
            )}
          </div>
          <div className="browse-footer">
            <span>{selected.length} selected</span>
            <div>
              <button
                className="button ghost"
                type="button"
                onClick={() => navigate("/")}
              >
                Cancel
              </button>
              <button
                className="button primary"
                type="button"
                disabled={selected.length === 0}
                onClick={() => onUseAsInspiration(selected)}
              >
                Use as inspiration ({selected.length})
              </button>
            </div>
          </div>
        </>
      )}
    </section>
  );
}

/**
 * Credential management. Saves or removes the OpenAI key (validation and
 * storage happen in Go), explains the storage precedence, and shows the
 * current status. When the key comes from OPENAI_API_KEY the form is replaced
 * with a read-only notice. Hosts the API-key help dialog.
 */
function SettingsPage({
  config,
  connections,
  refresh,
  setError,
}: {
  config: Config | null;
  connections: ConnectionStatus[];
  refresh: () => Promise<void>;
  setError: (message: string) => void;
}) {
  const [key, setKey] = useState("");
  const [fallback, setFallback] = useState(false);
  const [saved, setSaved] = useState("");
  const [saving, setSaving] = useState(false);
  const [showKeyHelp, setShowKeyHelp] = useState(false);
  async function save(event: FormEvent) {
    event.preventDefault();
    setError("");
    setSaved("");
    setSaving(true);
    try {
      const status = await api.saveKey(key, fallback);
      setKey("");
      setSaved(
        `Key saved in ${status.storage === "keyring" ? "your OS credential store" : "a restricted local config file"}.`,
      );
      await refresh();
    } catch (reason) {
      setError(errorText(reason, "Could not save key"));
    } finally {
      setSaving(false);
    }
  }
  async function remove() {
    setError("");
    try {
      await api.deleteKey();
      setSaved("Key removed.");
      await refresh();
    } catch (reason) {
      setError(errorText(reason, "Could not remove key"));
    }
  }
  return (
    <section className="page settings">
      <p className="eyebrow">Private by default</p>
      <h1>OpenAI API key</h1>
      <div className="settings-grid">
        <form className="card" onSubmit={save}>
          <div className="label-row">
            {config?.credential.readOnly ? (
              <span>API key</span>
            ) : (
              <label htmlFor="api-key">API key</label>
            )}
            <button
              className="field-help"
              type="button"
              aria-haspopup="dialog"
              onClick={(event) => {
                // Preserve a deterministic return target for keyboard users
                // and test environments where synthetic clicks do not focus.
                event.currentTarget.focus();
                setShowKeyHelp(true);
              }}
            >
              How do I get an API key?
            </button>
          </div>
          {config?.credential.readOnly ? (
            <div className="notice" role="status">
              <strong>Managed by the environment.</strong>
              <span>
                Restart the application with a different OPENAI_API_KEY value to
                change this credential.
              </span>
            </div>
          ) : (
            <>
              <input
                id="api-key"
                type="password"
                autoComplete="off"
                required
                maxLength={512}
                value={key}
                onChange={(e) => setKey(e.target.value)}
                placeholder="sk-…"
              />
              <label className="check-line">
                <input
                  type="checkbox"
                  checked={fallback}
                  onChange={(e) => setFallback(e.target.checked)}
                />
                Allow a restricted config-file fallback if the OS credential
                store is unavailable
              </label>
              <div className="button-row">
                <button
                  className="button primary"
                  type="submit"
                  disabled={saving}
                >
                  {saving ? "Validating…" : "Save key"}
                </button>
                {config?.credential.configured && (
                  <button
                    className="button danger"
                    type="button"
                    onClick={remove}
                  >
                    Remove saved key
                  </button>
                )}
              </div>
              {saving && (
                <p role="status">
                  Validating model access and saving securely…
                </p>
              )}
            </>
          )}
          {saved && (
            <p className="success" role="status">
              {saved}
            </p>
          )}
        </form>
        <aside className="card">
          <h2>How storage works</h2>
          <p>
            OPENAI_API_KEY takes precedence when present. Otherwise, the app
            uses Windows Credential Manager, macOS Keychain, or the Linux Secret
            Service before its permission-restricted config-file fallback.
          </p>
          <dl>
            <dt>Status</dt>
            <dd>
              {config?.credential.configured ? "Configured" : "Not configured"}
            </dd>
            <dt>Storage</dt>
            <dd>{config?.credential.storage || "none"}</dd>
          </dl>
        </aside>
      </div>
      <h1 className="settings-section">Streaming connections</h1>
      <p className="lede compact">
        Sign in to mirror the playlists you already have. Imported playlists are
        read-only snapshots — inspiration and a Soundiiz handoff source, never
        overwritten here. Credentials live only in your OS credential store.
      </p>
      <div className="connections-list card">
        {SERVICES.map(({ kind }) => {
          const status = connections.find((c) => c.kind === kind);
          return (
            <ConnectionRow
              key={kind}
              kind={kind}
              status={status}
              refresh={refresh}
              setError={setError}
            />
          );
        })}
      </div>
      <ApiKeyHelpDialog
        open={showKeyHelp}
        onClose={() => setShowKeyHelp(false)}
      />
    </section>
  );
}

/**
 * One streaming service in Settings: its connection state and a Connect or
 * Disconnect button. Connect opens the embedded sign-in window in Go; when the
 * desktop build has no adapter for the service the row is shown as unavailable.
 */
function ConnectionRow({
  kind,
  status,
  refresh,
  setError,
}: {
  kind: string;
  status: ConnectionStatus | undefined;
  refresh: () => Promise<void>;
  setError: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const available = status?.available ?? false;
  const connected = status?.connected ?? false;

  async function connect() {
    setError("");
    setBusy(true);
    try {
      await api.connectService(kind);
      await refresh();
    } catch (reason) {
      setError(errorText(reason, `Could not connect ${serviceLabel(kind)}`));
    } finally {
      setBusy(false);
    }
  }

  async function disconnect() {
    setError("");
    setBusy(true);
    try {
      await api.disconnectService(kind);
      await refresh();
    } catch (reason) {
      setError(errorText(reason, `Could not disconnect ${serviceLabel(kind)}`));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="connection-row">
      <div className="connection-id">
        <strong>{serviceLabel(kind)}</strong>
        <span className={`connection-state${connected ? " on" : ""}`}>
          {!available
            ? "Not available in this build"
            : connected
              ? `Connected${status?.displayName ? ` · ${status.displayName}` : ""}`
              : "Not connected"}
        </span>
      </div>
      {available &&
        (connected ? (
          <button
            className="button danger"
            type="button"
            disabled={busy}
            onClick={disconnect}
          >
            {busy ? "Working…" : "Disconnect"}
          </button>
        ) : (
          <button
            className="button secondary"
            type="button"
            disabled={busy}
            onClick={connect}
          >
            {busy ? "Waiting for sign-in…" : "Connect"}
          </button>
        ))}
    </div>
  );
}

/**
 * The playlist preview and workspace. Loads the playlist by id and shows its
 * current revision: the tracklist (with per-track remove and replace), a
 * full-playlist refinement form, and the Soundiiz handoff. Remove applies
 * immediately; replace, refine, and handoff go through `run`.
 */
function PlaylistPage({
  id,
  run,
  navigate,
  setError,
}: {
  id: string;
  run: (
    op: () => Promise<Job>,
    after?: (job: Job) => Promise<void> | void,
  ) => void;
  navigate: (path: string) => void;
  setError: (message: string) => void;
}) {
  const [item, setItem] = useState<Playlist | null>(null);
  const [refine, setRefine] = useState("");
  const [effort, setEffort] = useState<Effort>("medium");
  const load = useCallback(() => api.playlist(id).then(setItem), [id]);
  useEffect(() => {
    load().catch((reason: unknown) =>
      setError(errorText(reason, "Could not load the playlist")),
    );
  }, [load, setError]);
  if (!item)
    return (
      <section className="page">
        <p role="status">Loading playlist…</p>
      </section>
    );
  const revision = item.currentRevision;
  function submitRefine(event: FormEvent) {
    event.preventDefault();
    run(
      () => api.refine(id, refine, effort),
      async () => {
        setRefine("");
        await load();
      },
    );
  }
  function transfer() {
    run(
      () => api.soundiiz(id),
      async () => {
        const fresh = await api.playlist(id);
        setItem(fresh);
        if (fresh.soundiizUrl) await api.openExternalURL(fresh.soundiizUrl);
      },
    );
  }
  async function unlink(kind: string, externalId: string) {
    setError("");
    try {
      setItem(await api.unlinkSource(id, kind, externalId));
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : `Could not unlink ${serviceLabel(kind)}`,
      );
    }
  }
  const readOnly = item.origin === "imported";
  return (
    <section className="page playlist-page">
      <button className="back-link" onClick={() => navigate("/history")}>
        ← History
      </button>
      <div className="playlist-heading">
        <div>
          <p className="eyebrow">
            {readOnly
              ? "Imported · read-only snapshot"
              : `Preview · revision ${item.revisionCount}`}
          </p>
          <h1>{revision.title}</h1>
          <p className="lede compact">{revision.description}</p>
          <SourceBadges item={item} />
        </div>
        {!readOnly && <UsageCard item={item} />}
      </div>
      <div className="playlist-workspace">
        <div className="tracklist card" aria-label="Playlist tracks">
          {trackList(item).map((track) => (
            <TrackRow
              key={track.id}
              track={track}
              readOnly={readOnly}
              onRemove={async () => {
                try {
                  const updated = await api.removeTrack(id, track.id);
                  setItem(updated);
                } catch (reason) {
                  setError(
                    reason instanceof Error
                      ? reason.message
                      : "Could not remove track",
                  );
                }
              }}
              onReplace={(prompt) =>
                run(() => api.replaceTrack(id, track.id, prompt, effort), load)
              }
            />
          ))}
        </div>
        <aside className="actions-column">
          {!readOnly && (
            <form className="card refine-card" onSubmit={submitRefine}>
              <h2>Shape the mix</h2>
              <label htmlFor="refine-prompt">Refinement request</label>
              <textarea
                id="refine-prompt"
                required
                minLength={3}
                maxLength={4000}
                value={refine}
                onChange={(e) => setRefine(e.target.value)}
                placeholder="Make the middle more adventurous, but preserve the soft landing…"
              />
              <label>
                Reasoning
                <select
                  value={effort}
                  onChange={(e) => setEffort(e.target.value as Effort)}
                >
                  <option value="medium">Medium</option>
                  <option value="high">High</option>
                  <option value="xhigh">Extra high</option>
                  <option value="max">Maximum</option>
                </select>
              </label>
              <button className="button secondary" type="submit">
                Refine full playlist
              </button>
            </form>
          )}
          {readOnly && (
            <div className="card notice-card">
              <h2>Read-only snapshot</h2>
              <p>
                This playlist mirrors a streaming service. Its tracklist is
                refreshed on Reload and can't be edited here — use it as
                inspiration or hand it to Soundiiz.
              </p>
            </div>
          )}
          {item.sources && item.sources.length > 0 && (
            <div className="card sources-card">
              <h2>Linked services</h2>
              <ul>
                {item.sources.map((source) => (
                  <li key={`${source.kind}:${source.externalId}`}>
                    <div>
                      <strong>{serviceLabel(source.kind)}</strong>
                      <span>
                        synced {new Date(source.syncedAt).toLocaleDateString()}
                      </span>
                    </div>
                    <button
                      className="button tiny ghost"
                      type="button"
                      onClick={() => unlink(source.kind, source.externalId)}
                    >
                      Unlink
                    </button>
                  </li>
                ))}
              </ul>
              <p className="form-note">
                Unlinking splits this service back into its own snapshot and
                stops it re-merging on the next Reload.
              </p>
            </div>
          )}
          <div className="card transfer-card">
            <h2>Open in Soundiiz</h2>
            <p>
              Review the catalog matches, choose your streaming service, and
              complete the transfer on Soundiiz.
            </p>
            <button className="button primary" onClick={transfer}>
              Open Soundiiz handoff <span aria-hidden="true">↗</span>
            </button>
            {item.soundiizUrl && (
              <a
                className="previous-link"
                href={item.soundiizUrl}
                target="_blank"
                rel="noreferrer"
                onClick={(event) => {
                  event.preventDefault();
                  api
                    .openExternalURL(item.soundiizUrl!)
                    .catch((reason) =>
                      setError(
                        reason instanceof Error
                          ? reason.message
                          : "Could not open Soundiiz",
                      ),
                    );
                }}
              >
                Reopen current link
              </a>
            )}
          </div>
        </aside>
      </div>
    </section>
  );
}

/**
 * One row of the tracklist: generated sleeve, title/artists/album, the
 * curator's rationale, an optional quality note, and remove/replace controls.
 * Replace reveals an inline guidance field. Imported playlists are read-only
 * snapshots, so `readOnly` drops the controls entirely.
 */
function TrackRow({
  track,
  readOnly,
  onRemove,
  onReplace,
}: {
  track: Track;
  readOnly?: boolean;
  onRemove?: () => void;
  onReplace?: (prompt: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [prompt, setPrompt] = useState("");
  return (
    <article className="track-row">
      <div
        className="track-art"
        aria-hidden="true"
        style={coverStyle(track.id || track.title)}
      >
        <span>{String(track.position).padStart(2, "0")}</span>
      </div>
      <div className="track-main">
        <h3>
          {track.title}
          {track.version && <small> · {track.version}</small>}
        </h3>
        <p>
          {track.artists.join(", ")}
          {track.album && (
            <>
              {" "}
              — <span>{track.album}</span>
            </>
          )}
        </p>
        <p className="rationale">{track.rationale}</p>
        {track.qualityNote && (
          <span className="quality-note">{track.qualityNote}</span>
        )}
        {editing && (
          <form
            className="replace-form"
            onSubmit={(e) => {
              e.preventDefault();
              onReplace?.(prompt);
              setEditing(false);
            }}
          >
            <label>
              Replacement guidance <span>(optional)</span>
              <input
                value={prompt}
                maxLength={4000}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="Similar energy, but a female vocalist…"
              />
            </label>
            <button className="button tiny" type="submit">
              Replace
            </button>
            <button
              className="button tiny ghost"
              type="button"
              onClick={() => setEditing(false)}
            >
              Cancel
            </button>
          </form>
        )}
      </div>
      {!readOnly && (
        <div className="track-actions">
          <button
            onClick={() => setEditing(true)}
            aria-label={`Replace ${track.title}`}
          >
            Replace
          </button>
          <button onClick={onRemove} aria-label={`Remove ${track.title}`}>
            Remove
          </button>
        </div>
      )}
    </article>
  );
}

/**
 * The cost/consumption summary for the current revision: track and token
 * counts and the estimated USD spend, flagged as token-only when the
 * web-search fee is not yet known.
 */
function UsageCard({ item }: { item: Playlist }) {
  const usage = item.currentRevision.usage;
  return (
    <div className="usage-card card">
      <span>{trackList(item).length} tracks</span>
      <span>{(usage.totalTokens || 0).toLocaleString()} tokens</span>
      <strong>≈ ${usage.estimatedCostUsd.toFixed(4)}</strong>
      <small>
        {usage.searchFeeKnown
          ? "Estimated total"
          : "Token estimate; web-search fees may be additional"}
      </small>
    </div>
  );
}
