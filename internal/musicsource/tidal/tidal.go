// Package tidal implements musicsource.Provider against TIDAL's private API.
//
// Sign-in is the OAuth2 authorization-code flow with PKCE, driven through the
// desktop webview: AuthRequest builds the login.tidal.com URL and Complete
// exchanges the captured redirect for tokens. The client credentials are the
// well-known ones shipped by community clients (python-tidal and others); this
// is unofficial use of an undocumented API and can break without notice.
package tidal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
)

const (
	defaultLoginBase = "https://login.tidal.com"
	defaultAuthBase  = "https://auth.tidal.com"
	defaultAPIBase   = "https://api.tidal.com/v1"

	// clientID / clientSecret are the PKCE "limited input device" credentials
	// used by python-tidal and other community clients.
	clientID     = "6BDSRdpK9hqEBTgU"
	clientSecret = "xeuPmY7nbpZ9IIbLAcQ93shka1VNheUAqN6IcszjTG8="

	redirectURI = "https://tidal.com/android/login/auth"
	scope       = "r_usr w_usr w_sub"

	pageSize  = 50
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) PlaylistForge/1.0"
)

// Provider is the TIDAL musicsource adapter. The zero value is not usable; call
// New.
type Provider struct {
	http      *http.Client
	loginBase string
	authBase  string
	apiBase   string

	mu              sync.Mutex
	pendingVerifier string // set by AuthRequest, consumed by Complete
}

// New returns a Provider talking to the live TIDAL endpoints.
func New() *Provider {
	return &Provider{
		http:      &http.Client{Timeout: 30 * time.Second},
		loginBase: defaultLoginBase,
		authBase:  defaultAuthBase,
		apiBase:   defaultAPIBase,
	}
}

// Kind identifies this provider.
func (p *Provider) Kind() musicsource.Kind { return musicsource.KindTIDAL }

// token is the JSON persisted in musicsource.Session.Raw.
type token struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	UserID       string `json:"userId"`
	CountryCode  string `json:"countryCode"`
}

// AuthRequest builds the PKCE authorization URL and stashes the code verifier
// for the Complete call that follows.
func (p *Provider) AuthRequest() (musicsource.AuthRequest, error) {
	verifier, err := randomVerifier()
	if err != nil {
		return musicsource.AuthRequest{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	p.mu.Lock()
	p.pendingVerifier = verifier
	p.mu.Unlock()

	q := url.Values{
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"client_id":             {clientID},
		"lang":                  {"en"},
		"appMode":               {"android"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"restrict_signup":       {"true"},
		"scope":                 {scope},
	}
	return musicsource.AuthRequest{
		URL:            p.loginBase + "/authorize?" + q.Encode(),
		RedirectPrefix: redirectURI,
	}, nil
}

// oauthResponse covers both the code-exchange and refresh token responses. The
// user object is present on the initial exchange only.
type oauthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	User         struct {
		UserID      json.Number `json:"userId"`
		CountryCode string      `json:"countryCode"`
		Username    string      `json:"username"`
	} `json:"user"`
}

// Complete exchanges the captured redirect URL for a session.
func (p *Provider) Complete(ctx context.Context, captured string) (musicsource.Session, error) {
	u, err := url.Parse(strings.TrimSpace(captured))
	if err != nil {
		return musicsource.Session{}, fmt.Errorf("parse redirect: %w", err)
	}
	if e := u.Query().Get("error"); e != "" {
		return musicsource.Session{}, fmt.Errorf("tidal sign-in failed: %s", e)
	}
	code := u.Query().Get("code")
	if code == "" {
		return musicsource.Session{}, errors.New("tidal sign-in returned no authorization code")
	}

	p.mu.Lock()
	verifier := p.pendingVerifier
	p.pendingVerifier = ""
	p.mu.Unlock()
	if verifier == "" {
		return musicsource.Session{}, errors.New("no TIDAL sign-in is in progress")
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"scope":         {scope},
		"code_verifier": {verifier},
	}
	var tok oauthResponse
	if err := p.postForm(ctx, p.authBase+"/v1/oauth2/token", form, &tok); err != nil {
		return musicsource.Session{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	return p.sessionFromToken(ctx, tok, "", "")
}

// Refresh renews an access token from its refresh token. TIDAL refresh
// responses omit the user object and usually the refresh token itself, so both
// are carried over from the existing session.
func (p *Provider) Refresh(ctx context.Context, s musicsource.Session) (musicsource.Session, error) {
	var t token
	if err := json.Unmarshal(s.Raw, &t); err != nil {
		return musicsource.Session{}, fmt.Errorf("decode session: %w", err)
	}
	if t.RefreshToken == "" {
		return s, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {scope},
	}
	var tok oauthResponse
	if err := p.postForm(ctx, p.authBase+"/v1/oauth2/token", form, &tok); err != nil {
		return musicsource.Session{}, fmt.Errorf("refresh token: %w", err)
	}
	tok.User.UserID = json.Number(t.UserID)
	tok.User.CountryCode = t.CountryCode
	return p.sessionFromToken(ctx, tok, t.RefreshToken, s.DisplayName)
}

// sessionFromToken turns an OAuth response into a Session, filling any missing
// account facts from the /sessions endpoint.
func (p *Provider) sessionFromToken(ctx context.Context, tok oauthResponse, fallbackRefresh, fallbackDisplay string) (musicsource.Session, error) {
	if tok.AccessToken == "" {
		return musicsource.Session{}, errors.New("tidal returned no access token")
	}
	refresh := tok.RefreshToken
	if refresh == "" {
		refresh = fallbackRefresh
	}
	userID := tok.User.UserID.String()
	country := tok.User.CountryCode
	display := tok.User.Username

	if userID == "" || userID == "0" || country == "" {
		var sess sessionsResponse
		if err := p.getJSON(ctx, tok.AccessToken, "", "/sessions", nil, &sess); err != nil {
			return musicsource.Session{}, fmt.Errorf("read session: %w", err)
		}
		userID = strconv.FormatInt(sess.UserID, 10)
		country = sess.CountryCode
	}

	raw, err := json.Marshal(token{
		AccessToken:  tok.AccessToken,
		RefreshToken: refresh,
		UserID:       userID,
		CountryCode:  country,
	})
	if err != nil {
		return musicsource.Session{}, err
	}

	var expiry time.Time
	if tok.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if display == "" {
		display = fallbackDisplay
	}
	if display == "" {
		display = "TIDAL listener"
	}
	return musicsource.Session{
		Kind:        musicsource.KindTIDAL,
		Raw:         raw,
		DisplayName: display,
		ExpiresAt:   expiry,
	}, nil
}

type sessionsResponse struct {
	SessionID   string `json:"sessionId"`
	UserID      int64  `json:"userId"`
	CountryCode string `json:"countryCode"`
}

// ListPlaylists returns every playlist the user has created, paginating over
// the v1 users/{id}/playlists endpoint.
func (p *Provider) ListPlaylists(ctx context.Context, s musicsource.Session) ([]musicsource.RemotePlaylist, error) {
	t, err := decode(s)
	if err != nil {
		return nil, err
	}
	var out []musicsource.RemotePlaylist
	for offset := 0; ; offset += pageSize {
		q := url.Values{
			"limit":  {strconv.Itoa(pageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		var page playlistPage
		if err := p.getJSON(ctx, t.AccessToken, t.CountryCode, "/users/"+t.UserID+"/playlists", q, &page); err != nil {
			return nil, fmt.Errorf("list tidal playlists: %w", err)
		}
		for _, it := range page.Items {
			out = append(out, musicsource.RemotePlaylist{
				ExternalID:  it.UUID,
				Title:       it.Title,
				Description: it.Description,
				TrackCount:  it.NumberOfTracks,
				UpdatedAt:   parseTime(it.LastUpdated),
				URL:         "https://tidal.com/playlist/" + it.UUID,
			})
		}
		if len(page.Items) == 0 || offset+pageSize >= page.TotalNumberOfItems {
			break
		}
	}
	return out, nil
}

type playlistPage struct {
	TotalNumberOfItems int `json:"totalNumberOfItems"`
	Items              []struct {
		UUID           string `json:"uuid"`
		Title          string `json:"title"`
		Description    string `json:"description"`
		NumberOfTracks int    `json:"numberOfTracks"`
		LastUpdated    string `json:"lastUpdated"`
	} `json:"items"`
}

// PlaylistTracks returns the ordered tracks of one playlist.
func (p *Provider) PlaylistTracks(ctx context.Context, s musicsource.Session, externalID string) ([]playlist.Track, error) {
	t, err := decode(s)
	if err != nil {
		return nil, err
	}
	var tracks []playlist.Track
	for offset := 0; ; offset += pageSize {
		q := url.Values{
			"limit":  {strconv.Itoa(pageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		var page trackPage
		if err := p.getJSON(ctx, t.AccessToken, t.CountryCode, "/playlists/"+url.PathEscape(externalID)+"/tracks", q, &page); err != nil {
			return nil, fmt.Errorf("read tidal playlist tracks: %w", err)
		}
		for _, it := range page.Items {
			tracks = append(tracks, it.toTrack(len(tracks)+1))
		}
		if len(page.Items) == 0 || offset+pageSize >= page.TotalNumberOfItems {
			break
		}
	}
	return tracks, nil
}

type trackPage struct {
	TotalNumberOfItems int          `json:"totalNumberOfItems"`
	Items              []tidalTrack `json:"items"`
}

type tidalTrack struct {
	ID      json.Number `json:"id"`
	Title   string      `json:"title"`
	Version *string     `json:"version"`
	ISRC    *string     `json:"isrc"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Artist struct {
		Name string `json:"name"`
	} `json:"artist"`
	Album struct {
		Title string `json:"title"`
	} `json:"album"`
}

func (t tidalTrack) toTrack(pos int) playlist.Track {
	var artists []string
	for _, a := range t.Artists {
		if a.Name != "" {
			artists = append(artists, a.Name)
		}
	}
	if len(artists) == 0 && t.Artist.Name != "" {
		artists = append(artists, t.Artist.Name)
	}
	return playlist.Track{
		ID:       t.ID.String(),
		Position: pos,
		Title:    t.Title,
		Artists:  artists,
		Album:    t.Album.Title,
		Version:  cleanPtr(t.Version),
		ISRC:     cleanPtr(t.ISRC),
	}
}

// --- HTTP plumbing -------------------------------------------------------------

func decode(s musicsource.Session) (token, error) {
	var t token
	if err := json.Unmarshal(s.Raw, &t); err != nil {
		return token{}, fmt.Errorf("decode tidal session: %w", err)
	}
	if t.AccessToken == "" || t.UserID == "" {
		return token{}, musicsource.ErrNotConnected
	}
	return t, nil
}

func (p *Provider) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return p.do(req, out)
}

func (p *Provider) getJSON(ctx context.Context, accessToken, country, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if country != "" {
		q.Set("countryCode", country)
	}
	endpoint := p.apiBase + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return p.do(req, out)
}

func (p *Provider) do(req *http.Request, out any) error {
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return musicsource.ErrNotConnected
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("tidal API %s: %s", resp.Status, snippet(body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode tidal response: %w", err)
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

func randomVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate PKCE verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func cleanPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// parseTime accepts the ISO-8601 variants TIDAL uses ("...+0000" without a
// colon, with or without milliseconds) and returns the zero time on anything
// unrecognised so a single odd value cannot fail a whole sync.
func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
