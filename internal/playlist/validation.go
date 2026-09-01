package playlist

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var (
	// ErrInvalidPrompt indicates a description outside the supported length.
	ErrInvalidPrompt = errors.New("playlist description must be between 3 and 4000 characters")
	// ErrInvalidTrackCount indicates a size outside AllowedTrackCounts.
	ErrInvalidTrackCount = errors.New("track count must be one of 20, 30, 40, 50, 60, or 100")
	// ErrInvalidEffort indicates an unsupported reasoning effort.
	ErrInvalidEffort = errors.New("reasoning effort must be medium, high, xhigh, or max")
)

// ValidateGenerateRequest enforces domain limits before queuing paid work.
func ValidateGenerateRequest(req GenerateRequest) error {
	prompt := strings.TrimSpace(req.Prompt)
	if len([]rune(prompt)) < MinPromptLen || len([]rune(prompt)) > MaxPromptLen {
		return ErrInvalidPrompt
	}
	if _, ok := AllowedTrackCounts[req.TrackCount]; !ok {
		return ErrInvalidTrackCount
	}
	if !req.Effort.Valid() {
		return ErrInvalidEffort
	}
	if len(req.ReferenceIDs) > 10 {
		return errors.New("at most 10 reference playlists may be selected")
	}
	return nil
}

// ValidateGenerated normalizes provider output and rejects incomplete or
// duplicate tracks before assigning stable positions.
func ValidateGenerated(g GeneratedPlaylist, expected int) error {
	if strings.TrimSpace(g.Title) == "" {
		return errors.New("generated playlist title is empty")
	}
	if len(g.Tracks) != expected {
		return fmt.Errorf("generated playlist has %d tracks; expected %d", len(g.Tracks), expected)
	}
	seen := make(map[string]struct{}, len(g.Tracks))
	for i := range g.Tracks {
		track := &g.Tracks[i]
		track.Title = strings.TrimSpace(track.Title)
		track.Album = strings.TrimSpace(track.Album)
		track.Rationale = strings.TrimSpace(track.Rationale)
		if track.Title == "" || len(track.Artists) == 0 {
			return fmt.Errorf("track %d is missing title or artist", i+1)
		}
		for j := range track.Artists {
			track.Artists[j] = strings.TrimSpace(track.Artists[j])
			if track.Artists[j] == "" {
				return fmt.Errorf("track %d contains an empty artist", i+1)
			}
		}
		key := NormalizedTrackKey(*track)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate track at position %d: %s", i+1, track.Title)
		}
		seen[key] = struct{}{}
		track.Position = i + 1
	}
	return nil
}

// NormalizedTrackKey provides a conservative title/artist duplicate key.
func NormalizedTrackKey(track Track) string {
	return normalize(track.Title) + "|" + normalize(strings.Join(track.Artists, ","))
}

func normalize(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
