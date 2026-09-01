package playlist

import "time"

// Pricing is a versioned per-token and per-tool-call rate card.
type Pricing struct {
	Version               string
	InputPerMillion       float64
	CachedInputPerMillion float64
	OutputPerMillion      float64
	WebSearchPerCall      *float64
}

var currentWebSearchPerCall = 0.01

// CurrentPricing is the rate card used for new estimates. Historical Usage
// keeps its PricingVersion so later price changes remain explainable.
var CurrentPricing = Pricing{
	Version:               "2026-09-01",
	InputPerMillion:       4.00,
	CachedInputPerMillion: 0.40,
	OutputPerMillion:      20.00,
	WebSearchPerCall:      &currentWebSearchPerCall,
}

// EstimateUsage adds a best-effort USD estimate without double-counting cached
// input or reasoning tokens (reasoning is already part of output tokens).
func EstimateUsage(usage Usage, pricing Pricing) Usage {
	normalInput := usage.InputTokens - usage.CachedTokens
	if normalInput < 0 {
		normalInput = 0
	}
	usage.EstimatedCostUSD = float64(normalInput)/1_000_000*pricing.InputPerMillion +
		float64(usage.CachedTokens)/1_000_000*pricing.CachedInputPerMillion +
		float64(usage.OutputTokens)/1_000_000*pricing.OutputPerMillion
	if pricing.WebSearchPerCall != nil {
		usage.EstimatedCostUSD += float64(usage.WebSearchCalls) * *pricing.WebSearchPerCall
		usage.SearchFeeKnown = true
	}
	usage.PricingVersion = pricing.Version
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now().UTC()
	}
	return usage
}
