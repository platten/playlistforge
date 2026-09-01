import type { Config, Effort, Job, Playlist } from "./types";
import { createDesktopAPI } from "./desktop-api";

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
  soundiiz(id: string, destinations: string[]): Promise<Job>;
  job(id: string): Promise<Job>;
  cancelJob(id: string): Promise<void>;
  openExternalURL(url: string): Promise<void>;
}

// Every browser mutation carries a non-simple header. Together with the
// backend's Origin checks, this prevents another website from submitting a
// simple cross-origin request to the unauthenticated loopback API.
const protectionHeaders = {
  "Content-Type": "application/json",
  "X-Playlist-Forge": "1",
};

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // Keep response decoding and error normalization in one place so page
  // components only handle domain outcomes.
  const response = await fetch(path, init);
  if (!response.ok) {
    const body = (await response
      .json()
      .catch(() => ({ error: response.statusText }))) as { error?: string };
    throw new Error(body.error || `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

const httpApi: BackendAPI = {
  config: () => request<Config>("/api/config"),
  saveKey: (key: string, allowPlaintext: boolean) =>
    request<Config["credential"]>("/api/config/openai-key", {
      method: "PUT",
      headers: protectionHeaders,
      body: JSON.stringify({ key, allowPlaintext }),
    }),
  deleteKey: () =>
    request<void>("/api/config/openai-key", {
      method: "DELETE",
      headers: { "X-Playlist-Forge": "1" },
    }),
  playlists: () => request<Playlist[]>("/api/playlists"),
  playlist: (id: string) =>
    request<Playlist>(`/api/playlists/${encodeURIComponent(id)}`),
  generate: (body: {
    prompt: string;
    trackCount: number;
    effort: Effort;
    referenceIds: string[];
  }) =>
    request<Job>("/api/playlists", {
      method: "POST",
      headers: protectionHeaders,
      body: JSON.stringify(body),
    }),
  refine: (id: string, prompt: string, effort: Effort) =>
    request<Job>(`/api/playlists/${encodeURIComponent(id)}/refine`, {
      method: "POST",
      headers: protectionHeaders,
      body: JSON.stringify({ prompt, effort }),
    }),
  removeTrack: (playlistId: string, trackId: string) =>
    request<Playlist>(
      `/api/playlists/${encodeURIComponent(playlistId)}/tracks/${encodeURIComponent(trackId)}`,
      {
        method: "DELETE",
        headers: { "X-Playlist-Forge": "1" },
      },
    ),
  replaceTrack: (
    playlistId: string,
    trackId: string,
    prompt: string,
    effort: Effort,
  ) =>
    request<Job>(
      `/api/playlists/${encodeURIComponent(playlistId)}/tracks/${encodeURIComponent(trackId)}/replace`,
      {
        method: "POST",
        headers: protectionHeaders,
        body: JSON.stringify({ prompt, effort }),
      },
    ),
  soundiiz: (id: string, destinations: string[]) =>
    request<Job>(`/api/playlists/${encodeURIComponent(id)}/soundiiz`, {
      method: "POST",
      headers: protectionHeaders,
      body: JSON.stringify({ destinations }),
    }),
  job: (id: string) => request<Job>(`/api/jobs/${encodeURIComponent(id)}`),
  cancelJob: (id: string) =>
    request<void>(`/api/jobs/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers: { "X-Playlist-Forge": "1" },
    }),
  openExternalURL: async (url: string) => {
    window.open(url, "_blank", "noopener,noreferrer");
  },
};

// Wails injects its Go bindings before the application module runs. Browser
// builds do not have that global and retain the loopback HTTP transport.
export const api: BackendAPI = createDesktopAPI() || httpApi;

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
