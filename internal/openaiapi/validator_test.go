package openaiapi

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
	key, model string
	err        error
}

func (f *fakeModelAPI) Get(_ context.Context, key, model string) error {
	f.key, f.model = key, model
	return f.err
}

func TestValidator(t *testing.T) {
	api := &fakeModelAPI{}
	validator := &Validator{api: api}
	if err := validator.Validate(context.Background(), "  test-key  "); err != nil {
		t.Fatal(err)
	}
	if api.key != "test-key" || api.model != playlist.ModelGPTSol {
		t.Fatalf("call = %q %q", api.key, api.model)
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
		if request.URL.String() != "https://api.openai.com/v1/models/gpt-5.6-sol" || request.Header.Get("Authorization") != "Bearer contract-key" {
			t.Fatalf("request = %s %#v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"gpt-5.6-sol","object":"model","created":0,"owned_by":"openai"}`)), Request: request}, nil
	})}
	if err := (sdkModelAPI{httpClient: httpClient}).Get(context.Background(), "contract-key", playlist.ModelGPTSol); err != nil {
		t.Fatal(err)
	}
}
