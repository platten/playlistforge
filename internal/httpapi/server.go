// Package httpapi exposes the local application service and embedded frontend
// over a loopback-only HTTP interface.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
	"playlistforge/internal/playlist"
	"playlistforge/internal/storage"
	"playlistforge/internal/webui"
)

const (
	maxBodyBytes        = 1 << 20
	mutationHeaderName  = "X-Playlist-Forge"
	mutationHeaderValue = "1"
)

type credentialStore interface {
	Status() credentials.Status
	Set(string, bool) (credentials.Status, error)
	Delete() error
}

type keyValidator interface {
	Validate(context.Context, string) error
}

// Server owns HTTP routing, request validation, and browser security headers.
// Business behavior remains in app.Service.
type Server struct {
	app    *app.Service
	keys   credentialStore
	keyAPI keyValidator
	logger *zap.Logger
	static fs.FS
}

// New creates a Server backed by the compiled-in Vite distribution.
func New(service *app.Service, keys credentialStore, keyAPI keyValidator, logger *zap.Logger) (*Server, error) {
	assets, err := fs.Sub(webui.Assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}
	return &Server{app: service, keys: keys, keyAPI: keyAPI, logger: logger, static: assets}, nil
}

// Handler returns the complete API, SPA, recovery, and security middleware stack.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", s.config)
	mux.HandleFunc("/api/config/openai-key", s.key)
	mux.HandleFunc("/api/jobs/", s.jobs)
	mux.HandleFunc("/api/playlists", s.playlists)
	mux.HandleFunc("/api/playlists/", s.playlist)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	mux.HandleFunc("/", s.frontend)
	return s.security(s.recoverer(mux))
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"credential":   s.keys.Status(),
		"model":        playlist.ModelGPTSol,
		"trackCounts":  []int{20, 30, 40, 50, 60, 100},
		"efforts":      []playlist.Effort{playlist.EffortMedium, playlist.EffortHigh, playlist.EffortXHigh, playlist.EffortMax},
		"destinations": app.SupportedDestinations(),
		"pricing":      map[string]any{"version": playlist.CurrentPricing.Version, "inputPerMillion": playlist.CurrentPricing.InputPerMillion, "cachedInputPerMillion": playlist.CurrentPricing.CachedInputPerMillion, "outputPerMillion": playlist.CurrentPricing.OutputPerMillion, "webSearchFeeKnown": playlist.CurrentPricing.WebSearchPerCall != nil},
	})
}

func (s *Server) key(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var body struct {
			Key            string `json:"key"`
			AllowPlaintext bool   `json:"allowPlaintext"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			badRequest(w, err)
			return
		}
		if err := s.keyAPI.Validate(r.Context(), body.Key); err != nil {
			badRequest(w, err)
			return
		}
		status, err := s.keys.Set(body.Key, body.AllowPlaintext)
		if err != nil {
			badRequest(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		if err := s.keys.Delete(); err != nil {
			serverError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) playlists(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.app.List(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var request playlist.GenerateRequest
		if err := decodeJSON(w, r, &request); err != nil {
			badRequest(w, err)
			return
		}
		job, err := s.app.Generate(request)
		if err != nil {
			badRequest(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	if id == "" || strings.Contains(id, "/") {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		job, ok := s.app.GetJob(id)
		if !ok {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, job)
	case http.MethodDelete:
		if err := s.app.CancelJob(id); err != nil {
			badRequest(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodDelete)
	}
}

func (s *Server) playlist(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/playlists/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		notFound(w)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		item, err := s.app.Get(r.Context(), id)
		if err != nil {
			handleRepoError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}
	if len(parts) == 2 && parts[1] == "refine" && r.Method == http.MethodPost {
		var body struct {
			Prompt string          `json:"prompt"`
			Effort playlist.Effort `json:"effort"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			badRequest(w, err)
			return
		}
		job, err := s.app.Refine(id, body.Prompt, body.Effort)
		if err != nil {
			badRequest(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
		return
	}
	if len(parts) == 2 && parts[1] == "soundiiz" && r.Method == http.MethodPost {
		var body struct {
			Destinations []string `json:"destinations"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			badRequest(w, err)
			return
		}
		job, err := s.app.Handoff(id, body.Destinations)
		if err != nil {
			badRequest(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
		return
	}
	if len(parts) == 3 && parts[1] == "tracks" && r.Method == http.MethodDelete {
		item, err := s.app.DeleteTrack(r.Context(), id, parts[2])
		if err != nil {
			handleRepoError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}
	if len(parts) == 4 && parts[1] == "tracks" && parts[3] == "replace" && r.Method == http.MethodPost {
		var body struct {
			Prompt string          `json:"prompt"`
			Effort playlist.Effort `json:"effort"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			badRequest(w, err)
			return
		}
		job, err := s.app.Replace(id, parts[2], body.Prompt, body.Effort)
		if err != nil {
			badRequest(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
		return
	}
	notFound(w)
}

func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		// Client-side routes have no physical file; serve the SPA shell so React
		// can resolve them after refresh or direct navigation.
		data, err = fs.ReadFile(s.static, "index.html")
		name = "index.html"
	}
	if err != nil {
		notFound(w)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The app intentionally has no authentication because it is bound to
		// loopback. These controls defend the local API from hostile websites,
		// DNS rebinding, framing, and injected asset loads.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !validHost(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "invalid host")
			return
		}
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/api/") && strings.Contains(r.URL.Path, "..") {
			badRequest(w, errors.New("invalid path"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if r.Header.Get(mutationHeaderName) != mutationHeaderValue {
					writeError(w, http.StatusForbidden, "missing request protection header")
					return
				}
				if !safeOrigin(r) {
					writeError(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func validHost(hostport string) bool {
	// Accept only the address the listener owns. In particular, localhost is
	// not accepted because a hostile DNS answer must not cross this boundary.
	host := hostport
	if strings.Contains(hostport, ":") {
		var err error
		host, _, err = net.SplitHostPort(hostport)
		if err != nil {
			return false
		}
	}
	return host == "127.0.0.1"
}

func safeOrigin(r *http.Request) bool {
	// Non-browser clients may omit Origin, but browsers that send it must match
	// the exact loopback origin. Sec-Fetch-Site catches explicit cross-site use.
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == r.Host && parsed.Path == ""
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", zap.Any("panic", recovered))
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	// Strict decoding keeps typoed or future fields from being silently ignored
	// and bounds memory use before any business operation is queued.
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func badRequest(w http.ResponseWriter, err error) { writeError(w, http.StatusBadRequest, err.Error()) }
func serverError(w http.ResponseWriter, _ error) {
	writeError(w, http.StatusInternalServerError, "internal server error")
}
func notFound(w http.ResponseWriter) { writeError(w, http.StatusNotFound, "not found") }
func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
func handleRepoError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		notFound(w)
	} else {
		serverError(w, err)
	}
}
