import { afterEach, describe, expect, it, vi } from "vitest";
import { createDesktopAPI } from "./desktop-api";

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
  afterEach(() => Reflect.deleteProperty(window, "go"));

  it("is absent in an ordinary browser", () => {
    expect(createDesktopAPI()).toBeUndefined();
  });

  it("delegates the complete UI contract to Go bindings", async () => {
    const bindings = installBindings();
    const api = createDesktopAPI()!;
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

  it("normalizes non-Error binding rejections", async () => {
    const bindings = installBindings();
    bindings.Config.mockRejectedValueOnce("backend failed" as never);
    await expect(createDesktopAPI()!.config()).rejects.toThrow(
      "backend failed",
    );
  });
});
