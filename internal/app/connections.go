package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"playlistforge/internal/musicsource"
)

// connectableKinds is the fixed set of streaming services the UI offers, in
// display order.
var connectableKinds = []musicsource.Kind{musicsource.KindTIDAL, musicsource.KindQobuz, musicsource.KindSpotify}

// ConnectionStatus is the non-secret state of one streaming service.
type ConnectionStatus struct {
	Kind        string `json:"kind"`
	Available   bool   `json:"available"`   // a provider is registered for this kind
	Connected   bool   `json:"connected"`   // a session is stored
	DisplayName string `json:"displayName"` // "signed in as ..." when connected
}

// Connections reports the status of every connectable service.
func (s *Service) Connections() []ConnectionStatus {
	out := make([]ConnectionStatus, 0, len(connectableKinds))
	for _, kind := range connectableKinds {
		status := ConnectionStatus{Kind: string(kind)}
		if s.sources != nil {
			_, status.Available = s.sources.Get(kind)
		}
		if session, err := s.session(kind); err == nil {
			status.Connected = true
			status.DisplayName = session.DisplayName
		}
		out = append(out, status)
	}
	return out
}

// AuthRequest returns what the desktop sign-in window should do for kind.
func (s *Service) AuthRequest(kind musicsource.Kind) (musicsource.AuthRequest, error) {
	provider, err := s.provider(kind)
	if err != nil {
		return musicsource.AuthRequest{}, err
	}
	return provider.AuthRequest()
}

// CompleteAuth turns the value captured by the sign-in window into a stored
// session and returns the refreshed status.
func (s *Service) CompleteAuth(ctx context.Context, kind musicsource.Kind, captured string) (ConnectionStatus, error) {
	provider, err := s.provider(kind)
	if err != nil {
		return ConnectionStatus{}, err
	}
	session, err := provider.Complete(ctx, captured)
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("complete %s sign-in: %w", kind, err)
	}
	session.Kind = kind
	if err := s.saveSession(session); err != nil {
		return ConnectionStatus{}, err
	}
	return ConnectionStatus{Kind: string(kind), Available: true, Connected: true, DisplayName: session.DisplayName}, nil
}

// Disconnect removes the stored session for kind. Imported playlists are kept.
func (s *Service) Disconnect(kind musicsource.Kind) error {
	if s.sessions == nil {
		return errors.New("streaming import is not available")
	}
	if err := s.sessions.Delete(string(kind)); err != nil {
		return err
	}
	return nil
}

// provider returns the registered provider for kind.
func (s *Service) provider(kind musicsource.Kind) (musicsource.Provider, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("unknown streaming service %q", kind)
	}
	if s.sources == nil {
		return nil, errors.New("streaming import is not available")
	}
	provider, ok := s.sources.Get(kind)
	if !ok {
		return nil, fmt.Errorf("%s is not supported by this build", kind)
	}
	return provider, nil
}

// session loads the stored session for kind, refreshing it through the provider
// when it is within a minute of expiry and persisting any renewal.
func (s *Service) session(kind musicsource.Kind) (musicsource.Session, error) {
	if s.sessions == nil {
		return musicsource.Session{}, musicsource.ErrNotConnected
	}
	blob, err := s.sessions.Get(string(kind))
	if err != nil {
		return musicsource.Session{}, err
	}
	var session musicsource.Session
	if err := json.Unmarshal(blob, &session); err != nil {
		return musicsource.Session{}, fmt.Errorf("decode %s session: %w", kind, err)
	}
	session.Kind = kind
	if session.ExpiresAt.IsZero() || time.Until(session.ExpiresAt) >= time.Minute {
		return session, nil
	}
	provider, err := s.provider(kind)
	if err != nil {
		return musicsource.Session{}, err
	}
	refreshed, err := provider.Refresh(s.ctx, session)
	if err != nil {
		return musicsource.Session{}, fmt.Errorf("refresh %s session: %w", kind, err)
	}
	refreshed.Kind = kind
	if err := s.saveSession(refreshed); err != nil {
		return musicsource.Session{}, err
	}
	return refreshed, nil
}

// saveSession persists session under its Kind. Callers reach it only after
// confirming s.sessions is set.
func (s *Service) saveSession(session musicsource.Session) error {
	blob, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode %s session: %w", session.Kind, err)
	}
	return s.sessions.Set(string(session.Kind), blob)
}
