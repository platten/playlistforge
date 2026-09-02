import { afterEach, describe, expect, it, vi } from "vitest";
import { api, waitForJob } from "./api";

describe("API client", () => {
  afterEach(() => vi.restoreAllMocks());
  it("adds the same-origin protection header and serializes input", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(
          JSON.stringify({ id: "job", status: "queued", phase: "waiting" }),
          { status: 202, headers: { "Content-Type": "application/json" } },
        ),
      );
    await api.generate({
      prompt: "<img src=x onerror=alert(1)> jazz",
      trackCount: 20,
      effort: "medium",
      referenceIds: [],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/playlists",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-Playlist-Forge": "1" }),
      }),
    );
    const body = JSON.parse(
      String((fetchMock.mock.calls[0][1] as RequestInit).body),
    );
    expect(body.prompt).toContain("<img");
  });
  it("surfaces safe API errors", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "invalid input" }), { status: 400 }),
    );
    await expect(api.config()).rejects.toThrow("invalid input");
  });
  it("supports every application endpoint", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (_input, init) => {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        return new Response("{}", {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      });
    await api.saveKey("sk-x", false);
    await api.deleteKey();
    await api.playlists();
    await api.playlist("a/b");
    await api.refine("p", "more", "high");
    await api.removeTrack("p", "t");
    await api.replaceTrack("p", "t", "different", "xhigh");
    await api.soundiiz("p");
    await api.job("j");
    await api.cancelJob("j");
    expect(fetchMock).toHaveBeenCalledTimes(10);
    expect(
      fetchMock.mock.calls.some(([path]) => String(path).includes("a%2Fb")),
    ).toBe(true);
    const handoff = fetchMock.mock.calls.find(([path]) =>
      String(path).endsWith("/soundiiz"),
    );
    expect((handoff?.[1] as RequestInit).body).toBe("{}");
  });
  it("opens external links in a new browser context", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    await api.openExternalURL("https://soundiiz.com/go/import-playlist/a");
    expect(open).toHaveBeenCalledWith(
      "https://soundiiz.com/go/import-playlist/a",
      "_blank",
      "noopener,noreferrer",
    );
  });
  it("uses the HTTP status text when an error body is not JSON", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("bad gateway", { status: 502, statusText: "Bad Gateway" }),
    );
    await expect(api.config()).rejects.toThrow("Bad Gateway");
  });
  it("uses a generic fallback for a JSON error without a message", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", { status: 418 }),
    );
    await expect(api.config()).rejects.toThrow("Request failed (418)");
  });
  it("polls jobs through success and reports updates", async () => {
    const timer = vi
      .spyOn(window, "setTimeout")
      .mockImplementation((handler: TimerHandler) => {
        if (typeof handler === "function") handler();
        return 1;
      });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          id: "j",
          status: "succeeded",
          phase: "Complete",
          playlistId: "p",
        }),
        { status: 200 },
      ),
    );
    const updates: string[] = [];
    const result = await waitForJob(
      { id: "j", status: "queued", phase: "Wait" },
      (job) => updates.push(job.status),
    );
    expect(result.playlistId).toBe("p");
    expect(updates).toEqual(["queued", "succeeded"]);
    timer.mockRestore();
  });
  it.each([
    [
      { id: "j", status: "failed", phase: "Failed", error: "curation failed" },
      "curation failed",
    ],
    [{ id: "j", status: "cancelled", phase: "Cancelled" }, "cancelled"],
  ] as const)(
    "turns terminal job state into an error",
    async (terminal, message) => {
      await expect(waitForJob(terminal, () => undefined)).rejects.toThrow(
        message,
      );
    },
  );
  it("provides a fallback for failed jobs without an error field", async () => {
    await expect(
      waitForJob(
        { id: "j", status: "failed", phase: "Failed" },
        () => undefined,
      ),
    ).rejects.toThrow("The operation failed");
  });
});
