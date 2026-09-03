// Package spotify implements musicsource.Provider against Spotify.
//
// Sign-in reuses the Spotify web player rather than a registered OAuth app:
// the desktop webview loads the Spotify login, and once open.spotify.com has
// minted its own web-player access token the capture probe reads it back out
// (from the /api/token response or web storage). That token has broad read
// scope but only lasts ~1 hour and carries no refresh token, so Refresh is a
// no-op that reports the session as disconnected once it expires — the user
// signs in again. Playlist reads then use the ordinary api.spotify.com/v1 REST
// endpoints. This is unofficial use of the web player's credentials and can
// break without notice.
package spotify

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
	"time"

	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
)

const (
	defaultAPIBase = "https://api.spotify.com/v1"
	loginURL       = "https://accounts.spotify.com/login"

	playlistPageSize = 50
	trackPageSize    = 100
	// assumedTokenLife bounds how long a captured token is trusted when the
	// capture didn't include an expiry.
	assumedTokenLife = 50 * time.Minute
	userAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

// authExtractJS is evaluated in the sign-in window on an interval. On first run
// it hooks fetch/XHR to catch the web player's own access-token response and
// its "client-token" request header (Spotify throttles api.spotify.com hard
// without the latter). Every run it returns
// {"accessToken":"…","accessTokenExpirationTimestampMs":N,"clientToken":"…"} as
// a string once an access token is seen (hook or web storage), else "".
const authExtractJS = `(function(){try{
if(!window.__pfSpInit){
  window.__pfSpInit=1;
  var save=function(txt){try{var o=JSON.parse(txt);if(o&&typeof o.accessToken==="string"&&o.accessToken){window.__pfSpTok={accessToken:o.accessToken,exp:o.accessTokenExpirationTimestampMs||0};}}catch(e){}};
  var isTok=function(u){u=""+u;return u.indexOf("api/token")>-1||u.indexOf("get_access_token")>-1;};
  var grabCT=function(a){try{if(window.__pfSpCT)return;var h=(a&&a[1]&&a[1].headers)||(a&&a[0]&&a[0].headers);if(!h)return;var v="";if(typeof h.get==="function"){v=h.get("client-token")||h.get("Client-Token")||"";}else{for(var k in h){if(String(k).toLowerCase()==="client-token"){v=h[k];break;}}}if(v)window.__pfSpCT=String(v);}catch(e){}};
  try{var of=window.fetch;if(of){window.fetch=function(){var a=arguments;grabCT(a);return of.apply(this,a).then(function(r){try{if(isTok((a[0]&&a[0].url)||a[0]||"")){r.clone().text().then(save);}}catch(e){}return r;});};}}catch(e){}
  try{var OX=window.XMLHttpRequest;if(OX){window.XMLHttpRequest=function(){var x=new OX();var ou=x.open,os=x.setRequestHeader;x.open=function(m,u){this.__pfu=""+u;return ou.apply(this,arguments);};x.setRequestHeader=function(n,v){try{if(String(n).toLowerCase()==="client-token"&&v&&!window.__pfSpCT){window.__pfSpCT=String(v);}}catch(e){}return os.apply(this,arguments);};x.addEventListener("load",function(){try{if(this.__pfu&&isTok(this.__pfu)){save(this.responseText);}}catch(e){}});return x;};}}catch(e){}
}
var t=window.__pfSpTok;
if(!t){var scan=function(store){try{for(var i=0;i<store.length;i++){var raw=store.getItem(store.key(i));if(!raw||raw.indexOf("accessToken")<0){continue;}try{var o=JSON.parse(raw);if(o&&typeof o.accessToken==="string"&&o.accessToken){return {accessToken:o.accessToken,exp:o.accessTokenExpirationTimestampMs||0};}}catch(e){}}}catch(e){}return null;};t=scan(window.localStorage)||scan(window.sessionStorage);}
if(!t){return "";}
if(!window.__pfSpSeenAt){window.__pfSpSeenAt=Date.now();}
// Hold the capture until the client token is seen (the web player sends it on
// its own api requests), giving up after ~8s so sign-in never stalls.
if(!window.__pfSpCT && Date.now()-window.__pfSpSeenAt < 8000){return "";}
return JSON.stringify({accessToken:t.accessToken,accessTokenExpirationTimestampMs:t.exp,clientToken:window.__pfSpCT||""});
}catch(e){return "";}})()`

// Provider is the Spotify musicsource adapter. The zero value is not usable;
// call New.
type Provider struct {
	http     *http.Client
	apiBase  string
	loginURL string
}

// New returns a Provider talking to the live Spotify endpoints.
func New() *Provider {
	return &Provider{
		http:     &http.Client{Timeout: 30 * time.Second},
		apiBase:  defaultAPIBase,
		loginURL: loginURL,
	}
}

// Kind identifies this provider.
func (p *Provider) Kind() musicsource.Kind { return musicsource.KindSpotify }

// token is the JSON persisted in musicsource.Session.Raw.
type token struct {
	AccessToken string `json:"accessToken"`
	ClientToken string `json:"clientToken,omitempty"`
	UserID      string `json:"userId"`
}

// AuthRequest points the webview at the Spotify login and captures the
// web-player token once the player boots.
func (p *Provider) AuthRequest() (musicsource.AuthRequest, error) {
	return musicsource.AuthRequest{
		URL:       p.loginURL,
		ExtractJS: authExtractJS,
		Width:     1200,
		Height:    900,
	}, nil
}

// capturedToken is what the probe posts back.
type capturedToken struct {
	AccessToken      string `json:"accessToken"`
	ExpirationMillis int64  `json:"accessTokenExpirationTimestampMs"`
	ClientToken      string `json:"clientToken"`
}

// Complete turns the captured token into a session.
func (p *Provider) Complete(ctx context.Context, captured string) (musicsource.Session, error) {
	captured = strings.TrimSpace(captured)
	var ct capturedToken
	if err := json.Unmarshal([]byte(captured), &ct); err != nil || ct.AccessToken == "" {
		// The probe may hand back a bare token string instead of the blob.
		ct = capturedToken{AccessToken: strings.Trim(captured, `"`)}
	}
	if ct.AccessToken == "" {
		return musicsource.Session{}, errors.New("Spotify sign-in returned no access token")
	}

	t := token{AccessToken: ct.AccessToken, ClientToken: ct.ClientToken}

	// Only a definitive 401 means the capture is bad; a 429 or a network
	// hiccup on this one cosmetic call must not sink an otherwise-good sign-in
	// (Spotify rate-limits the web-player token hard right after the player's
	// own boot burst).
	me, err := p.currentUser(ctx, t)
	if errors.Is(err, musicsource.ErrNotConnected) {
		return musicsource.Session{}, errors.New("Spotify sign-in captured an invalid token")
	}

	expiry := time.Now().Add(assumedTokenLife)
	if ct.ExpirationMillis > 0 {
		if exp := time.UnixMilli(ct.ExpirationMillis); exp.After(time.Now()) {
			expiry = exp
		}
	}
	display := me.DisplayName
	if display == "" {
		display = me.ID
	}
	if display == "" {
		display = "Spotify listener"
	}

	t.UserID = me.ID
	raw, err := json.Marshal(t)
	if err != nil {
		return musicsource.Session{}, err
	}
	return musicsource.Session{
		Kind:        musicsource.KindSpotify,
		Raw:         raw,
		DisplayName: display,
		ExpiresAt:   expiry,
	}, nil
}

// Refresh cannot renew a web-player token (it has no refresh token), so once
// the session is near expiry it is reported as disconnected and the user signs
// in again.
func (p *Provider) Refresh(_ context.Context, _ musicsource.Session) (musicsource.Session, error) {
	return musicsource.Session{}, musicsource.ErrNotConnected
}

type spotifyUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func (p *Provider) currentUser(ctx context.Context, t token) (spotifyUser, error) {
	// Cosmetic (display name only) and best-effort, so keep it short.
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var me spotifyUser
	if err := p.get(ctx, t, "/me", nil, &me); err != nil {
		return spotifyUser{}, err
	}
	return me, nil
}

// ListPlaylists returns every playlist in the user's library (owned and
// followed).
func (p *Provider) ListPlaylists(ctx context.Context, s musicsource.Session) ([]musicsource.RemotePlaylist, error) {
	t, err := decode(s)
	if err != nil {
		return nil, err
	}
	var out []musicsource.RemotePlaylist
	for offset := 0; ; offset += playlistPageSize {
		q := url.Values{
			"limit":  {strconv.Itoa(playlistPageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		var page struct {
			Total int `json:"total"`
			Items []struct {
				ID           string              `json:"id"`
				Name         string              `json:"name"`
				Description  string              `json:"description"`
				SnapshotID   string              `json:"snapshot_id"`
				Tracks       struct{ Total int } `json:"tracks"`
				ExternalURLs struct {
					Spotify string `json:"spotify"`
				} `json:"external_urls"`
			} `json:"items"`
		}
		if err := p.get(ctx, t, "/me/playlists", q, &page); err != nil {
			return nil, fmt.Errorf("list Spotify playlists: %w", err)
		}
		for _, it := range page.Items {
			if it.ID == "" {
				continue
			}
			link := it.ExternalURLs.Spotify
			if link == "" {
				link = "https://open.spotify.com/playlist/" + it.ID
			}
			out = append(out, musicsource.RemotePlaylist{
				ExternalID:  it.ID,
				Title:       it.Name,
				Description: it.Description,
				TrackCount:  it.Tracks.Total,
				URL:         link,
				ETag:        it.SnapshotID,
			})
		}
		if len(page.Items) == 0 || offset+playlistPageSize >= page.Total {
			break
		}
	}
	return out, nil
}

// PlaylistTracks returns the ordered tracks of one playlist.
func (p *Provider) PlaylistTracks(ctx context.Context, s musicsource.Session, externalID string) ([]playlist.Track, error) {
	t, err := decode(s)
	if err != nil {
		return nil, err
	}
	const fields = "total,items(track(id,name,is_local,artists(name),album(name,release_date),external_ids(isrc)))"
	path := "/playlists/" + url.PathEscape(externalID) + "/tracks"

	var tracks []playlist.Track
	for offset := 0; ; offset += trackPageSize {
		q := url.Values{
			"limit":  {strconv.Itoa(trackPageSize)},
			"offset": {strconv.Itoa(offset)},
			"fields": {fields},
		}
		var page struct {
			Total int `json:"total"`
			Items []struct {
				Track *spotifyTrack `json:"track"`
			} `json:"items"`
		}
		if err := p.get(ctx, t, path, q, &page); err != nil {
			return nil, fmt.Errorf("read Spotify playlist tracks: %w", err)
		}
		for _, it := range page.Items {
			if it.Track == nil || it.Track.IsLocal || it.Track.ID == "" {
				continue
			}
			tracks = append(tracks, it.Track.toTrack(len(tracks)+1))
		}
		if len(page.Items) == 0 || offset+trackPageSize >= page.Total {
			break
		}
	}
	return tracks, nil
}

type spotifyTrack struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsLocal bool   `json:"is_local"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
	} `json:"album"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
}

func (t *spotifyTrack) toTrack(pos int) playlist.Track {
	var artists []string
	for _, a := range t.Artists {
		if a.Name != "" {
			artists = append(artists, a.Name)
		}
	}
	tr := playlist.Track{
		ID:       t.ID,
		Position: pos,
		Title:    t.Name,
		Artists:  artists,
		Album:    t.Album.Name,
		ISRC:     cleanPtr(t.ExternalIDs.ISRC),
	}
	if y := yearOf(t.Album.ReleaseDate); y > 0 {
		tr.ReleaseYear = &y
	}
	return tr
}

// --- HTTP plumbing -----------------------------------------------------------

func decode(s musicsource.Session) (token, error) {
	var t token
	if err := json.Unmarshal(s.Raw, &t); err != nil {
		return token{}, fmt.Errorf("decode Spotify session: %w", err)
	}
	if t.AccessToken == "" {
		return token{}, musicsource.ErrNotConnected
	}
	return t, nil
}

func (p *Provider) get(ctx context.Context, t token, path string, q url.Values, out any) error {
	endpoint := p.apiBase + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+t.AccessToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Language", "en")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("App-Platform", "WebPlayer")
		if t.ClientToken != "" {
			// Spotify throttles api.spotify.com far harder for requests
			// without the web player's client token.
			req.Header.Set("Client-Token", t.ClientToken)
		}

		resp, err := p.http.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return musicsource.ErrNotConnected
		case resp.StatusCode == http.StatusTooManyRequests && attempt < 2:
			wait := retryAfter(resp.Header.Get("Retry-After"))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			return fmt.Errorf("spotify API %s: %s", resp.Status, snippet(body))
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode Spotify response: %w", err)
		}
		return nil
	}
}

// --- helpers ---------------------------------------------------------------

func retryAfter(header string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && n > 0 {
		if n > 30 {
			n = 30
		}
		return time.Duration(n) * time.Second
	}
	return time.Second
}

func cleanPtr(s string) *string {
	trimmed := strings.TrimSpace(s)
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
