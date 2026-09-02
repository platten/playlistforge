package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
	"playlistforge/internal/playlist"
	"playlistforge/internal/soundiiz"
	"playlistforge/internal/storage"
)

type fakeKeys struct {
	status            credentials.Status
	setErr, deleteErr error
}

type fakeValidator struct{ err error }

func (f fakeValidator) Validate(context.Context, string) error { return f.err }

func (f *fakeKeys) Status() credentials.Status { return f.status }
func (f *fakeKeys) Set(value string, fallback bool) (credentials.Status, error) {
	if f.setErr != nil {
		return credentials.Status{}, f.setErr
	}
	f.status = credentials.Status{Configured: value != "", Storage: map[bool]string{true: "config", false: "keyring"}[fallback]}
	return f.status, nil
}
func (f *fakeKeys) Delete() error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.status = credentials.Status{Storage: "none"}
	return nil
}

type fakeGenerator struct{}

func (fakeGenerator) Generate(_ context.Context, req playlist.GenerateRequest, _ []playlist.Revision) (playlist.GeneratedPlaylist, playlist.Usage, error) {
	return generated(req.TrackCount), playlist.Usage{TotalTokens: 10}, nil
}
func (fakeGenerator) Refine(_ context.Context, rev playlist.Revision, _ string, _ playlist.Effort) (playlist.GeneratedPlaylist, playlist.Usage, error) {
	result := generated(len(rev.Tracks))
	result.Title = "Refined"
	return result, playlist.Usage{TotalTokens: 11}, nil
}
func (fakeGenerator) Replace(_ context.Context, _ playlist.Revision, _ string, _ string, _ playlist.Effort) (playlist.Track, playlist.Usage, error) {
	return playlist.Track{ID: "new", Title: "New", Artists: []string{"Artist"}, Rationale: "Fits"}, playlist.Usage{TotalTokens: 12}, nil
}
func generated(count int) playlist.GeneratedPlaylist {
	tracks := make([]playlist.Track, count)
	for i := range tracks {
		tracks[i] = playlist.Track{ID: fmt.Sprintf("t-%d", i), Title: fmt.Sprintf("Song %d", i), Artists: []string{"Artist"}, Rationale: "Fits"}
	}
	return playlist.GeneratedPlaylist{Title: "Mix", Description: "Desc", Tracks: tracks}
}

type fakeImporter struct{}

func (fakeImporter) Import(_ context.Context, rev playlist.Revision) (soundiiz.Result, error) {
	return soundiiz.Result{ShareURL: "https://soundiiz.com/go/import-playlist/token", ExpiresAt: time.Now().Add(time.Hour).Unix(), Tracks: len(rev.Tracks)}, nil
}

type testHarness struct {
	handler   http.Handler
	service   *app.Service
	keys      *fakeKeys
	validator *fakeValidator
	repo      *storage.Repository
}

func harness(t *testing.T) *testHarness {
	t.Helper()
	repo, err := storage.Open(filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(context.Background(), repo, fakeGenerator{}, fakeImporter{}, zap.NewNop())
	keys := &fakeKeys{status: credentials.Status{Configured: true, Storage: "keyring"}}
	validator := &fakeValidator{}
	server, err := New(service, keys, validator, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close(); _ = repo.Close() })
	return &testHarness{handler: server.Handler(), service: service, keys: keys, validator: validator, repo: repo}
}

func perform(handler http.Handler, method, path, body string, protect bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	req.Host = "127.0.0.1:8787"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if protect {
		req.Header.Set("X-Playlist-Forge", "1")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return value
}
func awaitJob(t *testing.T, service *app.Service, id string) playlist.Job {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		job, _ := service.GetJob(id)
		if job.Status != playlist.JobQueued && job.Status != playlist.JobRunning {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timeout")
	return playlist.Job{}
}

func TestConfigAndCredentials(t *testing.T) {
	h := harness(t)
	response := perform(h.handler, http.MethodGet, "/api/config", "", false)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%d %#v", response.Code, response.Header())
	}
	config := decode[map[string]any](t, response)
	if config["model"] != playlist.ModelGPTSol {
		t.Fatalf("config=%#v", config)
	}
	if _, exists := config["destinations"]; exists {
		t.Fatalf("obsolete destinations in config: %#v", config)
	}
	if got := perform(h.handler, http.MethodPost, "/api/config", "{}", true); got.Code != http.StatusMethodNotAllowed || got.Header().Get("Allow") != "GET" {
		t.Fatalf("method=%d", got.Code)
	}
	response = perform(h.handler, http.MethodPut, "/api/config/openai-key", `{"key":"sk-x","allowPlaintext":true}`, true)
	if response.Code != 200 || !h.keys.status.Configured || h.keys.status.Storage != "config" {
		t.Fatalf("put=%d %#v", response.Code, h.keys.status)
	}
	response = perform(h.handler, http.MethodDelete, "/api/config/openai-key", "", true)
	if response.Code != 204 || h.keys.status.Configured {
		t.Fatalf("delete=%d %#v", response.Code, h.keys.status)
	}
	h.validator.err = errors.New("invalid key")
	if got := perform(h.handler, http.MethodPut, "/api/config/openai-key", `{"key":"bad","allowPlaintext":false}`, true); got.Code != 400 {
		t.Fatalf("validation error=%d", got.Code)
	}
	h.validator.err = nil
	h.keys.setErr = errors.New("store failed")
	if got := perform(h.handler, http.MethodPut, "/api/config/openai-key", `{"key":"x","allowPlaintext":false}`, true); got.Code != 400 {
		t.Fatalf("set error=%d", got.Code)
	}
	h.keys.deleteErr = errors.New("delete failed")
	if got := perform(h.handler, http.MethodDelete, "/api/config/openai-key", "", true); got.Code != 500 {
		t.Fatalf("delete error=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodGet, "/api/config/openai-key", "", false); got.Code != 405 {
		t.Fatalf("method=%d", got.Code)
	}
}

func TestPlaylistWorkflow(t *testing.T) {
	h := harness(t)
	if got := perform(h.handler, http.MethodGet, "/api/playlists", "", false); got.Code != 200 || got.Body.String() != "[]\n" {
		t.Fatalf("list=%d %q", got.Code, got.Body.String())
	}
	if got := perform(h.handler, http.MethodPost, "/api/playlists", `{"prompt":"x","trackCount":20,"effort":"medium","referenceIds":[]}`, true); got.Code != 400 {
		t.Fatalf("invalid=%d", got.Code)
	}
	response := perform(h.handler, http.MethodPost, "/api/playlists", `{"prompt":"warm jazz","trackCount":20,"effort":"medium","referenceIds":[]}`, true)
	if response.Code != 202 {
		t.Fatalf("generate=%d %s", response.Code, response.Body.String())
	}
	job := decode[playlist.Job](t, response)
	done := awaitJob(t, h.service, job.ID)
	if done.Status != playlist.JobSucceeded {
		t.Fatalf("job=%#v", done)
	}
	response = perform(h.handler, http.MethodGet, "/api/jobs/"+job.ID, "", false)
	if response.Code != 200 {
		t.Fatalf("job get=%d", response.Code)
	}
	if got := perform(h.handler, http.MethodDelete, "/api/jobs/"+job.ID, "", true); got.Code != 400 {
		t.Fatalf("finished cancel=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodGet, "/api/jobs/missing", "", false); got.Code != 404 {
		t.Fatalf("missing job=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodPost, "/api/jobs/", "{}", true); got.Code != 404 {
		t.Fatalf("blank job=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodPost, "/api/jobs/id", "{}", true); got.Code != 405 {
		t.Fatalf("job method=%d", got.Code)
	}
	response = perform(h.handler, http.MethodGet, "/api/playlists/"+done.PlaylistID, "", false)
	item := decode[playlist.Playlist](t, response)
	if len(item.CurrentRevision.Tracks) != 20 {
		t.Fatalf("item=%#v", item)
	}
	if got := perform(h.handler, http.MethodPut, "/api/playlists/"+done.PlaylistID, "{}", true); got.Code != 405 {
		t.Fatalf("item method=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodGet, "/api/playlists/missing", "", false); got.Code != 404 {
		t.Fatalf("missing=%d", got.Code)
	}
	trackID := item.CurrentRevision.Tracks[0].ID
	response = perform(h.handler, http.MethodDelete, "/api/playlists/"+done.PlaylistID+"/tracks/"+trackID, "", true)
	if response.Code != 200 {
		t.Fatalf("remove=%d %s", response.Code, response.Body.String())
	}
	item = decode[playlist.Playlist](t, response)
	trackID = item.CurrentRevision.Tracks[0].ID
	response = perform(h.handler, http.MethodPost, "/api/playlists/"+done.PlaylistID+"/tracks/"+trackID+"/replace", `{"prompt":"new","effort":"medium"}`, true)
	if response.Code != 202 {
		t.Fatalf("replace=%d %s", response.Code, response.Body.String())
	}
	job = decode[playlist.Job](t, response)
	if awaitJob(t, h.service, job.ID).Status != playlist.JobSucceeded {
		t.Fatal("replace failed")
	}
	response = perform(h.handler, http.MethodPost, "/api/playlists/"+done.PlaylistID+"/refine", `{"prompt":"more adventurous","effort":"high"}`, true)
	if response.Code != 202 {
		t.Fatalf("refine=%d %s", response.Code, response.Body.String())
	}
	job = decode[playlist.Job](t, response)
	if awaitJob(t, h.service, job.ID).Status != playlist.JobSucceeded {
		t.Fatal("refine failed")
	}
	response = perform(h.handler, http.MethodPost, "/api/playlists/"+done.PlaylistID+"/soundiiz", `{}`, true)
	if response.Code != 202 {
		t.Fatalf("handoff=%d %s", response.Code, response.Body.String())
	}
	job = decode[playlist.Job](t, response)
	if awaitJob(t, h.service, job.ID).Status != playlist.JobSucceeded {
		t.Fatal("handoff failed")
	}
	if got := perform(h.handler, http.MethodPost, "/api/playlists/"+done.PlaylistID+"/unknown", "{}", true); got.Code != 404 {
		t.Fatalf("unknown=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodPatch, "/api/playlists", "{}", true); got.Code != 405 {
		t.Fatalf("list method=%d", got.Code)
	}
}

func TestInputAndSecurity(t *testing.T) {
	h := harness(t)
	request := httptest.NewRequest(http.MethodPost, "http://evil.test/api/playlists", strings.NewReader(`{}`))
	request.Host = "evil.test"
	request.Header.Set("X-Playlist-Forge", "1")
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, request)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("host=%d", rec.Code)
	}
	if got := perform(h.handler, http.MethodPost, "/api/playlists", `{}`, false); got.Code != 403 {
		t.Fatalf("header=%d", got.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/playlists", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:8787"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Playlist-Forge", "1")
	request.Header.Set("Origin", "http://evil.test")
	rec = httptest.NewRecorder()
	h.handler.ServeHTTP(rec, request)
	if rec.Code != 403 {
		t.Fatalf("origin=%d", rec.Code)
	}
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	h.handler.ServeHTTP(rec, request)
	if rec.Code != 403 {
		t.Fatalf("fetch site=%d", rec.Code)
	}
	if got := perform(h.handler, http.MethodPost, "/api/playlists", `{}`, true); got.Code != 400 {
		t.Fatalf("validation=%d", got.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/playlists", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:8787"
	req.Header.Set("X-Playlist-Forge", "1")
	rec = httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("content type=%d", rec.Code)
	}
	if got := perform(h.handler, http.MethodPost, "/api/playlists", `{"prompt":"valid","trackCount":20,"effort":"medium","unknown":true}`, true); got.Code != 400 {
		t.Fatalf("unknown json=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodPost, "/api/playlists", `{} {}`, true); got.Code != 400 {
		t.Fatalf("double json=%d", got.Code)
	}
	large := `{"prompt":"` + strings.Repeat("a", maxBodyBytes) + `","trackCount":20,"effort":"medium"}`
	if got := perform(h.handler, http.MethodPost, "/api/playlists", large, true); got.Code != 400 {
		t.Fatalf("large=%d", got.Code)
	}
	if !validHost("127.0.0.1") || !validHost("127.0.0.1:123") || validHost("localhost:123") || validHost("127.0.0.1:bad:port") {
		t.Fatal("host validation")
	}
}

func TestFrontendAndRecovery(t *testing.T) {
	h := harness(t)
	response := perform(h.handler, http.MethodGet, "/", "", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "<div id=\"root\"></div>") || response.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("index=%d %q", response.Code, response.Body.String())
	}
	if got := perform(h.handler, http.MethodGet, "/some/client/route", "", false); got.Code != 200 {
		t.Fatalf("fallback=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodHead, "/", "", false); got.Code != 200 || got.Body.Len() != 0 {
		t.Fatalf("head=%d %d", got.Code, got.Body.Len())
	}
	if got := perform(h.handler, http.MethodPost, "/", "", true); got.Code != 405 {
		t.Fatalf("method=%d", got.Code)
	}
	if got := perform(h.handler, http.MethodGet, "/api/no-such-route", "", false); got.Code != 404 {
		t.Fatalf("api fallback=%d", got.Code)
	}
	server := &Server{logger: zap.NewNop()}
	handler := server.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("panic=%d", rec.Code)
	}
}

func TestDecodeJSONAndHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/", bytes.NewBufferString(`{"value":1}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	var target struct {
		Value int `json:"value"`
	}
	if err := decodeJSON(rec, req, &target); err != nil || target.Value != 1 {
		t.Fatalf("decode=%d %v", target.Value, err)
	}
	if !safeOrigin(req) {
		t.Fatal("empty origin should be safe")
	}
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1/path")
	if safeOrigin(req) {
		t.Fatal("path origin accepted")
	}
	rec = httptest.NewRecorder()
	handleRepoError(rec, storage.ErrNotFound)
	if rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	rec = httptest.NewRecorder()
	handleRepoError(rec, errors.New("db"))
	if rec.Code != 500 {
		t.Fatal(rec.Code)
	}
}
