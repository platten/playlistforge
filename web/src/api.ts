import type { Config, Effort, Job, Playlist } from "./types";

export interface BackendAPI {
  config(): Promise<Config>;
  saveKey(key: string, allowPlaintext: boolean): Promise<Config["credential"]>;
  deleteKey(): Promise<void>;
  playlists(): Promise<Playlist[]>;
  playlist(id: string): Promise<Playlist>;
  generate(body: {
    prompt: string;
    trackCount: number;
    effort: Effort;
    referenceIds: string[];
  }): Promise<Job>;
  refine(id: string, prompt: string, effort: Effort): Promise<Job>;
  removeTrack(playlistId: string, trackId: string): Promise<Playlist>;
  replaceTrack(
    playlistId: string,
    trackId: string,
    prompt: string,
    effort: Effort,
  ): Promise<Job>;
  soundiiz(id: string): Promise<Job>;
  job(id: string): Promise<Job>;
  cancelJob(id: string): Promise<void>;
  openExternalURL(url: string): Promise<void>;
}

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
  if (!bindings) throw new Error("Wails desktop bindings are unavailable");
  try {
    return await operation(bindings);
  } catch (reason) {
    throw reason instanceof Error ? reason : new Error(String(reason));
  }
}

// Wails is the only supported runtime. Keeping this adapter typed and narrow
// prevents generated binding details from leaking into React components.
export const api: BackendAPI = {
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
  soundiiz: (id) => desktopCall((backend) => backend.CreateSoundiizHandoff(id)),
  job: (id) => desktopCall((backend) => backend.GetJob(id)),
  cancelJob: (id) => desktopCall((backend) => backend.CancelJob(id)),
  openExternalURL: (url) =>
    desktopCall((backend) => backend.OpenExternalURL(url)),
};

export async function waitForJob(
  initial: Job,
  onUpdate: (job: Job) => void,
): Promise<Job> {
  // Jobs are process-local and intentionally polled. The short interval keeps
  // the UI responsive without requiring a persistent WebSocket connection for
  // a single-user desktop application.
  let job = initial;
  onUpdate(job);
  while (job.status === "queued" || job.status === "running") {
    await new Promise((resolve) => window.setTimeout(resolve, 800));
    job = await api.job(job.id);
    onUpdate(job);
  }
  if (job.status === "failed")
    throw new Error(job.error || "The operation failed");
  if (job.status === "cancelled")
    throw new Error("The operation was cancelled");
  return job;
}
