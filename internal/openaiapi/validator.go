package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"playlistforge/internal/playlist"
)

type modelAPI interface {
	List(context.Context, string) ([]string, error)
}

type sdkModelAPI struct{ httpClient *http.Client }

func (s sdkModelAPI) List(ctx context.Context, key string) ([]string, error) {
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithBaseURL("https://api.openai.com/v1")}
	if s.httpClient != nil {
		opts = append(opts, option.WithHTTPClient(s.httpClient))
	}
	client := openai.NewClient(opts...)
	page, err := client.Models.List(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(page.Data))
	for _, model := range page.Data {
		models = append(models, model.ID)
	}
	return models, nil
}

// Validator checks both API-key validity and access to supported models.
type Validator struct {
	api modelAPI
}

// NewValidator creates a production validator against the fixed OpenAI endpoint.
func NewValidator() *Validator { return &Validator{api: sdkModelAPI{}} }

// Validate accepts keys that can access at least one supported model.
func (v *Validator) Validate(ctx context.Context, key string) error {
	_, err := v.Models(ctx, key)
	return err
}

// Models fetches the account catalog and keeps models compatible with playlist generation.
func (v *Validator) Models(ctx context.Context, key string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 512 {
		return nil, errors.New("API key is empty or too long")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	available, err := v.api.List(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("OpenAI rejected the key or model access: %w", err)
	}
	models := []string{}
	for _, model := range []string{playlist.ModelGPTSol, playlist.ModelGPTLuna} {
		if slices.Contains(available, model) {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("no supported GPT-6 models available; this key needs access to gpt-6-sol or gpt-6-luna")
	}
	return models, nil
}
