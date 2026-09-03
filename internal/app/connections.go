package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/musicsource"
)

// connectableKinds is the fixed set of streaming services the UI offers, in
// display order.
var connectableKinds = []musicsource.Kind{musicsource.KindTIDAL, musicsource.KindQobuz}

// verifyTimeout bounds a single provider VerifySession call made by
// CheckConnections.
const verifyTimeout = 15 * time.Second

// connHealth is the cached result of the most recent VerifySession for a Kind.
type connHealth struct {
	needsReauth bool
	checkedAt   time.Time
}

// ConnectionStatus is the non-secret state of one streaming service.
type ConnectionStatus struct {
	Kind        string `json:"kind"`
	Available   bool   `json:"available"`   // a provider is registered for this kind
	Connected   bool   `json:"connected"`   // a session is stored
	DisplayName string `json:"displayName"` // "signed in as ..." when connected
	// NeedsReauth is set when a stored session has been rejected by the service
	// (or can no longer be refreshed). The session is kept so the UI can offer a
	// one-click reconnect rather than silently dropping the connection.
	NeedsReauth bool `json:"needsReauth"`
}

// Connections reports the status of every connectable service.
func (s *Service) Connections() []ConnectionStatus {
	out := make([]ConnectionStatus, 0, len(connectableKinds))
	for _, kind := range connectableKinds {
		status := ConnectionStatus{Kind: string(kind)}
		if s.sources != nil {
			_, status.Available = s.sources.Get(kind)
		}
		switch session, err := s.session(kind); {
		case err == nil:
			status.Connected = true
			status.DisplayName = session.DisplayName
			status.NeedsReauth = s.healthOf(kind).needsReauth
		case s.sessions != nil && s.sessions.Has(string(kind)):
			// A stored session that won't load or refresh (a revoked refresh
			// token, say). Keep it visible as connected-but-broken.
			status.Connected = true
			status.NeedsReauth = true
			if name, ok := s.storedDisplayName(kind); ok {
				status.DisplayName = name
			}
		}
		out = append(out, status)
	}
	return out
}

// storedDisplayName reads just the DisplayName off the persisted session blob,
// without the refresh path session() runs. Used to keep a name on a broken row.
func (s *Service) storedDisplayName(kind musicsource.Kind) (string, bool) {
	if s.sessions == nil {
		return "", false
	}
	blob, err := s.sessions.Get(string(kind))
	if err != nil {
		return "", false
	}
	var stored musicsource.Session
	if err := json.Unmarshal(blob, &stored); err != nil || stored.DisplayName == "" {
		return "", false
	}
	return stored.DisplayName, true
}

// healthOf returns the cached verification result for kind.
func (s *Service) healthOf(kind musicsource.Kind) connHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.health[kind]
}

// markReauth records whether kind currently needs the user to sign in again.
// Called by the health loop and by a sync that hits an auth failure.
func (s *Service) markReauth(kind musicsource.Kind, needs bool) {
	s.mu.Lock()
	s.health[kind] = connHealth{needsReauth: needs, checkedAt: s.now()}
	s.mu.Unlock()
}

// CheckConnections verifies every stored streaming session against its service
// and updates the cached health that Connections reports. A transient failure
// (network, rate limit) leaves the previous state untouched. Safe to call from
// the background loop or a UI-triggered refresh.
func (s *Service) CheckConnections(ctx context.Context) {
	for _, kind := range connectableKinds {
		if s.sessions == nil || !s.sessions.Has(string(kind)) {
			s.markReauth(kind, false)
			continue
		}
		provider, err := s.provider(kind)
		if err != nil {
			continue
		}
		session, err := s.session(kind)
		if err != nil {
			s.markReauth(kind, true)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
		err = provider.VerifySession(cctx, session)
		cancel()
		switch {
		case err == nil:
			s.markReauth(kind, false)
		case errors.Is(err, musicsource.ErrNotConnected):
			s.markReauth(kind, true)
		default:
			s.logger.Debug("verify streaming session",
				zap.String("kind", string(kind)), zap.Error(err))
		}
	}
}

// StreamingEnabled reports whether streaming import is wired (a session store
// and a provider registry are both present). The bootstrap layer uses it to
// decide whether to run the background connection-health loop.
func (s *Service) StreamingEnabled() bool {
	return s.sessions != nil && s.sources != nil
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
	s.markReauth(kind, false)
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
	s.markReauth(kind, false)
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
