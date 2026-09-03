// Package qobuz implements musicsource.Provider against Qobuz's private API.
//
// Sign-in reuses the Qobuz web player: the desktop webview loads
// play.qobuz.com/login and, once the player has stored a session, Complete
// reads the user_auth_token out of its localStorage "localuser" entry. The
// numeric app id needed for API calls is scraped from the web player's JS
// bundle, the technique used by community clients (qobuz-dl / spoofbuz). Reading
// playlists needs only the app id and the auth token — no request signing — so
// the app secret is not required here. This is unofficial use of an
// undocumented API and can break without notice.
package qobuz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
)

const (
	defaultAPIBase  = "https://www.qobuz.com/api.json/0.2"
	defaultPlayBase = "https://play.qobuz.com"

	pageSize  = 500
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) PlaylistForge/1.0"
)

var (
	bundleURLRe = regexp.MustCompile(`<script src="(/resources/[^"]+?/bundle\.js)"`)
	appIDRe     = regexp.MustCompile(`production:\{api:\{appId:"(\d+)"`)
)

// Provider is the Qobuz musicsource adapter. The zero value is not usable; call
// New.
type Provider struct {
	http     *http.Client
	apiBase  string
	playBase string

	mu    sync.Mutex
	appID string // cached bundle scrape, per process
}

// New returns a Provider talking to the live Qobuz endpoints.
func New() *Provider {
	return &Provider{
		http:     &http.Client{Timeout: 30 * time.Second},
		apiBase:  defaultAPIBase,
		playBase: defaultPlayBase,
	}
}

// Kind identifies this provider.
func (p *Provider) Kind() musicsource.Kind { return musicsource.KindQobuz }

// token is the JSON persisted in musicsource.Session.Raw.
type token struct {
	AppID     string `json:"appId"`
	AuthToken string `json:"authToken"`
	UserID    string `json:"userId"`
}

// authExtractJS is evaluated in the sign-in window on an interval. Once the
// Qobuz web player has stored a session it returns {"id":…,"token":"…"} as a
// string; it checks the known "localuser" entry first, then falls back to
// scanning local/session storage for any {id|user_id, token} object so a
// renamed key in a newer player build still works.
const authExtractJS = `(function(){try{
function pick(store){
  try{var s=store.getItem("localuser");if(s){var o=JSON.parse(s);if(o&&o.token){return JSON.stringify({id:o.id,token:o.token});}}}catch(e){}
  try{for(var i=0;i<store.length;i++){var k=store.key(i);var raw=store.getItem(k);
    if(!raw||raw.indexOf("token")<0){continue;}
    try{var v=JSON.parse(raw);if(v&&v.token&&(v.id||v.user_id)){return JSON.stringify({id:v.id||v.user_id,token:v.token});}}catch(e){}}}catch(e){}
  return "";
}
return pick(window.localStorage)||pick(window.sessionStorage)||"";
}catch(e){return "";}})()`

// AuthRequest points the webview at the Qobuz web player's login page and reads
// the stored session back out of its storage once the user is in.
func (p *Provider) AuthRequest() (musicsource.AuthRequest, error) {
	return musicsource.AuthRequest{
		URL:       p.playBase + "/login",
		ExtractJS: authExtractJS,
		// The Qobuz web player refuses to render below ~1024px wide.
		Width:  1200,
		Height: 860,
	}, nil
}

// localUser mirrors play.qobuz.com's localStorage "localuser" entry.
type localUser struct {
	ID    json.Number `json:"id"`
	Token string      `json:"token"`
}

// Complete turns the captured localStorage blob into a session.
func (p *Provider) Complete(ctx context.Context, captured string) (musicsource.Session, error) {
	var lu localUser
	if err := json.Unmarshal([]byte(strings.TrimSpace(captured)), &lu); err != nil {
		return musicsource.Session{}, fmt.Errorf("parse Qobuz sign-in state: %w", err)
	}
	if lu.Token == "" {
		return musicsource.Session{}, errors.New("Qobuz sign-in returned no auth token")
	}
	appID, err := p.resolveAppID(ctx)
	if err != nil {
		return musicsource.Session{}, err
	}

	display := "Qobuz listener"
	if name, err := p.currentUser(ctx, appID, lu.Token); err == nil && name != "" {
		display = name
	}

	raw, err := json.Marshal(token{AppID: appID, AuthToken: lu.Token, UserID: lu.ID.String()})
	if err != nil {
		return musicsource.Session{}, err
	}
	return musicsource.Session{
		Kind:        musicsource.KindQobuz,
		Raw:         raw,
		DisplayName: display,
	}, nil
}

// Refresh is a no-op: Qobuz auth tokens do not carry an expiry and are renewed
// only by signing in again.
func (p *Provider) Refresh(_ context.Context, s musicsource.Session) (musicsource.Session, error) {
	return s, nil
}

// VerifySession confirms the stored auth token still works by re-reading the
// account through /user/login — no playlist enumeration, so it stays clear of
// the rate limiter. A rejected token comes back as ErrNotConnected via apiGet.
func (p *Provider) VerifySession(ctx context.Context, s musicsource.Session) error {
	t, err := decode(s)
	if err != nil {
		return err
	}
	_, err = p.currentUser(ctx, t.AppID, t.AuthToken)
	return err
}

// resolveAppID scrapes the numeric app id from the web player's JS bundle,
// caching it for the life of the process.
func (p *Provider) resolveAppID(ctx context.Context) (string, error) {
	p.mu.Lock()
	cached := p.appID
	p.mu.Unlock()
	if cached != "" {
		return cached, nil
	}

	loginPage, err := p.fetch(ctx, p.playBase+"/login")
	if err != nil {
		return "", fmt.Errorf("load Qobuz login page: %w", err)
	}
	m := bundleURLRe.FindSubmatch(loginPage)
	if m == nil {
		return "", errors.New("could not locate the Qobuz web bundle")
	}
	bundle, err := p.fetch(ctx, p.playBase+string(m[1]))
	if err != nil {
		return "", fmt.Errorf("load Qobuz web bundle: %w", err)
	}
	am := appIDRe.FindSubmatch(bundle)
	if am == nil {
		return "", errors.New("could not find the Qobuz app id in the web bundle")
	}
	appID := string(am[1])

	p.mu.Lock()
	p.appID = appID
	p.mu.Unlock()
	return appID, nil
}

// currentUser resolves a display name for the connected account. Best-effort:
// any failure leaves the caller's default in place.
func (p *Provider) currentUser(ctx context.Context, appID, authToken string) (string, error) {
	q := url.Values{"app_id": {appID}, "user_auth_token": {authToken}}
	var out struct {
		User struct {
			DisplayName string `json:"display_name"`
			Login       string `json:"login"`
			Firstname   string `json:"firstname"`
			Lastname    string `json:"lastname"`
		} `json:"user"`
	}
	if err := p.apiGet(ctx, appID, authToken, "/user/login", q, &out); err != nil {
		return "", err
	}
	switch {
	case out.User.DisplayName != "":
		return out.User.DisplayName, nil
	case out.User.Firstname != "" || out.User.Lastname != "":
		return strings.TrimSpace(out.User.Firstname + " " + out.User.Lastname), nil
	default:
		return out.User.Login, nil
	}
}

// ListPlaylists returns every playlist the user owns.
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
		var page struct {
			Playlists struct {
				Items []qobuzPlaylist `json:"items"`
				Total int             `json:"total"`
			} `json:"playlists"`
		}
		if err := p.apiGet(ctx, t.AppID, t.AuthToken, "/playlist/getUserPlaylists", q, &page); err != nil {
			return nil, fmt.Errorf("list qobuz playlists: %w", err)
		}
		for _, it := range page.Playlists.Items {
			owner := it.Owner.ID.String()
			if t.UserID != "" && owner != "" && owner != t.UserID {
				continue // subscribed, not owned
			}
			id := it.ID.String()
			out = append(out, musicsource.RemotePlaylist{
				ExternalID:  id,
				Title:       it.Name,
				Description: it.Description,
				TrackCount:  it.TracksCount,
				UpdatedAt:   it.UpdatedAt.Time,
				URL:         "https://play.qobuz.com/playlist/" + id,
			})
		}
		if len(page.Playlists.Items) == 0 || offset+pageSize >= page.Playlists.Total {
			break
		}
	}
	return out, nil
}

type qobuzPlaylist struct {
	ID          json.Number `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	TracksCount int         `json:"tracks_count"`
	UpdatedAt   epochTime   `json:"updated_at"`
	Owner       struct {
		ID json.Number `json:"id"`
	} `json:"owner"`
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
			"playlist_id": {externalID},
			"extra":       {"tracks"},
			"limit":       {strconv.Itoa(pageSize)},
			"offset":      {strconv.Itoa(offset)},
		}
		var page struct {
			Tracks struct {
				Items []qobuzTrack `json:"items"`
				Total int          `json:"total"`
			} `json:"tracks"`
		}
		if err := p.apiGet(ctx, t.AppID, t.AuthToken, "/playlist/get", q, &page); err != nil {
			return nil, fmt.Errorf("read qobuz playlist tracks: %w", err)
		}
		for _, it := range page.Tracks.Items {
			tracks = append(tracks, it.toTrack(len(tracks)+1))
		}
		if len(page.Tracks.Items) == 0 || offset+pageSize >= page.Tracks.Total {
			break
		}
	}
	return tracks, nil
}

type qobuzTrack struct {
	ID        json.Number `json:"id"`
	Title     string      `json:"title"`
	Version   *string     `json:"version"`
	ISRC      *string     `json:"isrc"`
	Performer struct {
		Name string `json:"name"`
	} `json:"performer"`
	Album struct {
		Title  string `json:"title"`
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
		ReleaseDateOriginal string `json:"release_date_original"`
	} `json:"album"`
}

func (t qobuzTrack) toTrack(pos int) playlist.Track {
	var artists []string
	switch {
	case t.Performer.Name != "":
		artists = []string{t.Performer.Name}
	case t.Album.Artist.Name != "":
		artists = []string{t.Album.Artist.Name}
	}
	track := playlist.Track{
		ID:       t.ID.String(),
		Position: pos,
		Title:    t.Title,
		Artists:  artists,
		Album:    t.Album.Title,
		Version:  cleanPtr(t.Version),
		ISRC:     cleanPtr(t.ISRC),
	}
	if y := yearOf(t.Album.ReleaseDateOriginal); y > 0 {
		track.ReleaseYear = &y
	}
	return track
}

// --- HTTP plumbing -----------------------------------------------------------

func decode(s musicsource.Session) (token, error) {
	var t token
	if err := json.Unmarshal(s.Raw, &t); err != nil {
		return token{}, fmt.Errorf("decode qobuz session: %w", err)
	}
	if t.AuthToken == "" || t.AppID == "" {
		return token{}, musicsource.ErrNotConnected
	}
	return t, nil
}

func (p *Provider) apiGet(ctx context.Context, appID, authToken, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("app_id", appID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-App-Id", appID)
	if authToken != "" {
		req.Header.Set("X-User-Auth-Token", authToken)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		// Transport failure — the service could not be reached. Keep the inner
		// error matchable so a cancelled sync still reads as cancelled.
		return fmt.Errorf("%w: reach qobuz: %w", musicsource.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return musicsource.ErrNotConnected
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: qobuz API %s", musicsource.ErrUnavailable, resp.Status)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("qobuz API %s: %s", resp.Status, snippet(body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode qobuz response: %w", err)
	}
	return nil
}

func (p *Provider) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: reach qobuz: %w", musicsource.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: GET %s: %s", musicsource.ErrUnavailable, endpoint, resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", endpoint, resp.Status)
	}
	return body, nil
}

// --- helpers ---------------------------------------------------------------

// epochTime decodes a Qobuz timestamp, which is a Unix seconds integer (and,
// defensively, tolerates a quoted number, an RFC-3339 string, or null by
// yielding the zero time rather than failing the decode).
type epochTime struct{ time.Time }

func (e *epochTime) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if n, err := strconv.ParseInt(string(b), 10, 64); err == nil {
		if n > 0 {
			e.Time = time.Unix(n, 0).UTC()
		}
		return nil
	}
	if t, err := time.Parse(time.RFC3339, string(b)); err == nil {
		e.Time = t.UTC()
	}
	return nil
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

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
