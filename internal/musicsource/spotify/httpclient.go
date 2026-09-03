package spotify

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
)

// chromeClient returns an http.Client whose TLS ClientHello mimics Chrome.
// Spotify's edge rate-limits requests whose TLS fingerprint doesn't look like a
// browser's, even when the bearer token, client token, and headers are all
// correct. HTTP/1.1 only — the handshake is what's being checked, not the h2
// framing.
func chromeClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    60 * time.Second,
			TLSNextProto:       map[string]func(string, *tls.Conn) http.RoundTripper{}, // disable HTTP/2
			DisableCompression: false,
			DialContext:        dialer.DialContext,
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				raw, err := dialer.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					_ = raw.Close()
					return nil, err
				}
				u := utls.UClient(raw, &utls.Config{
					ServerName: host,
					NextProtos: []string{"http/1.1"},
				}, utls.HelloChrome_Auto)
				if err := u.HandshakeContext(ctx); err != nil {
					_ = raw.Close()
					return nil, err
				}
				return u, nil
			},
		},
	}
}
