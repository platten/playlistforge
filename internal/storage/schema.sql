PRAGMA foreign_keys = ON;

-- The aggregate row points at the active immutable revision. Temporary
-- Soundiiz state belongs here because it describes the playlist, not a revision.
CREATE TABLE IF NOT EXISTS playlists (
    id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    current_revision_id TEXT NOT NULL,
    revision_count INTEGER NOT NULL DEFAULT 1,
    soundiiz_url TEXT,
    soundiiz_expires_at TEXT,
    -- 'generated' (born from a brief) or 'imported' (mirrors a streaming service).
    origin TEXT NOT NULL DEFAULT 'generated'
);

-- User and model edits append rows; existing revisions are never updated.
CREATE TABLE IF NOT EXISTS revisions (
    id TEXT PRIMARY KEY,
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    prompt TEXT NOT NULL,
    track_target INTEGER NOT NULL,
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    usage_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(playlist_id, revision_number)
);

-- Track identity is scoped to a revision so historical ordering remains intact.
CREATE TABLE IF NOT EXISTS tracks (
    id TEXT NOT NULL,
    revision_id TEXT NOT NULL REFERENCES revisions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title TEXT NOT NULL,
    artists_json TEXT NOT NULL,
    album TEXT NOT NULL,
    release_year INTEGER,
    version TEXT,
    remaster_year INTEGER,
    quality_note TEXT,
    isrc TEXT,
    rationale TEXT NOT NULL,
    PRIMARY KEY (revision_id, id),
    UNIQUE(revision_id, position)
);

-- References record lineage for generated playlists without copying aggregates.
CREATE TABLE IF NOT EXISTS revision_references (
    revision_id TEXT NOT NULL REFERENCES revisions(id) ON DELETE CASCADE,
    reference_playlist_id TEXT NOT NULL,
    PRIMARY KEY (revision_id, reference_playlist_id)
);

-- Where a playlist also lives on a streaming service. A playlist may carry zero
-- or more links; sync creates, refreshes, and removes them. tracks_fetched is 0
-- until the linked playlist's tracklist has been hydrated.
CREATE TABLE IF NOT EXISTS playlist_sources (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    external_id TEXT NOT NULL,
    external_url TEXT,
    etag TEXT,
    remote_updated_at TEXT,
    synced_at TEXT NOT NULL,
    tracks_fetched INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (playlist_id, kind),
    UNIQUE (kind, external_id)
);

-- A pair of playlist ids the same-music matcher must never re-merge, recorded
-- when a listener manually unlinks a wrongly merged source.
CREATE TABLE IF NOT EXISTS match_suppressed (
    playlist_id TEXT NOT NULL,
    external_kind TEXT NOT NULL,
    external_id TEXT NOT NULL,
    PRIMARY KEY (playlist_id, external_kind, external_id)
);

CREATE INDEX IF NOT EXISTS idx_revisions_playlist ON revisions(playlist_id, revision_number);
CREATE INDEX IF NOT EXISTS idx_tracks_revision ON tracks(revision_id, position);
