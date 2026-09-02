import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "jest-axe";
import App from "./App";
import type { Config } from "./types";

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

function installBindings() {
  const bindings = {
    Config: vi.fn(() => Promise.resolve(config)),
    SaveKey: vi.fn(() => Promise.resolve(config.credential)),
    DeleteKey: vi.fn(() => Promise.resolve()),
    ListPlaylists: vi.fn(() => Promise.resolve([])),
    GetPlaylist: vi.fn(() => Promise.resolve({})),
    Generate: vi.fn(() => Promise.resolve({})),
    Refine: vi.fn(() => Promise.resolve({})),
    RemoveTrack: vi.fn(() => Promise.resolve({})),
    ReplaceTrack: vi.fn(() => Promise.resolve({})),
    CreateSoundiizHandoff: vi.fn(() => Promise.resolve({})),
    GetJob: vi.fn(() => Promise.resolve({})),
    CancelJob: vi.fn(() => Promise.resolve()),
    OpenExternalURL: vi.fn(() => Promise.resolve()),
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { desktop: { API: bindings } },
  });
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
    Reflect.deleteProperty(window, "go");
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
