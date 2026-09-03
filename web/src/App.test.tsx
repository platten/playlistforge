/**
 * End-to-end tests for the application shell with a mocked Wails runtime:
 * user prompt text is never interpreted as HTML, the creation page has no axe
 * violations, a key can be saved and the environment-managed state is
 * read-only, the API-key help dialog opens, the Soundiiz handoff view has no
 * destination controls, and an exhausted OpenAI balance surfaces a billing
 * recovery action. `Call.ByName` is mocked and dispatched by trailing method
 * name to a plain fake bindings object.
 */
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "jest-axe";
import { Call } from "@wailsio/runtime";
import App from "./App";
import type { Config, ConnectionStatus, Job } from "./types";

vi.mock("@wailsio/runtime", () => ({
  Call: { ByName: vi.fn() },
}));

const config: Config = {
  credential: { configured: true, storage: "keyring" },
  model: "gpt-5.6-sol",
  trackCounts: [20, 30],
  efforts: ["medium", "high"],
  pricing: {
    version: "test",
    inputPerMillion: 4,
    cachedInputPerMillion: 0.4,
    outputPerMillion: 20,
    webSearchFeeKnown: false,
  },
};

// The Wails v3 runtime routes every bound call through Call.ByName with a
// "<package>.<type>.<method>" name; this fake dispatches on the trailing
// method so tests keep asserting against a plain bindings object.
function installBindings() {
  const bindings = {
    Config: vi.fn(() => Promise.resolve(config)),
    SaveKey: vi.fn(() => Promise.resolve(config.credential)),
    DeleteKey: vi.fn(() => Promise.resolve()),
    ListPlaylists: vi.fn((): Promise<unknown[]> => Promise.resolve([])),
    GetPlaylist: vi.fn(() => Promise.resolve({})),
    Generate: vi.fn(() => Promise.resolve({})),
    Refine: vi.fn(() => Promise.resolve({})),
    RemoveTrack: vi.fn(() => Promise.resolve({})),
    ReplaceTrack: vi.fn(() => Promise.resolve({})),
    CreateSoundiizHandoff: vi.fn(() => Promise.resolve({})),
    GetJob: vi.fn(() => Promise.resolve({})),
    CancelJob: vi.fn(() => Promise.resolve()),
    OpenExternalURL: vi.fn(() => Promise.resolve()),
    Connections: vi.fn((): Promise<ConnectionStatus[]> =>
      Promise.resolve([
        { kind: "tidal", available: false, connected: false, displayName: "" },
        { kind: "qobuz", available: false, connected: false, displayName: "" },
      ]),
    ),
    CheckConnections: vi.fn(() => bindings.Connections()),
    ConnectService: vi.fn(() =>
      Promise.resolve({
        kind: "tidal",
        available: true,
        connected: true,
        displayName: "Listener",
      }),
    ),
    DisconnectService: vi.fn(() => Promise.resolve()),
    SyncSource: vi.fn((): Promise<Job> =>
      Promise.resolve({
        id: "sync-job",
        status: "running",
        phase: "TIDAL · Rainy",
        completed: 1,
        total: 3,
      }),
    ),
    UnlinkSource: vi.fn(() => Promise.resolve({})),
  };
  vi.mocked(Call.ByName).mockImplementation(((
    name: string,
    ...args: unknown[]
  ) => {
    const method = name.slice(name.lastIndexOf(".") + 1);
    const handler = (bindings as Record<string, (...a: unknown[]) => unknown>)[
      method
    ];
    if (!handler) return Promise.reject(new Error(`unbound method: ${name}`));
    return handler(...args);
  }) as typeof Call.ByName);
  return bindings;
}

describe("App", () => {
  let bindings: ReturnType<typeof installBindings>;

  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    vi.stubGlobal("scrollTo", vi.fn());
    bindings = installBindings();
  });
  afterEach(() => {
    vi.mocked(Call.ByName).mockReset();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders the creation experience without interpreting prompt text as HTML", async () => {
    render(<App />);
    const prompt = await screen.findByLabelText(
      /what should this playlist feel/i,
    );
    fireEvent.change(prompt, {
      target: { value: "<img src=x onerror=alert(1)> jazz" },
    });
    expect(prompt).toHaveValue("<img src=x onerror=alert(1)> jazz");
    expect(document.querySelector("img")).toBeNull();
    expect(
      screen.getByRole("button", { name: /forge playlist/i }),
    ).toBeEnabled();
  });
  it("has no detectable accessibility violations on the creation page", async () => {
    render(<App />);
    await screen.findByLabelText(/what should this playlist feel/i);
    expect(await axe(document.body)).toHaveNoViolations();
  });
  it("offers billing recovery for an exhausted OpenAI balance", async () => {
    bindings.Generate.mockResolvedValueOnce({
      id: "job",
      status: "failed",
      phase: "Failed",
      error: "Your OpenAI organization has no prepaid credits remaining.",
      errorCode: "credit_balance_exhausted",
    });
    render(<App />);
    const prompt = await screen.findByLabelText(
      /what should this playlist feel/i,
    );
    fireEvent.change(prompt, { target: { value: "rainy jazz" } });
    fireEvent.click(screen.getByRole("button", { name: /forge playlist/i }));

    const billing = await screen.findByRole("button", {
      name: /review openai billing/i,
    });
    expect(screen.getByRole("alert")).toHaveTextContent(/no prepaid credits/i);
    expect(await axe(screen.getByRole("alert"))).toHaveNoViolations();
    fireEvent.click(billing);
    await waitFor(() =>
      expect(bindings.OpenExternalURL).toHaveBeenCalledWith(
        "https://platform.openai.com/settings/organization/billing/overview",
      ),
    );
  });
  it("navigates to settings and saves a key", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    const input = screen.getByLabelText("API key");
    fireEvent.change(input, { target: { value: "sk-test" } });
    fireEvent.click(screen.getByRole("button", { name: "Save key" }));
    await waitFor(() =>
      expect(bindings.SaveKey).toHaveBeenCalledWith("sk-test", false),
    );
  });
  it("shows an environment-managed key without editable secret controls", async () => {
    bindings.Config.mockResolvedValueOnce({
      ...config,
      credential: {
        configured: true,
        storage: "environment",
        readOnly: true,
      },
    });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    expect(screen.getByText(/managed by the environment/i)).toBeInTheDocument();
    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Remove saved key" }),
    ).toBeNull();
    expect(screen.getByText("environment")).toBeInTheDocument();
  });
  it("explains API signup and billing in an accessible help dialog", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    const helpButton = screen.getByRole("button", {
      name: "How do I get an API key?",
    });
    fireEvent.click(helpButton);

    const dialog = screen.getByRole("dialog", {
      name: "Get an OpenAI API key",
    });
    expect(dialog).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /signup page/i })).toHaveAttribute(
      "href",
      "https://platform.openai.com/signup",
    );
    expect(
      screen.getByRole("link", { name: /billing settings/i }),
    ).toHaveAttribute(
      "href",
      "https://platform.openai.com/settings/organization/billing/overview",
    );
    expect(
      screen.getByRole("link", { name: /api keys page/i }),
    ).toHaveAttribute("href", "https://platform.openai.com/api-keys");
    const closeButton = screen.getByRole("button", {
      name: "Close API key help",
    });
    expect(closeButton).toHaveFocus();
    expect(await axe(dialog)).toHaveNoViolations();

    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(helpButton).toHaveFocus();
  });
  const revision = (title: string) => ({
    id: `r-${title}`,
    playlistId: `p-${title}`,
    number: 1,
    title,
    description: `${title} description`,
    prompt: "",
    trackTarget: 2,
    model: "gpt-5.6-sol",
    effort: "medium" as const,
    createdAt: "2026-09-01T00:00:00Z",
    tracks: [
      {
        id: "t1",
        position: 1,
        title: "One",
        artists: ["A"],
        album: "",
        rationale: "",
      },
      {
        id: "t2",
        position: 2,
        title: "Two",
        artists: ["B"],
        album: "",
        rationale: "",
      },
    ],
    usage: {
      responseId: "x",
      model: "gpt-5.6-sol",
      effort: "medium" as const,
      inputTokens: 0,
      cachedTokens: 0,
      outputTokens: 0,
      reasoningTokens: 0,
      totalTokens: 0,
      webSearchCalls: 0,
      estimatedCostUsd: 0,
      searchFeeKnown: true,
      pricingVersion: "test",
      elapsedMillis: 0,
      createdAt: "2026-09-01T00:00:00Z",
    },
  });

  const playlist = (
    id: string,
    title: string,
    origin: "generated" | "imported",
    kind?: string,
  ) => ({
    id,
    createdAt: "2026-09-01T00:00:00Z",
    updatedAt: "2026-09-01T00:00:00Z",
    revisionCount: 1,
    origin,
    ...(kind
      ? {
          sources: [
            {
              kind,
              url: `https://${kind}.com/playlist/${id}`,
              syncedAt: "2026-09-01T00:00:00Z",
              externalId: id,
            },
          ],
        }
      : {}),
    currentRevision: revision(title),
  });

  it("hides a streaming playlist that duplicates one forged here, by name", async () => {
    bindings.ListPlaylists.mockResolvedValue([
      playlist("imp-aurora", "Aurora", "imported", "tidal"),
      playlist("gen-aurora", "Aurora", "generated"),
      playlist("imp-borealis", "Borealis", "imported", "qobuz"),
    ]);

    render(<App />);
    // Browse shows "Aurora" once — the imported twin is folded into the forged
    // record — and "Borealis" (no forged twin) still appears.
    fireEvent.click(await screen.findByRole("button", { name: "Browse" }));
    expect(await screen.findAllByText("Aurora")).toHaveLength(1);
    expect(screen.getByText("Borealis")).toBeInTheDocument();
  });

  it("does not list playlists on the Create page", async () => {
    bindings.ListPlaylists.mockResolvedValue([
      playlist("gen-a", "Forged A", "generated"),
      playlist("imp-a", "Import A", "imported", "tidal"),
    ]);

    render(<App />);
    await screen.findByLabelText(/what should this playlist feel/i);
    // The inspiration fieldset is a link into Browse, not an inline checklist.
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    expect(screen.queryByText("Forged A")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /browse all playlists/i }),
    ).toBeInTheDocument();
  });

  it("clusters Browse selections and seeds them into the composer", async () => {
    bindings.ListPlaylists.mockResolvedValue([
      {
        id: "gen",
        createdAt: "2026-09-01T00:00:00Z",
        updatedAt: "2026-09-01T00:00:00Z",
        revisionCount: 1,
        origin: "generated",
        currentRevision: revision("Forged mix"),
      },
      {
        id: "imp",
        createdAt: "2026-09-01T00:00:00Z",
        updatedAt: "2026-09-01T00:00:00Z",
        revisionCount: 1,
        origin: "imported",
        sources: [
          {
            kind: "tidal",
            url: "https://tidal.com/playlist/imp",
            syncedAt: "2026-09-01T00:00:00Z",
            externalId: "imp",
          },
        ],
        currentRevision: revision("TIDAL favourites"),
      },
    ]);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Browse" }));
    fireEvent.click(
      await screen.findByRole("checkbox", { name: /TIDAL favourites/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: /use as inspiration \(1\)/i }),
    );

    expect(
      await screen.findByRole("button", { name: /forge playlist/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("TIDAL favourites")).toBeInTheDocument();
  });

  it("opens a playlist preview from a Browse row", async () => {
    const p = playlist("p-open", "Deep Focus", "generated");
    bindings.ListPlaylists.mockResolvedValue([p]);
    bindings.GetPlaylist.mockResolvedValue(p);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Browse" }));
    fireEvent.click(await screen.findByRole("button", { name: /Deep Focus/ }));

    await waitFor(() =>
      expect(bindings.GetPlaylist).toHaveBeenCalledWith("p-open"),
    );
    expect(
      await screen.findByRole("heading", { name: "Deep Focus", level: 1 }),
    ).toBeInTheDocument();
  });

  it("renders Browse for an un-hydrated import whose tracklist is absent", async () => {
    const shellRevision = revision("Not hydrated yet");
    // A freshly imported shell has a revision but no tracks array yet.
    delete (shellRevision as { tracks?: unknown }).tracks;
    bindings.ListPlaylists.mockResolvedValue([
      {
        id: "shell",
        createdAt: "2026-09-01T00:00:00Z",
        updatedAt: "2026-09-01T00:00:00Z",
        revisionCount: 1,
        origin: "imported",
        sources: [
          {
            kind: "tidal",
            url: "https://tidal.com/playlist/shell",
            syncedAt: "2026-09-01T00:00:00Z",
            externalId: "shell",
          },
        ],
        currentRevision: shellRevision,
      },
    ]);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Browse" }));
    expect(await screen.findByText("Not hydrated yet")).toBeInTheDocument();
    expect(screen.getByText(/0 tracks/)).toBeInTheDocument();
    // The whole view did not blank out.
    expect(
      screen.getByRole("button", { name: /use as inspiration/i }),
    ).toBeInTheDocument();
  });

  it("starts a background sync job from a Reload button", async () => {
    bindings.Connections.mockResolvedValue([
      { kind: "tidal", available: true, connected: true, displayName: "Me" },
      { kind: "qobuz", available: false, connected: false, displayName: "" },
    ]);
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Browse" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Reload TIDAL" }),
    );

    await waitFor(() =>
      expect(bindings.SyncSource).toHaveBeenCalledWith("tidal"),
    );
    // run() refreshes history once the job resolves.
    await waitFor(() =>
      expect(bindings.ListPlaylists.mock.calls.length).toBeGreaterThan(1),
    );
  });

  it("shows a try-again banner when a streaming service is unavailable", async () => {
    bindings.Connections.mockResolvedValue([
      { kind: "tidal", available: true, connected: true, displayName: "Me" },
      { kind: "qobuz", available: false, connected: false, displayName: "" },
    ]);
    bindings.SyncSource.mockResolvedValue({
      id: "sync-job",
      status: "failed",
      phase: "Failed",
      error: "TIDAL is not available right now. Please try again later.",
      errorCode: "service_unavailable",
    });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Browse" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Reload TIDAL" }),
    );

    expect(
      await screen.findByText(/TIDAL is not available right now/i),
    ).toBeInTheDocument();
  });

  it("connects and disconnects a streaming service from settings", async () => {
    bindings.Connections.mockResolvedValueOnce([
      { kind: "tidal", available: true, connected: false, displayName: "" },
      { kind: "qobuz", available: false, connected: false, displayName: "" },
    ]).mockResolvedValue([
      {
        kind: "tidal",
        available: true,
        connected: true,
        displayName: "Listener",
      },
      { kind: "qobuz", available: false, connected: false, displayName: "" },
    ]);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(await screen.findByRole("button", { name: "Connect" }));

    await waitFor(() =>
      expect(bindings.ConnectService).toHaveBeenCalledWith("tidal"),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    await waitFor(() =>
      expect(bindings.DisconnectService).toHaveBeenCalledWith("tidal"),
    );
  });

  it("surfaces an expired streaming session as a reconnect prompt", async () => {
    bindings.Connections.mockResolvedValue([
      {
        kind: "tidal",
        available: true,
        connected: true,
        displayName: "Listener",
        needsReauth: true,
      },
      { kind: "qobuz", available: false, connected: false, displayName: "" },
    ]);

    render(<App />);

    // App-wide banner.
    expect(
      await screen.findByText(/TIDAL session has expired/i),
    ).toBeInTheDocument();

    // Settings row shows the expired state and a Reconnect action.
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(
      await screen.findByText(/Session expired · Listener — reconnect/i),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
    await waitFor(() =>
      expect(bindings.ConnectService).toHaveBeenCalledWith("tidal"),
    );

    // Browse offers Reconnect in place of Reload.
    fireEvent.click(screen.getByRole("button", { name: "Browse" }));
    expect(
      await screen.findByRole("button", { name: "Reconnect TIDAL" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Reload TIDAL" }),
    ).not.toBeInTheDocument();
  });

  it("dismisses the reconnect banner until another session expires", async () => {
    bindings.Connections.mockResolvedValue([
      {
        kind: "tidal",
        available: true,
        connected: true,
        displayName: "Listener",
        needsReauth: true,
      },
      { kind: "qobuz", available: false, connected: false, displayName: "" },
    ]);

    render(<App />);
    const banner = await screen.findByText(/TIDAL session has expired/i);
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(banner).not.toBeInTheDocument();
  });

  it("offers a direct Soundiiz handoff without destination controls", async () => {
    window.history.replaceState({}, "", "/playlists/p");
    const playlist = {
      id: "p",
      createdAt: "2026-09-01T00:00:00Z",
      updatedAt: "2026-09-01T00:00:00Z",
      revisionCount: 1,
      currentRevision: {
        id: "r",
        playlistId: "p",
        number: 1,
        title: "Test mix",
        description: "A test playlist",
        prompt: "test prompt",
        trackTarget: 1,
        model: "gpt-5.6-sol",
        effort: "medium",
        createdAt: "2026-09-01T00:00:00Z",
        tracks: [],
        usage: {
          responseId: "response",
          model: "gpt-5.6-sol",
          effort: "medium",
          inputTokens: 1,
          cachedTokens: 0,
          outputTokens: 1,
          reasoningTokens: 0,
          totalTokens: 2,
          webSearchCalls: 0,
          estimatedCostUsd: 0.000024,
          searchFeeKnown: true,
          pricingVersion: "test",
          elapsedMillis: 1,
          createdAt: "2026-09-01T00:00:00Z",
        },
      },
    };
    bindings.GetPlaylist.mockResolvedValueOnce(playlist);

    render(<App />);
    expect(
      await screen.findByRole("button", { name: /open soundiiz handoff/i }),
    ).toBeEnabled();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    expect(screen.getByText(/choose your streaming service/i)).toBeVisible();
  });
});
