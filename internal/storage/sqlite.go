// Package storage persists playlists as immutable revisions in SQLite.
package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"playlistforge/internal/playlist"
)

//go:embed schema.sql
var schema string

// migrations bring a database created by an older schema up to date. Each is
// additive and its "duplicate column name" error on an already-current database
// is expected and ignored. SQLite has no ADD COLUMN IF NOT EXISTS.
var migrations = []string{
	`ALTER TABLE tracks ADD COLUMN isrc TEXT`,
	`ALTER TABLE playlists ADD COLUMN origin TEXT NOT NULL DEFAULT 'generated'`,
}

// ErrNotFound is returned when a playlist identifier does not exist.
var ErrNotFound = errors.New("playlist not found")

// Repository is a SQLite implementation of playlist.Repository.
type Repository struct {
	db *sql.DB
}

// Open initializes a SQLite repository and applies its idempotent schema.
func Open(path string) (*Repository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The desktop app has one user and short transactions. One connection keeps
	// SQLite locking predictable without sacrificing useful concurrency.
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("apply database schema: %w", err), fmt.Errorf("close sqlite: %w", closeErr))
		}
		return nil, fmt.Errorf("apply database schema: %w", err)
	}
	for _, migration := range migrations {
		if _, err := db.ExecContext(ctx, migration); err != nil && !isDuplicateColumn(err) {
			if closeErr := db.Close(); closeErr != nil {
				return nil, errors.Join(fmt.Errorf("apply migration: %w", err), fmt.Errorf("close sqlite: %w", closeErr))
			}
			return nil, fmt.Errorf("apply migration: %w", err)
		}
	}
	return &Repository{db: db}, nil
}

// isDuplicateColumn reports whether err is SQLite's "duplicate column name"
// from re-running an additive ALTER TABLE ADD COLUMN on a current database.
func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

// Close releases the database connection.
func (r *Repository) Close() error { return r.db.Close() }

// Create stores the first immutable revision and its reference relationships.
func (r *Repository) Create(ctx context.Context, revision playlist.Revision, references []string) (playlist.Playlist, error) {
	now := revision.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
		revision.CreatedAt = now
	}
	if revision.PlaylistID == "" {
		revision.PlaylistID = uuid.NewString()
	}
	if revision.ID == "" {
		revision.ID = uuid.NewString()
	}
	revision.Number = 1
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return playlist.Playlist{}, fmt.Errorf("begin create playlist: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO playlists(id,created_at,updated_at,current_revision_id,revision_count) VALUES(?,?,?,?,1)`,
		revision.PlaylistID, formatTime(now), formatTime(now), revision.ID)
	if err != nil {
		return playlist.Playlist{}, fmt.Errorf("insert playlist: %w", err)
	}
	if err := insertRevision(ctx, tx, revision, references); err != nil {
		return playlist.Playlist{}, err
	}
	if err := tx.Commit(); err != nil {
		return playlist.Playlist{}, fmt.Errorf("commit create playlist: %w", err)
	}
	return r.Get(ctx, revision.PlaylistID)
}

// AddRevision appends a revision and atomically makes it current.
func (r *Repository) AddRevision(ctx context.Context, playlistID string, revision playlist.Revision) (playlist.Playlist, error) {
	existing, err := r.Get(ctx, playlistID)
	if err != nil {
		return playlist.Playlist{}, err
	}
	if revision.ID == "" {
		revision.ID = uuid.NewString()
	}
	revision.PlaylistID = playlistID
	revision.Number = existing.RevisionCount + 1
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return playlist.Playlist{}, fmt.Errorf("begin revision: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertRevision(ctx, tx, revision, nil); err != nil {
		return playlist.Playlist{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE playlists SET current_revision_id=?, revision_count=?, updated_at=? WHERE id=?`,
		revision.ID, revision.Number, formatTime(revision.CreatedAt), playlistID)
	if err != nil {
		return playlist.Playlist{}, fmt.Errorf("activate revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return playlist.Playlist{}, fmt.Errorf("commit revision: %w", err)
	}
	return r.Get(ctx, playlistID)
}

func insertRevision(ctx context.Context, tx *sql.Tx, revision playlist.Revision, references []string) error {
	usage, err := json.Marshal(revision.Usage)
	if err != nil {
		return fmt.Errorf("encode usage: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO revisions(id,playlist_id,revision_number,title,description,prompt,track_target,model,effort,usage_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		revision.ID, revision.PlaylistID, revision.Number, revision.Title, revision.Description, revision.Prompt,
		revision.TrackTarget, revision.Model, string(revision.Effort), string(usage), formatTime(revision.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert revision: %w", err)
	}
	if err := insertTracks(ctx, tx, revision.ID, revision.Tracks); err != nil {
		return err
	}
	for _, reference := range references {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revision_references(revision_id,reference_playlist_id) VALUES(?,?)`, revision.ID, reference); err != nil {
			return fmt.Errorf("insert playlist reference: %w", err)
		}
	}
	return nil
}

// insertTracks writes tracks into revisionID at sequential positions. It assigns
// a fresh id to any track that lacks one or repeats an id already used in this
// revision — imported playlists legitimately contain the same recording more
// than once, and (revision_id, id) is the tracks primary key.
func insertTracks(ctx context.Context, tx *sql.Tx, revisionID string, tracks []playlist.Track) error {
	seen := make(map[string]struct{}, len(tracks))
	for i := range tracks {
		track := tracks[i]
		if _, dup := seen[track.ID]; track.ID == "" || dup {
			track.ID = uuid.NewString()
		}
		seen[track.ID] = struct{}{}
		track.Position = i + 1
		artists, err := json.Marshal(track.Artists)
		if err != nil {
			return fmt.Errorf("encode track artists: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tracks(id,revision_id,position,title,artists_json,album,release_year,version,remaster_year,quality_note,isrc,rationale) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			track.ID, revisionID, track.Position, track.Title, string(artists), track.Album, track.ReleaseYear,
			track.Version, track.RemasterYear, track.QualityNote, track.ISRC, track.Rationale)
		if err != nil {
			return fmt.Errorf("insert track %d: %w", i+1, err)
		}
	}
	return nil
}

// List returns playlists with the most recently updated first.
func (r *Repository) List(ctx context.Context) ([]playlist.Playlist, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM playlists ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list playlists: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan playlist id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate playlists: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close playlist list: %w", err)
	}
	result := make([]playlist.Playlist, 0, len(ids))
	for _, id := range ids {
		item, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// Get reconstructs a playlist and its current revision from normalized tables.
func (r *Repository) Get(ctx context.Context, id string) (playlist.Playlist, error) {
	var item playlist.Playlist
	var created, updated, revisionID string
	var soundURL, soundExpires sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,created_at,updated_at,current_revision_id,revision_count,soundiiz_url,soundiiz_expires_at,origin FROM playlists WHERE id=?`, id).
		Scan(&item.ID, &created, &updated, &revisionID, &item.RevisionCount, &soundURL, &soundExpires, &item.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return playlist.Playlist{}, ErrNotFound
	}
	if err != nil {
		return playlist.Playlist{}, fmt.Errorf("get playlist: %w", err)
	}
	item.CreatedAt, err = parseTime(created)
	if err != nil {
		return playlist.Playlist{}, err
	}
	item.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return playlist.Playlist{}, err
	}
	item.CurrentRevision, err = r.getRevision(ctx, revisionID)
	if err != nil {
		return playlist.Playlist{}, err
	}
	if soundURL.Valid {
		item.SoundiizURL = &soundURL.String
	}
	if soundExpires.Valid {
		expires, err := parseTime(soundExpires.String)
		if err != nil {
			return playlist.Playlist{}, err
		}
		item.SoundiizExpires = &expires
	}
	if err := r.loadSources(ctx, &item); err != nil {
		return playlist.Playlist{}, err
	}
	return item, nil
}

// loadSources attaches any streaming-service links to item and, for an imported
// playlist, sets TracksStale from whether the tracklist has been hydrated.
func (r *Repository) loadSources(ctx context.Context, item *playlist.Playlist) error {
	rows, err := r.db.QueryContext(ctx, `SELECT kind,external_id,external_url,synced_at,tracks_fetched FROM playlist_sources WHERE playlist_id=? ORDER BY kind`, item.ID)
	if err != nil {
		return fmt.Errorf("load playlist sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	anyStale := false
	linked := false
	for rows.Next() {
		var kind, externalID, syncedAt string
		var url sql.NullString
		var fetched int
		if err := rows.Scan(&kind, &externalID, &url, &syncedAt, &fetched); err != nil {
			return fmt.Errorf("scan playlist source: %w", err)
		}
		linked = true
		synced, err := parseTime(syncedAt)
		if err != nil {
			return err
		}
		item.Sources = append(item.Sources, playlist.PlaylistSource{Kind: kind, URL: url.String, SyncedAt: synced, ExternalID: externalID})
		if fetched == 0 {
			anyStale = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate playlist sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close playlist sources: %w", err)
	}
	item.TracksStale = item.Origin == playlist.OriginImported && linked && anyStale
	return nil
}

func (r *Repository) getRevision(ctx context.Context, id string) (playlist.Revision, error) {
	var revision playlist.Revision
	var effort, usageJSON, created string
	err := r.db.QueryRowContext(ctx, `SELECT id,playlist_id,revision_number,title,description,prompt,track_target,model,effort,usage_json,created_at FROM revisions WHERE id=?`, id).
		Scan(&revision.ID, &revision.PlaylistID, &revision.Number, &revision.Title, &revision.Description, &revision.Prompt,
			&revision.TrackTarget, &revision.Model, &effort, &usageJSON, &created)
	if err != nil {
		return playlist.Revision{}, fmt.Errorf("get revision: %w", err)
	}
	revision.Effort = playlist.Effort(effort)
	if err := json.Unmarshal([]byte(usageJSON), &revision.Usage); err != nil {
		return playlist.Revision{}, fmt.Errorf("decode usage: %w", err)
	}
	revision.CreatedAt, err = parseTime(created)
	if err != nil {
		return playlist.Revision{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,position,title,artists_json,album,release_year,version,remaster_year,quality_note,isrc,rationale FROM tracks WHERE revision_id=? ORDER BY position`, id)
	if err != nil {
		return playlist.Revision{}, fmt.Errorf("get tracks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var track playlist.Track
		var artistsJSON string
		var release, remaster sql.NullInt64
		var version, quality, isrc sql.NullString
		if err := rows.Scan(&track.ID, &track.Position, &track.Title, &artistsJSON, &track.Album, &release, &version, &remaster, &quality, &isrc, &track.Rationale); err != nil {
			return playlist.Revision{}, fmt.Errorf("scan track: %w", err)
		}
		if err := json.Unmarshal([]byte(artistsJSON), &track.Artists); err != nil {
			return playlist.Revision{}, fmt.Errorf("decode artists: %w", err)
		}
		if release.Valid {
			value := int(release.Int64)
			track.ReleaseYear = &value
		}
		if remaster.Valid {
			value := int(remaster.Int64)
			track.RemasterYear = &value
		}
		if version.Valid {
			track.Version = &version.String
		}
		if quality.Valid {
			track.QualityNote = &quality.String
		}
		if isrc.Valid {
			track.ISRC = &isrc.String
		}
		revision.Tracks = append(revision.Tracks, track)
	}
	if err := rows.Err(); err != nil {
		return playlist.Revision{}, fmt.Errorf("iterate tracks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return playlist.Revision{}, fmt.Errorf("close tracks: %w", err)
	}
	if revision.Tracks == nil {
		// A not-yet-hydrated import has no track rows; keep the JSON contract a
		// list so the frontend never sees a null tracklist.
		revision.Tracks = []playlist.Track{}
	}
	return revision, nil
}

// DeleteTrack preserves history by creating a new revision without trackID.
func (r *Repository) DeleteTrack(ctx context.Context, playlistID, trackID string) (playlist.Playlist, error) {
	item, err := r.Get(ctx, playlistID)
	if err != nil {
		return playlist.Playlist{}, err
	}
	if len(item.CurrentRevision.Tracks) <= 1 {
		return playlist.Playlist{}, errors.New("a playlist must keep at least one track")
	}
	tracks := make([]playlist.Track, 0, len(item.CurrentRevision.Tracks)-1)
	found := false
	for _, track := range item.CurrentRevision.Tracks {
		if track.ID == trackID {
			found = true
			continue
		}
		track.ID = uuid.NewString()
		track.Position = len(tracks) + 1
		tracks = append(tracks, track)
	}
	if !found {
		return playlist.Playlist{}, errors.New("track not found")
	}
	revision := item.CurrentRevision
	revision.ID = uuid.NewString()
	revision.Tracks = tracks
	revision.CreatedAt = time.Now().UTC()
	return r.AddRevision(ctx, playlistID, revision)
}

// SetSoundiiz stores the latest temporary Soundiiz handoff and expiration.
func (r *Repository) SetSoundiiz(ctx context.Context, playlistID, url string, expiresAt int64) error {
	expires := formatTime(time.Unix(expiresAt, 0).UTC())
	result, err := r.db.ExecContext(ctx, `UPDATE playlists SET soundiiz_url=?, soundiiz_expires_at=?, updated_at=? WHERE id=?`, url, expires, formatTime(time.Now().UTC()), playlistID)
	if err != nil {
		return fmt.Errorf("save Soundiiz handoff: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read handoff result: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time: %w", err)
	}
	return parsed, nil
}
