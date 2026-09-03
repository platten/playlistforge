package spotify

import (
	"bytes"
	"io"
	"net/http"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// httpDoer is the slice of *http.Client the adapter actually uses. Keeping it an
// interface lets the live build swap in a browser-impersonating client while
// tests inject a plain net/http one.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// chromeClient returns an httpDoer whose TLS ClientHello, HTTP/2 SETTINGS and
// header ordering imitate Chrome. Spotify's edge answers 429 to any
// api.spotify.com request that doesn't look like it came from the web player —
// even when the bearer token, client token and headers are all correct — so a
// stock net/http client is refused regardless of what it sends. If the
// impersonating client can't be constructed we fall back to net/http rather
// than take Spotify offline entirely.
func chromeClient() httpDoer {
	inner, err := tlsclient.NewHttpClient(
		tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(profiles.Chrome_133),
		tlsclient.WithTimeoutSeconds(30),
		tlsclient.WithNotFollowRedirects(),
	)
	if err != nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return &chromeDoer{inner: inner}
}

// chromeDoer adapts a tls-client HttpClient — which speaks the bogdanfinn/fhttp
// request and response types — to the standard net/http shapes the rest of the
// adapter is written against.
type chromeDoer struct{ inner tlsclient.HttpClient }

// chromeHeaderOrder is the sequence Chrome's fetch stack emits request headers
// in. tls-client reads this pseudo-header, orders the wire accordingly, and
// strips it before sending.
var chromeHeaderOrder = []string{
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"authorization",
	"accept",
	"accept-language",
	"app-platform",
	"client-token",
	"origin",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-dest",
	"referer",
	"accept-encoding",
	"user-agent",
}

func (d *chromeDoer) Do(req *http.Request) (*http.Response, error) {
	var body io.Reader
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}

	fr, err := fhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), body)
	if err != nil {
		return nil, err
	}
	for name, values := range req.Header {
		for _, v := range values {
			fr.Header.Add(name, v)
		}
	}
	fr.Header[fhttp.HeaderOrderKey] = chromeHeaderOrder

	fresp, err := d.inner.Do(fr)
	if err != nil {
		return nil, err
	}

	hdr := make(http.Header, len(fresp.Header))
	for name, values := range fresp.Header {
		for _, v := range values {
			hdr.Add(name, v)
		}
	}
	return &http.Response{
		StatusCode: fresp.StatusCode,
		Status:     fresp.Status,
		Header:     hdr,
		Body:       fresp.Body,
		Request:    req,
	}, nil
}
