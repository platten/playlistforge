/**
 * Tests for the typed Wails adapter and waitForJob: every method resolves to
 * the correct fully-qualified bound name and argument list, Error and non-Error
 * rejections are both normalized to an Error, job polling reports each interim
 * update, a terminal "failed" job without a message falls back to a default,
 * and a failed job's structured errorCode is carried through as a JobError.
 * `@wailsio/runtime`'s Call is mocked.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Call } from "@wailsio/runtime";
import { api, JobError, waitForJob } from "./api";

vi.mock("@wailsio/runtime", () => ({
  Call: { ByName: vi.fn() },
}));

const byName = vi.mocked(Call.ByName);
const PREFIX = "playlistforge/internal/desktop.API.";

describe("Wails API adapter", () => {
  beforeEach(() => {
    byName.mockResolvedValue({} as never);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("delegates the complete UI contract to the bound Go service", async () => {
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
    await api.connections();
    await api.connectService("tidal");
    await api.disconnectService("qobuz");
    await api.syncSource("tidal");
    await api.unlinkSource("p", "qobuz", "ext-1");

    expect(byName).toHaveBeenCalledWith(`${PREFIX}Config`);
    expect(byName).toHaveBeenCalledWith(`${PREFIX}SaveKey`, "sk-test", false);
    expect(byName).toHaveBeenCalledWith(`${PREFIX}ListPlaylists`);
    expect(byName).toHaveBeenCalledWith(
      `${PREFIX}ReplaceTrack`,
      "p",
      "t",
      "different",
      "xhigh",
    );
    expect(byName).toHaveBeenCalledWith(`${PREFIX}CreateSoundiizHandoff`, "p");
    expect(byName).toHaveBeenCalledWith(
      `${PREFIX}OpenExternalURL`,
      "https://soundiiz.com/go/import-playlist/a",
    );
    expect(byName).toHaveBeenCalledWith(`${PREFIX}Connections`);
    expect(byName).toHaveBeenCalledWith(`${PREFIX}ConnectService`, "tidal");
    expect(byName).toHaveBeenCalledWith(`${PREFIX}DisconnectService`, "qobuz");
    expect(byName).toHaveBeenCalledWith(`${PREFIX}SyncSource`, "tidal");
    expect(byName).toHaveBeenCalledWith(
      `${PREFIX}UnlinkSource`,
      "p",
      "qobuz",
      "ext-1",
    );
  });

  it("preserves Error rejections from the runtime", async () => {
    byName.mockRejectedValueOnce(new Error("backend failed") as never);
    await expect(api.config()).rejects.toThrow("backend failed");
  });

  it("normalizes non-Error runtime rejections", async () => {
    byName.mockRejectedValueOnce("backend failed" as never);
    await expect(api.config()).rejects.toThrow("backend failed");
  });

  it("polls jobs through success and reports updates", async () => {
    const timer = vi
      .spyOn(window, "setTimeout")
      .mockImplementation((handler: TimerHandler) => {
        if (typeof handler === "function") handler();
        return 1;
      });
    byName.mockResolvedValueOnce({
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

  it("preserves a failed job's structured error code", async () => {
    const failure = waitForJob(
      {
        id: "j",
        status: "failed",
        phase: "Failed",
        error: "Credits are exhausted.",
        errorCode: "credit_balance_exhausted",
      },
      () => undefined,
    );
    await expect(failure).rejects.toMatchObject({
      name: "JobError",
      code: "credit_balance_exhausted",
    } satisfies Partial<JobError>);
  });
});
