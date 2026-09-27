package openaiapi

// Tests for API-key and model-access validation: local rejection of an empty or
// over-long key, catalog filtering, and errors when OpenAI rejects the key. A fake modelAPI stands in for the SDK; the SDK contract type is
// checked separately.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"playlistforge/internal/playlist"
)

type fakeModelAPI struct {
	key    string
	models []string
	err    error
}

func (f *fakeModelAPI) List(_ context.Context, key string) ([]string, error) {
	f.key = key
	return f.models, f.err
}

func TestValidator(t *testing.T) {
	api := &fakeModelAPI{models: []string{playlist.ModelGPTLuna}}
	validator := &Validator{api: api}
	if err := validator.Validate(context.Background(), "  test-key  "); err != nil {
		t.Fatal(err)
	}
	if api.key != "test-key" {
		t.Fatalf("key = %q", api.key)
	}
	for _, key := range []string{"", strings.Repeat("x", 513)} {
		if err := validator.Validate(context.Background(), key); err == nil {
			t.Fatalf("accepted %d-character key", len(key))
		}
	}
	api.err = errors.New("unauthorized")
	if err := validator.Validate(context.Background(), "bad-key"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("error = %v", err)
	}
	if NewValidator() == nil {
		t.Fatal("nil validator")
	}
}

func TestSDKModelContract(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.openai.com/v1/models" || request.Header.Get("Authorization") != "Bearer contract-key" {
			t.Fatalf("request = %s %#v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-6-luna","object":"model","created":0,"owned_by":"openai"}],"object":"list"}`)), Request: request}, nil
	})}
	if _, err := (sdkModelAPI{httpClient: httpClient}).List(context.Background(), "contract-key"); err != nil {
		t.Fatal(err)
	}
}

func TestModelsFilterAndOrder(t *testing.T) {
	api := &fakeModelAPI{models: []string{"whisper-1", playlist.ModelGPTLuna, playlist.ModelGPTSol, playlist.ModelGPTLuna}}
	validator := &Validator{api: api}
	models, err := validator.Models(context.Background(), "key")
	if err != nil || strings.Join(models, ",") != "gpt-6-sol,gpt-6-luna" {
		t.Fatalf("models=%v err=%v", models, err)
	}
	api.models = []string{"gpt-5.6-sol"}
	if err := validator.Validate(context.Background(), "key"); err == nil {
		t.Fatal("accepted unsupported catalog")
	}
}

func TestSDKModelError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	if _, err := (sdkModelAPI{httpClient: client}).List(context.Background(), "key"); err == nil {
		t.Fatal("expected network error")
	}
}
