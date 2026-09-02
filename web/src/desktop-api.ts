import type { BackendAPI } from "./api";
import type { Config, Effort, Job, Playlist } from "./types";

type DesktopBindings = {
  Config(): Promise<Config>;
  SaveKey(key: string, allowPlaintext: boolean): Promise<Config["credential"]>;
  DeleteKey(): Promise<void>;
  ListPlaylists(): Promise<Playlist[]>;
  GetPlaylist(id: string): Promise<Playlist>;
  Generate(body: {
    prompt: string;
    trackCount: number;
    effort: Effort;
    referenceIds: string[];
  }): Promise<Job>;
  Refine(id: string, prompt: string, effort: Effort): Promise<Job>;
  RemoveTrack(playlistId: string, trackId: string): Promise<Playlist>;
  ReplaceTrack(
    playlistId: string,
    trackId: string,
    prompt: string,
    effort: Effort,
  ): Promise<Job>;
  CreateSoundiizHandoff(id: string): Promise<Job>;
  GetJob(id: string): Promise<Job>;
  CancelJob(id: string): Promise<void>;
  OpenExternalURL(url: string): Promise<void>;
};

function desktopBindings(): DesktopBindings | undefined {
  return (
    window as typeof window & {
      go?: { desktop?: { API?: DesktopBindings } };
    }
  ).go?.desktop?.API;
}

async function desktopCall<T>(operation: (api: DesktopBindings) => Promise<T>) {
  const bindings = desktopBindings();
  if (!bindings) throw new Error("Desktop bindings are unavailable");
  try {
    return await operation(bindings);
  } catch (reason) {
    throw reason instanceof Error ? reason : new Error(String(reason));
  }
}

export function createDesktopAPI(): BackendAPI | undefined {
  if (!desktopBindings()) return undefined;
  return {
    config: () => desktopCall((backend) => backend.Config()),
    saveKey: (key, allowPlaintext) =>
      desktopCall((backend) => backend.SaveKey(key, allowPlaintext)),
    deleteKey: () => desktopCall((backend) => backend.DeleteKey()),
    playlists: () => desktopCall((backend) => backend.ListPlaylists()),
    playlist: (id) => desktopCall((backend) => backend.GetPlaylist(id)),
    generate: (body) => desktopCall((backend) => backend.Generate(body)),
    refine: (id, prompt, effort) =>
      desktopCall((backend) => backend.Refine(id, prompt, effort)),
    removeTrack: (playlistId, trackId) =>
      desktopCall((backend) => backend.RemoveTrack(playlistId, trackId)),
    replaceTrack: (playlistId, trackId, prompt, effort) =>
      desktopCall((backend) =>
        backend.ReplaceTrack(playlistId, trackId, prompt, effort),
      ),
    soundiiz: (id) =>
      desktopCall((backend) => backend.CreateSoundiizHandoff(id)),
    job: (id) => desktopCall((backend) => backend.GetJob(id)),
    cancelJob: (id) => desktopCall((backend) => backend.CancelJob(id)),
    openExternalURL: (url) =>
      desktopCall((backend) => backend.OpenExternalURL(url)),
  };
}
