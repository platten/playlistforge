import { FormEvent, useCallback, useEffect, useState } from "react";
import { api, waitForJob } from "./api";
import { ApiKeyHelpDialog } from "./ApiKeyHelpDialog";
import { BusyOverlay } from "./BusyOverlay";
import type { Config, Effort, Job, Playlist, Track } from "./types";

type Route =
  { page: "home" | "history" | "settings" } | { page: "playlist"; id: string };

const defaultDestinations = ["tidal", "qobuz", "spotify", "apple_music"];
const destinationLabels: Record<string, string> = {
  tidal: "TIDAL",
  qobuz: "Qobuz",
  spotify: "Spotify",
  apple_music: "Apple Music",
};

function parseRoute(): Route {
  // A tiny History API router is sufficient for the four embedded views and
  // avoids adding a routing dependency to the runtime bundle.
  const match = window.location.pathname.match(/^\/playlists\/([^/]+)$/);
  if (match) return { page: "playlist", id: decodeURIComponent(match[1]) };
  if (window.location.pathname === "/history") return { page: "history" };
  if (window.location.pathname === "/settings") return { page: "settings" };
  return { page: "home" };
}

export default function App() {
  const [route, setRoute] = useState<Route>(parseRoute);
  const [config, setConfig] = useState<Config | null>(null);
  const [history, setHistory] = useState<Playlist[]>([]);
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    const [nextConfig, nextHistory] = await Promise.all([
      api.config(),
      api.playlists(),
    ]);
    setConfig(nextConfig);
    setHistory(nextHistory);
  }, []);

  useEffect(() => {
    // Initial data arrives asynchronously; the callback performs the state sync.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    refresh().catch((reason: Error) => setError(reason.message));
    const listener = () => setRoute(parseRoute());
    window.addEventListener("popstate", listener);
    return () => window.removeEventListener("popstate", listener);
  }, [refresh]);

  function navigate(path: string) {
    window.history.pushState({}, "", path);
    setRoute(parseRoute());
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  async function run(
    operation: () => Promise<Job>,
    after?: (done: Job) => Promise<void> | void,
  ) {
    // All paid operations share this lifecycle so refresh, cancellation, error
    // display, and the delayed busy overlay cannot drift between pages.
    setError("");
    try {
      const initial = await operation();
      const done = await waitForJob(initial, setJob);
      setJob(null);
      await refresh();
      await after?.(done);
    } catch (reason) {
      setJob(null);
      setError(
        reason instanceof Error ? reason.message : "Something went wrong",
      );
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
          <span className="brand-mark" aria-hidden="true">
            PF
          </span>
          <span>Playlist Forge</span>
        </button>
        <nav aria-label="Main navigation">
          <button
            onClick={() => navigate("/")}
            aria-current={route.page === "home" ? "page" : undefined}
          >
            Create
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
      </header>
      {error && (
        <div className="error-banner" role="alert">
          <span>{error}</span>
          <button onClick={() => setError("")} aria-label="Dismiss error">
            ×
          </button>
        </div>
      )}
      <main id="main">
        {route.page === "home" && (
          <CreatePage
            config={config}
            history={history}
            navigate={navigate}
            run={run}
          />
        )}
        {route.page === "history" && (
          <HistoryPage history={history} navigate={navigate} />
        )}
        {route.page === "settings" && (
          <SettingsPage config={config} refresh={refresh} setError={setError} />
        )}
        {route.page === "playlist" && (
          <PlaylistPage
            id={route.id}
            availableDestinations={config?.destinations || defaultDestinations}
            run={run}
            navigate={navigate}
            setError={setError}
          />
        )}
      </main>
      <footer>
        <span>
          Local-first. Your API key never enters the playlist database.
        </span>
        <span>Soundiiz completes transfers on its own site.</span>
        <span>© 2026 Paul Pietkiewicz · MIT License</span>
      </footer>
      <BusyOverlay job={job} onCancel={cancel} />
    </div>
  );
}

// Page components stay in this file because they share the small application
// shell. Extract one when it gains independent state or is reused elsewhere.
function CreatePage({
  config,
  history,
  navigate,
  run,
}: {
  config: Config | null;
  history: Playlist[];
  navigate: (path: string) => void;
  run: (op: () => Promise<Job>, after?: (job: Job) => void) => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [trackCount, setTrackCount] = useState(30);
  const [effort, setEffort] = useState<Effort>("medium");
  const [references, setReferences] = useState<string[]>([]);

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
              Use previous playlists as inspiration <span>(optional)</span>
            </legend>
            {history.slice(0, 8).map((item) => (
              <label key={item.id}>
                <input
                  type="checkbox"
                  checked={references.includes(item.id)}
                  onChange={(e) =>
                    setReferences(
                      e.target.checked
                        ? [...references, item.id]
                        : references.filter((id) => id !== item.id),
                    )
                  }
                />
                <span>{item.currentRevision.title}</span>
              </label>
            ))}
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

function HistoryPage({
  history,
  navigate,
}: {
  history: Playlist[];
  navigate: (path: string) => void;
}) {
  return (
    <section className="page">
      <p className="eyebrow">Your library</p>
      <h1>Playlist history</h1>
      <p className="lede compact">
        Every edit becomes a revision, so your latest tracklist stays available
        for future inspiration.
      </p>
      {history.length === 0 ? (
        <div className="empty card">
          <h2>No playlists yet</h2>
          <p>Your first creation will appear here.</p>
          <button className="button primary" onClick={() => navigate("/")}>
            Create one
          </button>
        </div>
      ) : (
        <div className="history-grid">
          {history.map((item) => (
            <button
              className="history-card card"
              key={item.id}
              onClick={() => navigate(`/playlists/${item.id}`)}
            >
              <span className="history-count">
                {item.currentRevision.tracks.length} tracks
              </span>
              <h2>{item.currentRevision.title}</h2>
              <p>{item.currentRevision.description}</p>
              <span>
                Revision {item.revisionCount} ·{" "}
                {new Date(item.updatedAt).toLocaleDateString()}
              </span>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}

function SettingsPage({
  config,
  refresh,
  setError,
}: {
  config: Config | null;
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
      setError(reason instanceof Error ? reason.message : "Could not save key");
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
      setError(
        reason instanceof Error ? reason.message : "Could not remove key",
      );
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
      <ApiKeyHelpDialog
        open={showKeyHelp}
        onClose={() => setShowKeyHelp(false)}
      />
    </section>
  );
}

function PlaylistPage({
  id,
  availableDestinations,
  run,
  navigate,
  setError,
}: {
  id: string;
  availableDestinations: string[];
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
  const [destination, setDestination] = useState("");
  const load = useCallback(
    () =>
      api.playlist(id).then((value) => {
        setItem(value);
        // Older releases allowed several saved intentions. Do not guess which
        // one should win when reopening that legacy state.
        setDestination(
          value.destinations?.length === 1 ? value.destinations[0] : "",
        );
      }),
    [id],
  );
  useEffect(() => {
    load().catch((reason: Error) => setError(reason.message));
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
      () => api.soundiiz(id, [destination]),
      async () => {
        const fresh = await api.playlist(id);
        setItem(fresh);
        if (fresh.soundiizUrl)
          window.open(fresh.soundiizUrl, "_blank", "noopener,noreferrer");
      },
    );
  }
  return (
    <section className="page playlist-page">
      <button className="back-link" onClick={() => navigate("/history")}>
        ← History
      </button>
      <div className="playlist-heading">
        <div>
          <p className="eyebrow">Preview · revision {item.revisionCount}</p>
          <h1>{revision.title}</h1>
          <p className="lede compact">{revision.description}</p>
        </div>
        <UsageCard item={item} />
      </div>
      <div className="playlist-workspace">
        <div className="tracklist card" aria-label="Playlist tracks">
          {revision.tracks.map((track) => (
            <TrackRow
              key={track.id}
              track={track}
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
          <div className="card transfer-card">
            <h2>Send to a service</h2>
            <p>
              Choose one intended destination. Soundiiz opens a generic handoff
              page where you review the matches and complete the transfer.
            </p>
            <fieldset className="destination-group">
              <legend>Streaming destination</legend>
              {availableDestinations.map((option) => (
                <label className="destination" key={option}>
                  <input
                    type="radio"
                    name="streaming-destination"
                    value={option}
                    checked={destination === option}
                    onChange={() => setDestination(option)}
                  />
                  <span>{destinationLabels[option] || option}</span>
                </label>
              ))}
            </fieldset>
            <button
              className="button primary"
              disabled={!destination}
              onClick={transfer}
            >
              Open Soundiiz handoff <span aria-hidden="true">↗</span>
            </button>
            {item.soundiizUrl && (
              <a
                className="previous-link"
                href={item.soundiizUrl}
                target="_blank"
                rel="noreferrer"
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

function TrackRow({
  track,
  onRemove,
  onReplace,
}: {
  track: Track;
  onRemove: () => void;
  onReplace: (prompt: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [prompt, setPrompt] = useState("");
  return (
    <article className="track-row">
      <span className="track-number">
        {String(track.position).padStart(2, "0")}
      </span>
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
              onReplace(prompt);
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
    </article>
  );
}

function UsageCard({ item }: { item: Playlist }) {
  const usage = item.currentRevision.usage;
  return (
    <div className="usage-card card">
      <span>{item.currentRevision.tracks.length} tracks</span>
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
