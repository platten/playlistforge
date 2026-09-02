/**
 * The single boundary between React and Go. Components import `api` and never
 * touch the Wails runtime directly, so the call convention (and any future
 * transport change) stays contained here. Long operations return a `Job`;
 * `waitForJob` polls it to a terminal state.
 */
import { Call } from "@wailsio/runtime";
import type {
  Config,
  ConnectionStatus,
  Effort,
  Job,
  Playlist,
  SyncResult,
} from "./types";

/** The desktop backend surface, mirroring the exported methods on Go's `desktop.API`. */
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

  // Streaming import.
  connections(): Promise<ConnectionStatus[]>;
  connectService(kind: string): Promise<ConnectionStatus>;
  disconnectService(kind: string): Promise<void>;
  syncSource(kind: string): Promise<SyncResult>;
  unlinkSource(
    playlistId: string,
    kind: string,
    externalId: string,
  ): Promise<Playlist>;
}

// Wails v3 keys every bound method by "<package path>.<type>.<method>". This is
// the service registered in main.go; keeping the adapter narrow stops the
// runtime's call convention from leaking into React components.
const SERVICE = "playlistforge/internal/desktop.API";

/** Call a bound Go method by its short name, normalizing any rejection to an Error. */
async function invoke<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(`${SERVICE}.${method}`, ...args)) as T;
  } catch (reason) {
    throw reason instanceof Error ? reason : new Error(String(reason));
  }
}

// Wails is the only supported runtime. Browser previews have no bindings, so
// every call rejects there with the runtime's own error.
export const api: BackendAPI = {
  config: () => invoke("Config"),
  saveKey: (key, allowPlaintext) => invoke("SaveKey", key, allowPlaintext),
  deleteKey: () => invoke("DeleteKey"),
  playlists: () => invoke("ListPlaylists"),
  playlist: (id) => invoke("GetPlaylist", id),
  generate: (body) => invoke("Generate", body),
  refine: (id, prompt, effort) => invoke("Refine", id, prompt, effort),
  removeTrack: (playlistId, trackId) =>
    invoke("RemoveTrack", playlistId, trackId),
  replaceTrack: (playlistId, trackId, prompt, effort) =>
    invoke("ReplaceTrack", playlistId, trackId, prompt, effort),
  soundiiz: (id) => invoke("CreateSoundiizHandoff", id),
  job: (id) => invoke("GetJob", id),
  cancelJob: (id) => invoke("CancelJob", id),
  openExternalURL: (url) => invoke("OpenExternalURL", url),
  connections: () => invoke("Connections"),
  connectService: (kind) => invoke("ConnectService", kind),
  disconnectService: (kind) => invoke("DisconnectService", kind),
  syncSource: (kind) => invoke("SyncSource", kind),
  unlinkSource: (playlistId, kind, externalId) =>
    invoke("UnlinkSource", playlistId, kind, externalId),
};

/**
 * A job that finished in the `failed` state. `code` carries the backend's
 * machine-readable reason (e.g. an OpenAI billing code) so the UI can offer a
 * targeted recovery action instead of only a message.
 */
export class JobError extends Error {
  constructor(
    message: string,
    readonly code?: string,
  ) {
    super(message);
    this.name = "JobError";
  }
}

/**
 * Poll a job until it reaches a terminal state, calling `onUpdate` with every
 * snapshot (including the first) so the caller can drive a progress overlay.
 * Resolves with the succeeded job; throws `JobError` on failure and a plain
 * Error on cancellation.
 */
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
    throw new JobError(job.error || "The operation failed", job.errorCode);
  if (job.status === "cancelled")
    throw new Error("The operation was cancelled");
  return job;
}
