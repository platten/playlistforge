package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"playlistforge/internal/playlist"
)

// ListSourceLinks returns every stored link for one streaming service.
func (r *Repository) ListSourceLinks(ctx context.Context, kind string) ([]playlist.SourceLink, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT playlist_id,external_id,etag,remote_updated_at,tracks_fetched FROM playlist_sources WHERE kind=?`, kind)
	if err != nil {
		return nil, fmt.Errorf("list source links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var links []playlist.SourceLink
	for rows.Next() {
		link := playlist.SourceLink{Kind: kind}
		var etag, remoteUpdated sql.NullString
		var fetched int
		if err := rows.Scan(&link.PlaylistID, &link.ExternalID, &etag, &remoteUpdated, &fetched); err != nil {
			return nil, fmt.Errorf("scan source link: %w", err)
		}
		link.ETag = etag.String
		link.TracksFetched = fetched != 0
		if remoteUpdated.Valid && remoteUpdated.String != "" {
			if link.RemoteUpdatedAt, err = parseTime(remoteUpdated.String); err != nil {
				return nil, err
			}
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source links: %w", err)
	}
	return links, rows.Close()
}

// CreateImported inserts an origin=imported playlist with an empty revision and
// one unfetched source link, returning the new playlist id.
func (r *Repository) CreateImported(ctx context.Context, in playlist.SourceInput, syncedAt time.Time) (string, error) {
	playlistID := uuid.NewString()
	revisionID := uuid.NewString()
	now := formatTime(syncedAt.UTC())
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO playlists(id,created_at,updated_at,current_revision_id,revision_count,origin) VALUES(?,?,?,?,1,?)`,
		playlistID, now, now, revisionID, playlist.OriginImported); err != nil {
		return "", fmt.Errorf("insert imported playlist: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO revisions(id,playlist_id,revision_number,title,description,prompt,track_target,model,effort,usage_json,created_at) VALUES(?,?,1,?,?,'',0,'','','{}',?)`,
		revisionID, playlistID, in.Title, in.Description, now); err != nil {
		return "", fmt.Errorf("insert imported revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO playlist_sources(playlist_id,kind,external_id,external_url,etag,remote_updated_at,synced_at,tracks_fetched) VALUES(?,?,?,?,?,?,?,0)`,
		playlistID, in.Kind, in.ExternalID, in.ExternalURL, nullString(in.ETag), nullTime(in.RemoteUpdatedAt), now); err != nil {
		return "", fmt.Errorf("insert source link: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit import: %w", err)
	}
	return playlistID, nil
}

// TouchSourceLink bumps only synced_at for an unchanged playlist.
func (r *Repository) TouchSourceLink(ctx context.Context, kind, externalID string, syncedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE playlist_sources SET synced_at=? WHERE kind=? AND external_id=?`,
		formatTime(syncedAt.UTC()), kind, externalID)
	if err != nil {
		return fmt.Errorf("touch source link: %w", err)
	}
	return nil
}

// MarkSourceChanged records a new remote state and resets tracks_fetched, and
// updates the linked playlist's current revision title and description.
func (r *Repository) MarkSourceChanged(ctx context.Context, in playlist.SourceInput, syncedAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark changed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var playlistID, revisionID string
	err = tx.QueryRowContext(ctx, `SELECT p.id,p.current_revision_id FROM playlist_sources s JOIN playlists p ON p.id=s.playlist_id WHERE s.kind=? AND s.external_id=?`, in.Kind, in.ExternalID).
		Scan(&playlistID, &revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("locate changed source: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE playlist_sources SET etag=?, remote_updated_at=?, synced_at=?, tracks_fetched=0 WHERE kind=? AND external_id=?`,
		nullString(in.ETag), nullTime(in.RemoteUpdatedAt), formatTime(syncedAt.UTC()), in.Kind, in.ExternalID); err != nil {
		return fmt.Errorf("update source link: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE revisions SET title=?, description=? WHERE id=?`, in.Title, in.Description, revisionID); err != nil {
		return fmt.Errorf("update imported revision: %w", err)
	}
	return tx.Commit()
}

// SetImportedTracks replaces the single revision's tracks for a linked playlist
// and marks the link hydrated.
func (r *Repository) SetImportedTracks(ctx context.Context, kind, externalID string, tracks []playlist.Track) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set tracks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var revisionID string
	err = tx.QueryRowContext(ctx, `SELECT p.current_revision_id FROM playlist_sources s JOIN playlists p ON p.id=s.playlist_id WHERE s.kind=? AND s.external_id=?`, kind, externalID).
		Scan(&revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("locate revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tracks WHERE revision_id=?`, revisionID); err != nil {
		return fmt.Errorf("clear imported tracks: %w", err)
	}
	if err := insertTracks(ctx, tx, revisionID, tracks); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE playlist_sources SET tracks_fetched=1 WHERE kind=? AND external_id=?`, kind, externalID); err != nil {
		return fmt.Errorf("mark hydrated: %w", err)
	}
	return tx.Commit()
}

// MergeSourceLink deletes the provisional imported playlist and re-attaches its
// one source link to an existing playlist holding the same music. The target
// already carries the authoritative tracklist, so the moved link is hydrated.
func (r *Repository) MergeSourceLink(ctx context.Context, shellPlaylistID, targetPlaylistID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin merge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var kind, externalID string
	var externalURL, etag, remoteUpdated sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT kind,external_id,external_url,etag,remote_updated_at FROM playlist_sources WHERE playlist_id=?`, shellPlaylistID).
		Scan(&kind, &externalID, &externalURL, &etag, &remoteUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read shell link: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlists WHERE id=?`, shellPlaylistID); err != nil {
		return fmt.Errorf("delete shell playlist: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO playlist_sources(playlist_id,kind,external_id,external_url,etag,remote_updated_at,synced_at,tracks_fetched) VALUES(?,?,?,?,?,?,?,1) ON CONFLICT DO NOTHING`,
		targetPlaylistID, kind, externalID, externalURL, etag, remoteUpdated, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("attach merged link: %w", err)
	}
	return tx.Commit()
}

// RemoveSourceLink drops one link. If that leaves an imported playlist with no
// remaining links, the playlist and any inbound references are hard-deleted.
func (r *Repository) RemoveSourceLink(ctx context.Context, kind, externalID string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin remove link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var playlistID, origin string
	err = tx.QueryRowContext(ctx, `SELECT p.id,p.origin FROM playlist_sources s JOIN playlists p ON p.id=s.playlist_id WHERE s.kind=? AND s.external_id=?`, kind, externalID).
		Scan(&playlistID, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return false, tx.Commit() // already gone
	}
	if err != nil {
		return false, fmt.Errorf("locate link: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_sources WHERE kind=? AND external_id=?`, kind, externalID); err != nil {
		return false, fmt.Errorf("delete link: %w", err)
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_sources WHERE playlist_id=?`, playlistID).Scan(&remaining); err != nil {
		return false, fmt.Errorf("count remaining links: %w", err)
	}
	deleted := false
	if remaining == 0 && origin != playlist.OriginGenerated {
		if _, err := tx.ExecContext(ctx, `DELETE FROM revision_references WHERE reference_playlist_id=?`, playlistID); err != nil {
			return false, fmt.Errorf("clear inbound references: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM playlists WHERE id=?`, playlistID); err != nil {
			return false, fmt.Errorf("delete orphaned playlist: %w", err)
		}
		deleted = true
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit remove link: %w", err)
	}
	return deleted, nil
}

// SuppressMatch records that playlistID and the given external playlist must
// never be auto-merged again.
func (r *Repository) SuppressMatch(ctx context.Context, playlistID, kind, externalID string) error {
	_, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO match_suppressed(playlist_id,external_kind,external_id) VALUES(?,?,?)`,
		playlistID, kind, externalID)
	if err != nil {
		return fmt.Errorf("suppress match: %w", err)
	}
	return nil
}

// MatchSuppressed reports whether an auto-merge between playlistID and the given
// external playlist is suppressed.
func (r *Repository) MatchSuppressed(ctx context.Context, playlistID, kind, externalID string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM match_suppressed WHERE playlist_id=? AND external_kind=? AND external_id=?`, playlistID, kind, externalID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read match suppression: %w", err)
	}
	return true, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t.UTC())
}
