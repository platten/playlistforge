package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
)

// SyncResult summarises one SyncSource run.
type SyncResult struct {
	Added    int       `json:"added"`
	Updated  int       `json:"updated"`
	Deleted  int       `json:"deleted"`
	Merged   int       `json:"merged"`
	SyncedAt time.Time `json:"syncedAt"`
}

// SyncSource refreshes the local mirror of one streaming service: new playlists
// are imported and matched against existing music, changed ones are
// re-hydrated, and playlists removed upstream are deleted. Track lists are
// fetched for new and changed playlists only; unchanged ones stay lazy. It does
// no OpenAI work and runs synchronously.
func (s *Service) SyncSource(ctx context.Context, kind musicsource.Kind) (SyncResult, error) {
	var result SyncResult
	session, err := s.session(kind)
	if err != nil {
		return result, err
	}
	provider, err := s.provider(kind)
	if err != nil {
		return result, err
	}
	remote, err := provider.ListPlaylists(ctx, session)
	if err != nil {
		return result, fmt.Errorf("list %s playlists: %w", kind, err)
	}

	links, err := s.repo.ListSourceLinks(ctx, string(kind))
	if err != nil {
		return result, err
	}
	byExternal := make(map[string]playlist.SourceLink, len(links))
	for _, link := range links {
		byExternal[link.ExternalID] = link
	}

	now := s.now().UTC()
	seen := make(map[string]struct{}, len(remote))
	for _, rp := range remote {
		seen[rp.ExternalID] = struct{}{}
		in := playlist.SourceInput{
			Kind: string(kind), ExternalID: rp.ExternalID, ExternalURL: rp.URL,
			ETag: rp.ETag, RemoteUpdatedAt: rp.UpdatedAt,
			Title: rp.Title, Description: rp.Description,
		}
		existing, known := byExternal[rp.ExternalID]
		switch {
		case !known:
			playlistID, err := s.repo.CreateImported(ctx, in, now)
			if err != nil {
				return result, err
			}
			result.Added++
			merged, err := s.hydrateAndMatch(ctx, provider, session, kind, playlistID, rp)
			if err != nil {
				s.logger.Warn("hydrate imported playlist", zap.String("kind", string(kind)), zap.String("external_id", rp.ExternalID), zap.Error(err))
			} else if merged {
				result.Added--
				result.Merged++
			}
		case sourceChanged(existing, rp):
			if err := s.repo.MarkSourceChanged(ctx, in, now); err != nil {
				return result, err
			}
			result.Updated++
			if _, err := s.hydrateAndMatch(ctx, provider, session, kind, existing.PlaylistID, rp); err != nil {
				s.logger.Warn("re-hydrate imported playlist", zap.String("kind", string(kind)), zap.String("external_id", rp.ExternalID), zap.Error(err))
			}
		default:
			if err := s.repo.TouchSourceLink(ctx, string(kind), rp.ExternalID, now); err != nil {
				return result, err
			}
		}
	}

	for externalID := range byExternal {
		if _, ok := seen[externalID]; ok {
			continue
		}
		deleted, err := s.repo.RemoveSourceLink(ctx, string(kind), externalID)
		if err != nil {
			return result, err
		}
		if deleted {
			result.Deleted++
		}
	}

	result.SyncedAt = now
	return result, nil
}

// sourceChanged decides whether a linked playlist needs re-hydration.
func sourceChanged(link playlist.SourceLink, rp musicsource.RemotePlaylist) bool {
	if !link.TracksFetched {
		return true // a previous hydration did not finish
	}
	if rp.ETag != "" || link.ETag != "" {
		return rp.ETag != link.ETag
	}
	if !rp.UpdatedAt.IsZero() && !link.RemoteUpdatedAt.IsZero() {
		return !rp.UpdatedAt.Equal(link.RemoteUpdatedAt)
	}
	return false // no change signal from this provider; trust the cache
}

// hydrateAndMatch fetches a playlist's tracks. A freshly created shell is first
// checked against existing music: on a match its source link moves onto that
// record (merged == true) and the shell is discarded; otherwise the tracks are
// written into the import. A changed existing import is simply overwritten.
func (s *Service) hydrateAndMatch(ctx context.Context, provider musicsource.Provider, session musicsource.Session, kind musicsource.Kind, playlistID string, rp musicsource.RemotePlaylist) (bool, error) {
	tracks, err := provider.PlaylistTracks(ctx, session, rp.ExternalID)
	if err != nil {
		return false, fmt.Errorf("fetch %s tracks: %w", kind, err)
	}
	item, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return false, err
	}
	if item.Origin == playlist.OriginImported && len(item.CurrentRevision.Tracks) == 0 {
		target, err := s.findSameMusic(ctx, playlistID, kind, rp.ExternalID, tracks)
		if err != nil {
			return false, err
		}
		if target != "" {
			if err := s.repo.MergeSourceLink(ctx, playlistID, target); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if err := s.repo.SetImportedTracks(ctx, string(kind), rp.ExternalID, tracks); err != nil {
		return false, err
	}
	return false, nil
}

// findSameMusic returns the id of an existing playlist that holds the same music
// as tracks, or "" for none. Suppressed pairs and un-hydrated candidates are
// skipped.
func (s *Service) findSameMusic(ctx context.Context, selfID string, kind musicsource.Kind, externalID string, tracks []playlist.Track) (string, error) {
	candidates, err := s.repo.List(ctx)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if candidate.ID == selfID || candidate.TracksStale || len(candidate.CurrentRevision.Tracks) == 0 {
			continue
		}
		suppressed, err := s.repo.MatchSuppressed(ctx, candidate.ID, string(kind), externalID)
		if err != nil {
			return "", err
		}
		if suppressed {
			continue
		}
		if playlist.SameMusic(tracks, candidate.CurrentRevision.Tracks).Matched {
			return candidate.ID, nil
		}
	}
	return "", nil
}

// UnlinkSource detaches one streaming service from a merged playlist, recreating
// it as a standalone imported record, and suppresses any future auto-merge of
// the two.
func (s *Service) UnlinkSource(ctx context.Context, playlistID string, kind musicsource.Kind, externalID string) (playlist.Playlist, error) {
	if err := s.repo.SuppressMatch(ctx, playlistID, string(kind), externalID); err != nil {
		return playlist.Playlist{}, err
	}
	if _, err := s.repo.RemoveSourceLink(ctx, string(kind), externalID); err != nil {
		return playlist.Playlist{}, err
	}
	// Re-import it as its own record; the next SyncSource will not re-merge it.
	if _, err := s.SyncSource(ctx, kind); err != nil {
		return playlist.Playlist{}, err
	}
	return s.repo.Get(ctx, playlistID)
}
