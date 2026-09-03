package app

// Tests for the streaming-connection use cases: Connections() reports
// available/connected/display-name per service, AuthRequest and CompleteAuth
// round-trip a session into the store, Disconnect clears it, and session()
// refreshes a token that is near expiry and persists the renewal. A fake
// session store and the musicsource/fake provider stand in.

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/musicsource"
	msfake "playlistforge/internal/musicsource/fake"
)

type fakeSessions struct {
	data      map[string][]byte
	getErr    error
	setErr    error
	deleteErr error
}

func newFakeSessions() *fakeSessions { return &fakeSessions{data: map[string][]byte{}} }

func (f *fakeSessions) Get(name string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	blob, ok := f.data[name]
	if !ok {
		return nil, musicsource.ErrNotConnected
	}
	return blob, nil
}
func (f *fakeSessions) Set(name string, blob []byte) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.data[name] = blob
	return nil
}
func (f *fakeSessions) Delete(name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.data, name)
	return nil
}
func (f *fakeSessions) Has(name string) bool { _, ok := f.data[name]; return ok }

func newConnService(t *testing.T, sessions sessionStore, reg musicsource.Registry) *Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	svc := New(ctx, nil, nil, nil, sessions, reg, zap.NewNop())
	t.Cleanup(func() { svc.Close(); cancel() })
	return svc
}

func TestConnectionsLifecycle(t *testing.T) {
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})

	status := svc.Connections()
	if len(status) != 2 {
		t.Fatalf("want 2 services, got %d", len(status))
	}
	if !status[0].Available || status[0].Connected || status[0].Kind != "tidal" {
		t.Fatalf("tidal before connect: %+v", status[0])
	}
	if status[1].Available || status[1].Kind != "qobuz" {
		t.Fatalf("qobuz should be unavailable: %+v", status[1])
	}

	req, err := svc.AuthRequest(musicsource.KindTIDAL)
	if err != nil || req.URL == "" {
		t.Fatalf("auth request: %+v %v", req, err)
	}
	if _, err := svc.AuthRequest(musicsource.KindQobuz); err == nil {
		t.Fatal("qobuz auth request should fail without a provider")
	}

	connected, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "captured-token")
	if err != nil || !connected.Connected || connected.DisplayName != "Fake User" {
		t.Fatalf("complete: %+v %v", connected, err)
	}
	if tidal.Completed != "captured-token" {
		t.Fatalf("provider not given the capture: %q", tidal.Completed)
	}
	if status := svc.Connections(); !status[0].Connected || status[0].DisplayName != "Fake User" {
		t.Fatalf("connections after connect: %+v", status[0])
	}

	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, ""); err == nil {
		t.Fatal("empty capture should error")
	}

	if err := svc.Disconnect(musicsource.KindTIDAL); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if svc.Connections()[0].Connected {
		t.Fatal("still connected after disconnect")
	}
}

func TestSessionRefresh(t *testing.T) {
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})

	// Store a session that expires almost immediately.
	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "tok"); err != nil {
		t.Fatal(err)
	}
	stale := musicsource.Session{Kind: musicsource.KindTIDAL, Raw: []byte(`{}`), DisplayName: "Fake User", ExpiresAt: time.Now().Add(10 * time.Second)}
	if err := svc.saveSession(stale); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.session(musicsource.KindTIDAL); err != nil {
		t.Fatalf("session: %v", err)
	}
	if tidal.Refreshed != 1 {
		t.Fatalf("expected one refresh, got %d", tidal.Refreshed)
	}
}

func TestConnectionEdgeCases(t *testing.T) {
	nearExpiry := func() musicsource.Session {
		return musicsource.Session{Kind: musicsource.KindTIDAL, Raw: []byte(`{}`), ExpiresAt: time.Now().Add(10 * time.Second)}
	}

	t.Run("save failure surfaces on connect", func(t *testing.T) {
		sessions := newFakeSessions()
		sessions.setErr = errors.New("keyring full")
		svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: msfake.New(musicsource.KindTIDAL)})
		if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "tok"); !errors.Is(err, sessions.setErr) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("disconnect failure surfaces", func(t *testing.T) {
		sessions := newFakeSessions()
		sessions.deleteErr = errors.New("delete blew up")
		svc := newConnService(t, sessions, musicsource.Registry{})
		if err := svc.Disconnect(musicsource.KindTIDAL); err == nil {
			t.Fatal("expected delete failure")
		}
	})

	t.Run("invalid kind", func(t *testing.T) {
		svc := newConnService(t, newFakeSessions(), musicsource.Registry{})
		if _, err := svc.AuthRequest(musicsource.Kind("spotify")); err == nil {
			t.Fatal("expected unknown-service error")
		}
	})

	t.Run("malformed stored session", func(t *testing.T) {
		sessions := newFakeSessions()
		sessions.data["tidal"] = []byte("not-json")
		svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: msfake.New(musicsource.KindTIDAL)})
		if _, err := svc.session(musicsource.KindTIDAL); err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("refresh error", func(t *testing.T) {
		sessions := newFakeSessions()
		tidal := msfake.New(musicsource.KindTIDAL)
		tidal.RefreshErr = errors.New("token revoked")
		svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})
		_ = svc.saveSession(nearExpiry())
		if _, err := svc.session(musicsource.KindTIDAL); err == nil {
			t.Fatal("expected refresh error")
		}
	})

	t.Run("save error after refresh", func(t *testing.T) {
		sessions := newFakeSessions()
		svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: msfake.New(musicsource.KindTIDAL)})
		_ = svc.saveSession(nearExpiry())
		sessions.setErr = errors.New("keyring full")
		if _, err := svc.session(musicsource.KindTIDAL); !errors.Is(err, sessions.setErr) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("near-expiry without a provider", func(t *testing.T) {
		sessions := newFakeSessions()
		svc := newConnService(t, sessions, musicsource.Registry{}) // no provider
		_ = svc.saveSession(nearExpiry())
		if _, err := svc.session(musicsource.KindTIDAL); err == nil {
			t.Fatal("expected error refreshing without a provider")
		}
	})
}

func TestCheckConnectionsFlagsReauth(t *testing.T) {
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})
	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "tok"); err != nil {
		t.Fatal(err)
	}

	// A healthy session verifies clean.
	svc.CheckConnections(context.Background())
	if tidal.VerifyCalls != 1 {
		t.Fatalf("expected one verify call, got %d", tidal.VerifyCalls)
	}
	if svc.Connections()[0].NeedsReauth {
		t.Fatal("healthy session should not need reauth")
	}

	// The service now rejects the token.
	tidal.VerifyErr = musicsource.ErrNotConnected
	svc.CheckConnections(context.Background())
	status := svc.Connections()[0]
	if !status.Connected || !status.NeedsReauth || status.DisplayName != "Fake User" {
		t.Fatalf("expected connected+needsReauth with a name, got %+v", status)
	}

	// A transient failure must not clear the reauth flag.
	tidal.VerifyErr = errors.New("network unreachable")
	svc.CheckConnections(context.Background())
	if !svc.Connections()[0].NeedsReauth {
		t.Fatal("transient verify error wrongly cleared the reauth flag")
	}

	// Reconnecting clears it.
	tidal.VerifyErr = nil
	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "tok2"); err != nil {
		t.Fatal(err)
	}
	if svc.Connections()[0].NeedsReauth {
		t.Fatal("reconnect should clear the reauth flag")
	}
}

func TestCheckConnectionsClearsFlagWhenDisconnected(t *testing.T) {
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	tidal.VerifyErr = musicsource.ErrNotConnected
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})
	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "tok"); err != nil {
		t.Fatal(err)
	}
	svc.CheckConnections(context.Background())
	if !svc.Connections()[0].NeedsReauth {
		t.Fatal("expected reauth flag set")
	}
	if err := svc.Disconnect(musicsource.KindTIDAL); err != nil {
		t.Fatal(err)
	}
	svc.CheckConnections(context.Background())
	if svc.Connections()[0].Connected || svc.Connections()[0].NeedsReauth {
		t.Fatalf("disconnected service should be clean: %+v", svc.Connections()[0])
	}
}

func TestSyncFlagsReauthOnAuthFailure(t *testing.T) {
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	tidal.ListErr = musicsource.ErrNotConnected
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})
	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncSource(context.Background(), musicsource.KindTIDAL); err == nil {
		t.Fatal("expected the sync to fail")
	}
	if !svc.Connections()[0].NeedsReauth {
		t.Fatal("a sync that hit ErrNotConnected should flag reauth")
	}
}

func TestStreamingEnabled(t *testing.T) {
	if newConnService(t, nil, nil).StreamingEnabled() {
		t.Fatal("no store/registry: streaming should be disabled")
	}
	full := newConnService(t, newFakeSessions(), musicsource.Registry{musicsource.KindTIDAL: msfake.New(musicsource.KindTIDAL)})
	if !full.StreamingEnabled() {
		t.Fatal("store + registry: streaming should be enabled")
	}
}

func TestCheckConnectionsSkipsKindWithoutProvider(t *testing.T) {
	sessions := newFakeSessions()
	svc := newConnService(t, sessions, musicsource.Registry{}) // empty registry
	// A stored session for a kind the registry no longer offers.
	if err := svc.saveSession(musicsource.Session{
		Kind: musicsource.KindTIDAL, Raw: []byte(`{"accessToken":"a"}`), DisplayName: "Ghost",
	}); err != nil {
		t.Fatal(err)
	}

	svc.CheckConnections(context.Background()) // must not panic on the missing provider

	status := svc.Connections()[0]
	if !status.Connected || status.DisplayName != "Ghost" {
		t.Fatalf("expected a connected ghost row, got %+v", status)
	}
}

func TestConnectionsSurfacesUnrefreshableSession(t *testing.T) {
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	tidal.RefreshErr = errors.New("refresh token revoked")
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal})

	// A stored session that is past the refresh threshold and can't be renewed.
	if err := svc.saveSession(musicsource.Session{
		Kind: musicsource.KindTIDAL, Raw: []byte(`{"accessToken":"a"}`),
		DisplayName: "Listener", ExpiresAt: time.Now().Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	status := svc.Connections()[0]
	if !status.Connected || !status.NeedsReauth || status.DisplayName != "Listener" {
		t.Fatalf("want connected+needsReauth+name, got %+v", status)
	}
}

func TestUndecodableSessionReportsReauth(t *testing.T) {
	sessions := newFakeSessions()
	sessions.data["tidal"] = []byte("not-json")
	svc := newConnService(t, sessions, musicsource.Registry{musicsource.KindTIDAL: msfake.New(musicsource.KindTIDAL)})

	// Connections keeps the row visible with no name it can recover.
	status := svc.Connections()[0]
	if !status.Connected || !status.NeedsReauth || status.DisplayName != "" {
		t.Fatalf("want connected+needsReauth, no name; got %+v", status)
	}
	// CheckConnections reaches the same conclusion via the session() failure.
	svc.markReauth(musicsource.KindTIDAL, false)
	svc.CheckConnections(context.Background())
	if !svc.Connections()[0].NeedsReauth {
		t.Fatal("CheckConnections should flag an undecodable session")
	}
}

func TestStartConnectionHealthIsSafeWithoutStreaming(t *testing.T) {
	svc := newConnService(t, nil, nil)
	svc.CheckConnections(context.Background()) // no store: must be a safe no-op
}

func TestConnectionsWithoutStreaming(t *testing.T) {
	svc := newConnService(t, nil, nil)
	for _, s := range svc.Connections() {
		if s.Available || s.Connected {
			t.Fatalf("no streaming wired: %+v", s)
		}
	}
	if err := svc.Disconnect(musicsource.KindTIDAL); err == nil {
		t.Fatal("disconnect should fail with no store")
	}
	if _, err := svc.AuthRequest(musicsource.KindTIDAL); err == nil {
		t.Fatal("auth request should fail with no registry")
	}
	if _, err := svc.CompleteAuth(context.Background(), musicsource.KindTIDAL, "x"); err == nil {
		t.Fatal("complete should fail with no registry")
	}
	if _, err := svc.session(musicsource.KindTIDAL); !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("session without store: %v", err)
	}
}
