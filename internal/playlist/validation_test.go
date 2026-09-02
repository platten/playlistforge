package playlist

// Tests for the dependency-free domain rules: Effort.Valid, the
// ValidateGenerateRequest bounds (prompt length, allowed track count, effort,
// reference limit), ValidateGenerated (exact count, empty title/artist,
// duplicate detection, position assignment), the normalized title/artist
// duplicate key, and EstimateUsage arithmetic including the cached-token
// discount and the per-call web-search fee.

import (
	"errors"
	"testing"
	"time"
)

func TestEffortValid(t *testing.T) {
	for _, effort := range []Effort{EffortMedium, EffortHigh, EffortXHigh, EffortMax} {
		if !effort.Valid() {
			t.Fatalf("expected %s to be valid", effort)
		}
	}
	if Effort("fast").Valid() {
		t.Fatal("unexpected valid effort")
	}
}

func TestValidateGenerateRequest(t *testing.T) {
	valid := GenerateRequest{Prompt: "warm jazz", TrackCount: 20, Effort: EffortMedium}
	if err := ValidateGenerateRequest(valid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*GenerateRequest)
		want   error
	}{
		{"short prompt", func(r *GenerateRequest) { r.Prompt = "  x " }, ErrInvalidPrompt},
		{"long prompt", func(r *GenerateRequest) { r.Prompt = string(make([]rune, MaxPromptLen+1)) }, ErrInvalidPrompt},
		{"count", func(r *GenerateRequest) { r.TrackCount = 25 }, ErrInvalidTrackCount},
		{"effort", func(r *GenerateRequest) { r.Effort = "low" }, ErrInvalidEffort},
		{"references", func(r *GenerateRequest) { r.ReferenceIDs = make([]string, 11) }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := valid
			tc.mutate(&request)
			err := ValidateGenerateRequest(request)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.want == nil && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestValidateGeneratedAndNormalizedKey(t *testing.T) {
	generated := GeneratedPlaylist{Title: " Mix ", Tracks: []Track{{Title: " Café! ", Artists: []string{" Artist One "}, Album: " A ", Rationale: " why "}, {Title: "Other", Artists: []string{"B"}}}}
	if err := ValidateGenerated(generated, 2); err != nil {
		t.Fatal(err)
	}
	if generated.Tracks[0].Position != 1 || generated.Tracks[0].Title != "Café!" {
		t.Fatalf("not normalized: %#v", generated.Tracks[0])
	}
	if got := NormalizedTrackKey(generated.Tracks[0]); got != "café|artistone" {
		t.Fatalf("key = %q", got)
	}
	cases := []GeneratedPlaylist{
		{Tracks: generated.Tracks},
		{Title: "x", Tracks: generated.Tracks[:1]},
		{Title: "x", Tracks: []Track{{Artists: []string{"a"}}, {Title: "b", Artists: []string{"b"}}}},
		{Title: "x", Tracks: []Track{{Title: "a"}, {Title: "b", Artists: []string{"b"}}}},
		{Title: "x", Tracks: []Track{{Title: "a", Artists: []string{""}}, {Title: "b", Artists: []string{"b"}}}},
		{Title: "x", Tracks: []Track{{Title: "Same", Artists: []string{"A"}}, {Title: " same! ", Artists: []string{"a"}}}},
	}
	for i, item := range cases {
		if err := ValidateGenerated(item, 2); err == nil {
			t.Fatalf("case %d expected error", i)
		}
	}
}

func TestEstimateUsage(t *testing.T) {
	fee := .01
	input := Usage{InputTokens: 1000, CachedTokens: 200, OutputTokens: 500, WebSearchCalls: 2}
	got := EstimateUsage(input, Pricing{Version: "test", InputPerMillion: 4, CachedInputPerMillion: .4, OutputPerMillion: 20, WebSearchPerCall: &fee})
	want := float64(800)/1e6*4 + float64(200)/1e6*.4 + float64(500)/1e6*20 + .02
	if got.EstimatedCostUSD != want || !got.SearchFeeKnown || got.PricingVersion != "test" || got.CreatedAt.IsZero() {
		t.Fatalf("unexpected usage: %#v", got)
	}
	created := time.Unix(1, 0)
	got = EstimateUsage(Usage{InputTokens: 1, CachedTokens: 2, CreatedAt: created}, Pricing{})
	if got.EstimatedCostUSD != 0 || got.SearchFeeKnown || !got.CreatedAt.Equal(created) {
		t.Fatalf("unexpected clamped usage: %#v", got)
	}
}
