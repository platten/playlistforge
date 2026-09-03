// Package tidal implements musicsource.Provider against TIDAL's private API.
//
// Sign-in uses the OAuth2 device authorization grant: AuthRequest asks TIDAL
// for a device code and hands back a verification URL for the desktop layer to
// open in the user's real browser, and Complete polls the token endpoint until
// the user approves. The web authorize flow is reCAPTCHA-gated and unusable
// from an embedded webview, so the device flow is the reliable path. The client
// credentials are the well-known ones shipped by community clients (python-tidal
// and others); this is unofficial use of an undocumented API and can break
// without notice.
package tidal

import (
	"context"
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
	defaultAuthBase = "https://auth.tidal.com"
	defaultAPIBase  = "https://api.tidal.com/v1"

	// clientID / clientSecret are the community device-flow ("TV") credentials
	// used by python-tidal and others.
	clientID     = "zU4XHVVkc2tDPo4t"
	clientSecret = "VJKhDFqJPqvsPVNBV6ukXTJmwlvbttP7wlMlrc72se4="

	scope           = "r_usr w_usr w_sub"
	deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"

	pageSize            = 50
	userAgent           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) PlaylistForge/1.0"
	defaultPollInterval = 2 * time.Second
)

// Provider is the TIDAL musicsource adapter. The zero value is not usable; call
// New.
type Provider struct {
	http            *http.Client
	authBase        string
	apiBase         string
	minPollInterval time.Duration

	mu      sync.Mutex
	pending pendingDevice // set by AuthRequest, consumed by Complete
}

type pendingDevice struct {
	deviceCode string
	interval   time.Duration
	expires    time.Time
}

// New returns a Provider talking to the live TIDAL endpoints.
func New() *Provider {
	return &Provider{
		http:            &http.Client{Timeout: 30 * time.Second},
		authBase:        defaultAuthBase,
		apiBase:         defaultAPIBase,
		minPollInterval: defaultPollInterval,
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

type deviceAuthResponse struct {
	DeviceCode              string  `json:"deviceCode"`
	UserCode                string  `json:"userCode"`
	VerificationURI         string  `json:"verificationUri"`
	VerificationURIComplete string  `json:"verificationUriComplete"`
	ExpiresIn               int     `json:"expiresIn"`
	Interval                float64 `json:"interval"`
}

// AuthRequest starts the device flow and returns the URL for the user to
// approve in their browser.
func (p *Provider) AuthRequest() (musicsource.AuthRequest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	form := url.Values{"client_id": {clientID}, "scope": {scope}}
	var dev deviceAuthResponse
	if err := p.postForm(ctx, p.authBase+"/v1/oauth2/device_authorization", form, &dev); err != nil {
		return musicsource.AuthRequest{}, fmt.Errorf("start tidal sign-in: %w", err)
	}
	if dev.DeviceCode == "" || dev.VerificationURIComplete == "" {
		return musicsource.AuthRequest{}, errors.New("tidal returned an incomplete device authorization")
	}

	interval := time.Duration(dev.Interval * float64(time.Second))
	if interval < p.minPollInterval {
		interval = p.minPollInterval
	}
	expires := time.Now().Add(5 * time.Minute)
	if dev.ExpiresIn > 0 {
		expires = time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	}

	p.mu.Lock()
	p.pending = pendingDevice{deviceCode: dev.DeviceCode, interval: interval, expires: expires}
	p.mu.Unlock()

	return musicsource.AuthRequest{
		URL:           ensureScheme(dev.VerificationURIComplete),
		OpenInBrowser: true,
	}, nil
}

// Complete polls the token endpoint until the pending device request is
// approved, expires, or ctx is cancelled. The captured argument is unused.
func (p *Provider) Complete(ctx context.Context, _ string) (musicsource.Session, error) {
	p.mu.Lock()
	dev := p.pending
	p.pending = pendingDevice{}
	p.mu.Unlock()
	if dev.deviceCode == "" {
		return musicsource.Session{}, errors.New("no TIDAL sign-in is in progress")
	}

	interval := dev.interval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"device_code":   {dev.deviceCode},
		"grant_type":    {deviceCodeGrant},
		"scope":         {scope},
	}

	for {
		if !dev.expires.IsZero() && time.Now().After(dev.expires) {
			return musicsource.Session{}, errors.New("TIDAL sign-in expired before it was approved")
		}
		tok, again, err := p.pollToken(ctx, form)
		if err != nil {
			return musicsource.Session{}, err
		}
		if !again {
			return p.sessionFromToken(ctx, tok, "", "")
		}
		select {
		case <-ctx.Done():
			return musicsource.Session{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// pollToken makes one device-code token request. again is true while the user
// has not acted yet ("authorization_pending" / "slow_down").
func (p *Provider) pollToken(ctx context.Context, form url.Values) (oauthResponse, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.authBase+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return oauthResponse{}, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		return oauthResponse{}, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var tok oauthResponse
		if err := json.Unmarshal(body, &tok); err != nil {
			return oauthResponse{}, false, fmt.Errorf("decode tidal token: %w", err)
		}
		return tok, false, nil
	}

	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &errBody)
	switch errBody.Error {
	case "authorization_pending", "slow_down", "":
		return oauthResponse{}, true, nil
	case "expired_token":
		return oauthResponse{}, false, errors.New("TIDAL sign-in expired before it was approved")
	case "access_denied":
		return oauthResponse{}, false, errors.New("the TIDAL sign-in request was declined")
	default:
		return oauthResponse{}, false, fmt.Errorf("tidal sign-in: %s", errBody.Error)
	}
}

// oauthResponse covers the device-code and refresh token responses. Device-flow
// responses do not include the user object, so identity comes from /sessions.
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

// VerifySession confirms the access token is still accepted by asking for the
// current session record — a cheap call with no pagination. A 401 comes back as
// ErrNotConnected via do; a near-expiry token is refreshed by the caller before
// this runs.
func (p *Provider) VerifySession(ctx context.Context, s musicsource.Session) error {
	t, err := decode(s)
	if err != nil {
		return err
	}
	var sess sessionsResponse
	return p.getJSON(ctx, t.AccessToken, t.CountryCode, "/sessions", nil, &sess)
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
		// Transport failures (DNS, refused, TLS, client timeout) mean the
		// service could not be reached. Keep context.Canceled matchable so a
		// cancelled sync is still reported as cancelled, not as an outage.
		return fmt.Errorf("%w: reach tidal: %w", musicsource.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return musicsource.ErrNotConnected
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: tidal API %s", musicsource.ErrUnavailable, resp.Status)
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

func ensureScheme(u string) string {
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return "https://" + u
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
