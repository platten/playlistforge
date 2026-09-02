package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"go.uber.org/zap"

	"playlistforge/internal/playlist"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fakeKeys struct {
	key string
	err error
}

func (f fakeKeys) Get() (string, error) { return f.key, f.err }

type fakeAPI struct {
	response *responses.Response
	err      error
	params   responses.ResponseNewParams
	key      string
}

func (f *fakeAPI) Create(_ context.Context, key string, params responses.ResponseNewParams) (*responses.Response, error) {
	f.key = key
	f.params = params
	return f.response, f.err
}

func responseWithText(t *testing.T, text string) *responses.Response {
	t.Helper()
	encoded, _ := json.Marshal(text)
	raw := fmt.Sprintf(`{"id":"resp_1","model":"gpt-5.6-sol","output":[{"id":"search","type":"web_search_call","status":"completed"},{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":%s,"annotations":[]}]}],"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":0},"output_tokens":500,"output_tokens_details":{"reasoning_tokens":200},"total_tokens":1500}}`, encoded)
	var result responses.Response
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func generatedJSON(count int) string {
	tracks := make([]map[string]any, count)
	for i := range tracks {
		tracks[i] = map[string]any{"title": fmt.Sprintf("Song %d", i), "artists": []string{fmt.Sprintf("Artist %d", i)}, "album": "Album", "releaseYear": 2000 + i%20, "version": nil, "remasterYear": nil, "qualityNote": nil, "rationale": "It fits."}
	}
	data, _ := json.Marshal(map[string]any{"title": "Night Drive", "description": "A coherent arc", "tracks": tracks})
	return string(data)
}

func newTestClient(api *fakeAPI) *Client {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Client{keys: fakeKeys{key: "test-key"}, api: api, logger: zap.NewNop(), pricing: playlist.CurrentPricing, now: func() time.Time { return now }}
}

func TestSDKResponseContractAndConstructor(t *testing.T) {
	called := false
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		if request.URL.String() != "https://api.openai.com/v1/responses" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("request = %s %#v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0}}`)), Request: request}, nil
	})}
	response, err := (sdkAPI{httpClient: httpClient}).Create(context.Background(), "test-key", responses.ResponseNewParams{})
	if err != nil || response.ID != "resp" || !called {
		t.Fatalf("response=%#v err=%v called=%v", response, err, called)
	}
	if New(fakeKeys{key: "x"}, zap.NewNop()) == nil {
		t.Fatal("nil client")
	}
}

func TestGenerateSuccessAndParameters(t *testing.T) {
	api := &fakeAPI{response: responseWithText(t, generatedJSON(20))}
	client := newTestClient(api)
	result, usage, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "rainy night", TrackCount: 20, Effort: playlist.EffortHigh}, []playlist.Revision{{Title: "Reference"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tracks) != 20 || result.Tracks[0].ID == "" || result.Tracks[19].Position != 20 {
		t.Fatalf("result = %#v", result)
	}
	if usage.ResponseID != "resp_1" || usage.WebSearchCalls != 1 || usage.CachedTokens != 100 || usage.ReasoningTokens != 200 || usage.EstimatedCostUSD == 0 {
		t.Fatalf("usage = %#v", usage)
	}
	if api.key != "test-key" || string(api.params.Model) != playlist.ModelGPTSol || string(api.params.Reasoning.Effort) != "high" || len(api.params.Tools) != 1 {
		t.Fatalf("params = %#v", api.params)
	}
	if !strings.Contains(api.params.Input.OfString.Value, "REFERENCE PLAYLISTS") || !strings.Contains(api.params.Instructions.Value, "expert music curator") {
		t.Fatalf("input = %s", api.params.Input.OfString.Value)
	}
}

func TestGenerateErrors(t *testing.T) {
	client := newTestClient(&fakeAPI{})
	if _, _, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "x", TrackCount: 20, Effort: playlist.EffortMedium}, nil); !errors.Is(err, playlist.ErrInvalidPrompt) {
		t.Fatalf("validation = %v", err)
	}
	client.keys = fakeKeys{err: errors.New("no key")}
	if _, _, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "valid", TrackCount: 20, Effort: playlist.EffortMedium}, nil); err == nil {
		t.Fatal("expected key error")
	}
	client = newTestClient(&fakeAPI{err: errors.New("network")})
	if _, _, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "valid", TrackCount: 20, Effort: playlist.EffortMedium}, nil); err == nil || !strings.Contains(err.Error(), "OpenAI response") {
		t.Fatalf("api error = %v", err)
	}
	client = newTestClient(&fakeAPI{response: responseWithText(t, "")})
	if _, _, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "valid", TrackCount: 20, Effort: playlist.EffortMedium}, nil); err == nil {
		t.Fatal("expected empty response error")
	}
	client = newTestClient(&fakeAPI{response: responseWithText(t, "not-json")})
	if _, _, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "valid", TrackCount: 20, Effort: playlist.EffortMedium}, nil); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("decode error = %v", err)
	}
	client = newTestClient(&fakeAPI{response: responseWithText(t, generatedJSON(19))})
	if _, _, err := client.Generate(context.Background(), playlist.GenerateRequest{Prompt: "valid", TrackCount: 20, Effort: playlist.EffortMedium}, nil); err == nil || !strings.Contains(err.Error(), "expected 20") {
		t.Fatalf("count error = %v", err)
	}
}

func TestClassifyAPIError(t *testing.T) {
	tests := map[string]string{
		"credit_balance_exhausted":          "no prepaid credits",
		"project_spend_limit_exceeded":      "project has reached",
		"organization_spend_limit_exceeded": "organization has reached its spending",
		"organization_usage_limit_exceeded": "API usage limit",
	}
	for code, want := range tests {
		t.Run(code, func(t *testing.T) {
			provider := &openai.Error{Code: code}
			classified := classifyAPIError(provider)
			var billing *BillingError
			if !errors.As(classified, &billing) || billing.PublicCode() != code || !strings.Contains(billing.PublicMessage(), want) || !errors.Is(classified, provider) {
				t.Fatalf("classified=%#v", classified)
			}
		})
	}

	rateLimit := &openai.Error{Code: "rate_limit_exceeded"}
	if classified := classifyAPIError(rateLimit); classified != rateLimit {
		t.Fatalf("ordinary rate limit was classified as billing: %#v", classified)
	}
	network := errors.New("network")
	if classified := classifyAPIError(network); classified != network {
		t.Fatalf("non-API error changed: %#v", classified)
	}
}

func TestRefine(t *testing.T) {
	client := newTestClient(&fakeAPI{response: responseWithText(t, generatedJSON(2))})
	revision := playlist.Revision{Tracks: []playlist.Track{{Title: "A"}, {Title: "B"}}}
	result, _, err := client.Refine(context.Background(), revision, " more adventurous ", playlist.EffortXHigh)
	if err != nil || len(result.Tracks) != 2 {
		t.Fatalf("result = %#v, %v", result, err)
	}
	if _, _, err := client.Refine(context.Background(), revision, "x", playlist.EffortMedium); !errors.Is(err, playlist.ErrInvalidPrompt) {
		t.Fatalf("prompt error = %v", err)
	}
	if _, _, err := client.Refine(context.Background(), revision, "valid", "bad"); !errors.Is(err, playlist.ErrInvalidEffort) {
		t.Fatalf("effort error = %v", err)
	}
}

func TestReplace(t *testing.T) {
	trackJSON := `{"title":"New","artists":["Artist"],"album":"Record","releaseYear":2024,"version":null,"remasterYear":null,"qualityNote":"lossless","rationale":"Better flow"}`
	client := newTestClient(&fakeAPI{response: responseWithText(t, trackJSON)})
	revision := playlist.Revision{Title: "Mix", Tracks: []playlist.Track{{ID: "old", Position: 1, Title: "Old", Artists: []string{"A"}}}}
	result, usage, err := client.Replace(context.Background(), revision, "old", "", playlist.EffortMax)
	if err != nil || result.Title != "New" || result.ID == "" || result.Position != 1 || usage.TotalTokens != 1500 {
		t.Fatalf("result = %#v usage=%#v err=%v", result, usage, err)
	}
	if _, _, err := client.Replace(context.Background(), revision, "old", "x", "bad"); !errors.Is(err, playlist.ErrInvalidEffort) {
		t.Fatalf("effort = %v", err)
	}
	if _, _, err := client.Replace(context.Background(), revision, "missing", "x", playlist.EffortMedium); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing = %v", err)
	}
	client = newTestClient(&fakeAPI{response: responseWithText(t, `{}`)})
	if _, _, err := client.Replace(context.Background(), revision, "old", "x", playlist.EffortMedium); err == nil {
		t.Fatal("expected invalid replacement")
	}
	client = newTestClient(&fakeAPI{response: responseWithText(t, `not-json`)})
	if _, _, err := client.Replace(context.Background(), revision, "old", "x", playlist.EffortMedium); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("decode = %v", err)
	}
}

func TestHelpers(t *testing.T) {
	prompt := buildGenerationPrompt(playlist.GenerateRequest{Prompt: "  hello  ", TrackCount: 30}, nil)
	if !strings.Contains(prompt, "exactly 30") || strings.Contains(prompt, "REFERENCE") {
		t.Fatalf("prompt = %s", prompt)
	}
	curationPrompt := strings.ToLower(instructions + "\n" + prompt)
	for _, unwanted := range []string{"remaster", "bitrate", "lossless", "high-resolution", "fidelity"} {
		if strings.Contains(curationPrompt, unwanted) {
			t.Fatalf("curation prompt contains quality preference %q: %s", unwanted, curationPrompt)
		}
	}
	if schema := playlistSchema(20); schema["type"] != "object" {
		t.Fatalf("schema = %#v", schema)
	}
	if schema := trackSchema(); schema["additionalProperties"] != false {
		t.Fatalf("schema = %#v", schema)
	}
	client := newTestClient(&fakeAPI{})
	usage := client.usage(nil, playlist.EffortMedium, time.Now())
	if usage.Model != playlist.ModelGPTSol || usage.PricingVersion == "" {
		t.Fatalf("usage = %#v", usage)
	}
}
