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

export interface Playlist {
  id: string;
  createdAt: string;
  updatedAt: string;
  currentRevision: Revision;
  revisionCount: number;
  soundiizUrl?: string;
  soundiizExpiresAt?: string;
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
