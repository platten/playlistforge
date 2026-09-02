// These interfaces mirror the Go JSON contract. Keep field names and optional
// values synchronized with internal/playlist/model.go when the API evolves.
export type Effort = "medium" | "high" | "xhigh" | "max";

export interface Track {
  id: string;
  position: number;
  title: string;
  artists: string[];
  album: string;
  releaseYear: number | null;
  version: string | null;
  remasterYear: number | null;
  qualityNote: string | null;
  /** ISRC of the exact recording; authoritative for imported tracks, usually null for generated ones. */
  isrc: string | null;
  rationale: string;
}

export interface Usage {
  responseId: string;
  model: string;
  effort: Effort;
  inputTokens: number;
  cachedTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  totalTokens: number;
  webSearchCalls: number;
  estimatedCostUsd: number;
  searchFeeKnown: boolean;
  pricingVersion: string;
  elapsedMillis: number;
  createdAt: string;
}

export interface Revision {
  id: string;
  playlistId: string;
  number: number;
  title: string;
  description: string;
  prompt: string;
  trackTarget: number;
  model: string;
  effort: Effort;
  tracks: Track[];
  usage: Usage;
  createdAt: string;
}

/** A place a playlist also lives on a streaming service. */
export interface PlaylistSourceLink {
  kind: string;
  url: string;
  syncedAt: string;
  /** The service's own id for this playlist; passed back to unlink it. */
  externalId: string;
}

export interface Playlist {
  id: string;
  createdAt: string;
  updatedAt: string;
  currentRevision: Revision;
  revisionCount: number;
  /** "generated" (from a brief) or "imported" (mirrors a streaming service). */
  origin: "generated" | "imported";
  /** Every service this playlist is linked to; empty for a purely generated one. */
  sources?: PlaylistSourceLink[];
  soundiizUrl?: string;
  soundiizExpiresAt?: string;
}

/** Status of one streaming service the desktop build can import from. */
export interface ConnectionStatus {
  kind: string;
  available: boolean;
  connected: boolean;
  displayName: string;
}

/** Summary returned by a per-service Reload. */
export interface SyncResult {
  added: number;
  updated: number;
  deleted: number;
  merged: number;
  syncedAt: string;
}

export interface Job {
  id: string;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  phase: string;
  playlistId?: string;
  error?: string;
  errorCode?: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface Config {
  credential: {
    configured: boolean;
    storage: "environment" | "keyring" | "config" | "none";
    readOnly?: boolean;
  };
  model: string;
  trackCounts: number[];
  efforts: Effort[];
  pricing: {
    version: string;
    inputPerMillion: number;
    cachedInputPerMillion: number;
    outputPerMillion: number;
    webSearchFeeKnown: boolean;
  };
}
