package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"playlistforge/internal/playlist"
)

type modelAPI interface {
	Get(context.Context, string, string) error
}

type sdkModelAPI struct{ httpClient *http.Client }

func (s sdkModelAPI) Get(ctx context.Context, key, model string) error {
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithBaseURL("https://api.openai.com/v1")}
	if s.httpClient != nil {
		opts = append(opts, option.WithHTTPClient(s.httpClient))
	}
	client := openai.NewClient(opts...)
	_, err := client.Models.Get(ctx, model)
	return err
}

// Validator checks both API-key validity and access to the configured model.
type Validator struct {
	api modelAPI
}

// NewValidator creates a production validator against the fixed OpenAI endpoint.
func NewValidator() *Validator { return &Validator{api: sdkModelAPI{}} }

// Validate rejects malformed keys locally, then asks OpenAI for the configured model.
func (v *Validator) Validate(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 512 {
		return errors.New("API key is empty or too long")
	}
	if err := v.api.Get(ctx, key, playlist.ModelGPTSol); err != nil {
		return fmt.Errorf("OpenAI rejected the key or model access: %w", err)
	}
	return nil
}
