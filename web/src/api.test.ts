import { afterEach, describe, expect, it, vi } from "vitest";
import { api, waitForJob } from "./api";

function installBindings() {
  const result = Promise.resolve({});
  const bindings = {
    Config: vi.fn(() => result),
    SaveKey: vi.fn(() => result),
    DeleteKey: vi.fn(() => Promise.resolve()),
    ListPlaylists: vi.fn(() => Promise.resolve([])),
    GetPlaylist: vi.fn(() => result),
    Generate: vi.fn(() => result),
    Refine: vi.fn(() => result),
    RemoveTrack: vi.fn(() => result),
    ReplaceTrack: vi.fn(() => result),
    CreateSoundiizHandoff: vi.fn(() => result),
    GetJob: vi.fn(() => result),
    CancelJob: vi.fn(() => Promise.resolve()),
    OpenExternalURL: vi.fn(() => Promise.resolve()),
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { desktop: { API: bindings } },
  });
  return bindings;
}

describe("Wails API adapter", () => {
  afterEach(() => {
    Reflect.deleteProperty(window, "go");
    vi.restoreAllMocks();
  });

  it("fails clearly when Wails bindings are unavailable", async () => {
    await expect(api.config()).rejects.toThrow(
      "Wails desktop bindings are unavailable",
    );
  });

  it("delegates the complete UI contract to Go bindings", async () => {
    const bindings = installBindings();
    await api.config();
    await api.saveKey("sk-test", false);
    await api.deleteKey();
    await api.playlists();
    await api.playlist("p");
    await api.generate({
      prompt: "jazz",
      trackCount: 20,
      effort: "medium",
      referenceIds: [],
    });
    await api.refine("p", "more", "high");
    await api.removeTrack("p", "t");
    await api.replaceTrack("p", "t", "different", "xhigh");
    await api.soundiiz("p");
    await api.job("j");
    await api.cancelJob("j");
    await api.openExternalURL("https://soundiiz.com/go/import-playlist/a");
    expect(bindings.SaveKey).toHaveBeenCalledWith("sk-test", false);
    expect(bindings.ReplaceTrack).toHaveBeenCalledWith(
      "p",
      "t",
      "different",
      "xhigh",
    );
    expect(bindings.CreateSoundiizHandoff).toHaveBeenCalledWith("p");
    expect(bindings.OpenExternalURL).toHaveBeenCalledOnce();
  });

  it("preserves Error rejections from Go bindings", async () => {
    const bindings = installBindings();
    bindings.Config.mockRejectedValueOnce(new Error("backend failed") as never);
    await expect(api.config()).rejects.toThrow("backend failed");
  });

  it("normalizes non-Error binding rejections", async () => {
    const bindings = installBindings();
    bindings.Config.mockRejectedValueOnce("backend failed" as never);
    await expect(api.config()).rejects.toThrow("backend failed");
  });

  it("polls jobs through success and reports updates", async () => {
    const bindings = installBindings();
    const timer = vi
      .spyOn(window, "setTimeout")
      .mockImplementation((handler: TimerHandler) => {
        if (typeof handler === "function") handler();
        return 1;
      });
    bindings.GetJob.mockResolvedValueOnce({
      id: "j",
      status: "succeeded",
      phase: "Complete",
      playlistId: "p",
    } as never);
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
