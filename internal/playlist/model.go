// Package playlist defines the dependency-free domain model, validation rules,
// and ports shared by the application and infrastructure adapters.
package playlist

import "time"

const (
	// ModelGPTSol is intentionally fixed so generation, validation, and cost
	// reporting always describe the same model contract.
	ModelGPTSol = "gpt-5.6-sol"
	// MinPromptLen and MaxPromptLen bound user-supplied playlist instructions.
	MinPromptLen = 3
	MaxPromptLen = 4000
)

// AllowedTrackCounts is the set of playlist sizes accepted by the domain.
var AllowedTrackCounts = map[int]struct{}{20: {}, 30: {}, 40: {}, 50: {}, 60: {}, 100: {}}

// Effort is an OpenAI reasoning-effort value exposed by the application.
type Effort string

const (
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Valid reports whether the effort is supported by Playlist Forge.
func (e Effort) Valid() bool {
	switch e {
	case EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	default:
		return false
	}
}

// Track is a catalog-oriented recording candidate plus curation metadata.
type Track struct {
	ID           string   `json:"id"`
	Position     int      `json:"position"`
	Title        string   `json:"title"`
	Artists      []string `json:"artists"`
	Album        string   `json:"album"`
	ReleaseYear  *int     `json:"releaseYear"`
	Version      *string  `json:"version"`
	RemasterYear *int     `json:"remasterYear"`
	QualityNote  *string  `json:"qualityNote"`
	Rationale    string   `json:"rationale"`
}

// Usage records provider-reported consumption and the contemporaneous estimate.
type Usage struct {
	ResponseID       string    `json:"responseId"`
	Model            string    `json:"model"`
	Effort           Effort    `json:"effort"`
	InputTokens      int64     `json:"inputTokens"`
	CachedTokens     int64     `json:"cachedTokens"`
	OutputTokens     int64     `json:"outputTokens"`
	ReasoningTokens  int64     `json:"reasoningTokens"`
	TotalTokens      int64     `json:"totalTokens"`
	WebSearchCalls   int       `json:"webSearchCalls"`
	EstimatedCostUSD float64   `json:"estimatedCostUsd"`
	SearchFeeKnown   bool      `json:"searchFeeKnown"`
	PricingVersion   string    `json:"pricingVersion"`
	ElapsedMillis    int64     `json:"elapsedMillis"`
	CreatedAt        time.Time `json:"createdAt"`
}

// Revision is an immutable snapshot of one playlist edit.
type Revision struct {
	ID          string    `json:"id"`
	PlaylistID  string    `json:"playlistId"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Prompt      string    `json:"prompt"`
	TrackTarget int       `json:"trackTarget"`
	Model       string    `json:"model"`
	Effort      Effort    `json:"effort"`
	Tracks      []Track   `json:"tracks"`
	Usage       Usage     `json:"usage"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Playlist points to its active revision and latest Soundiiz handoff.
type Playlist struct {
	ID              string     `json:"id"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	CurrentRevision Revision   `json:"currentRevision"`
	RevisionCount   int        `json:"revisionCount"`
	SoundiizURL     *string    `json:"soundiizUrl,omitempty"`
	SoundiizExpires *time.Time `json:"soundiizExpiresAt,omitempty"`
}

// GenerateRequest contains validated user input for a new playlist job.
type GenerateRequest struct {
	Prompt       string   `json:"prompt"`
	TrackCount   int      `json:"trackCount"`
	Effort       Effort   `json:"effort"`
	ReferenceIDs []string `json:"referenceIds"`
}

// GeneratedPlaylist is provider output before persistence metadata is assigned.
type GeneratedPlaylist struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Tracks      []Track `json:"tracks"`
}

// JobStatus is the lifecycle state of a background operation.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// Job is a client-visible snapshot of asynchronous work.
type Job struct {
	ID         string     `json:"id"`
	Status     JobStatus  `json:"status"`
	Phase      string     `json:"phase"`
	PlaylistID string     `json:"playlistId,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}
