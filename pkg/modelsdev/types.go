package modelsdev

import "time"

// Database represents the complete models.dev database
type Database struct {
	Providers map[string]Provider `json:"providers"`
}

// Provider represents an AI model provider
type Provider struct {
	Models map[string]Model `json:"models"`
}

// Model represents an AI model with its specifications and capabilities.
//
// Fields are sourced from https://models.dev/api.json. Boolean capability
// fields default to false when absent from the source data.
type Model struct {
	Name       string     `json:"name"`
	Family     string     `json:"family,omitempty"`
	Cost       *Cost      `json:"cost,omitempty"`
	Limit      Limit      `json:"limit"`
	Modalities Modalities `json:"modalities"`

	// Reasoning is true when the model supports internal reasoning.
	Reasoning bool `json:"reasoning,omitempty"`
	// ToolCall is true when the model supports tool/function calls.
	ToolCall bool `json:"tool_call,omitempty"`
	// Temperature is true when the API accepts the temperature parameter.
	Temperature bool `json:"temperature,omitempty"`
	// Attachment is true when the model accepts file/image attachments.
	Attachment bool `json:"attachment,omitempty"`
	// OpenWeights is true when the model has openly-released weights.
	OpenWeights bool `json:"open_weights,omitempty"`
	// ReleaseDate is the model's public release date (YYYY-MM-DD).
	ReleaseDate string `json:"release_date,omitempty"`
}

// Cost represents the pricing information for a model, in USD per 1M tokens.
//
// The flat fields are the base price band; Tiers override them once the
// prompt exceeds Tier.Size (e.g. GPT-5.x above 272k, Gemini Pro above 200k).
// See [Cost.RatesFor]. The legacy context_over_200k alias models.dev emits is
// derived from Tiers, so it is not modelled.
type Cost struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`

	// Tiers lists long-context price bands, ascending by Tier.Size.
	Tiers []CostTier `json:"tiers,omitempty"`
}

// Rates is a single price band, in USD per 1M tokens.
type Rates struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// CostTier is a price band that replaces the base [Cost] rates once the
// prompt exceeds Tier.Size tokens. A tier is self-contained: a rate it
// omits is 0, not inherited from the base band.
type CostTier struct {
	Rates

	Tier TierSpec `json:"tier"`
}

// TierSpec describes the threshold above which a [CostTier] applies.
type TierSpec struct {
	// Type is the tier dimension; models.dev currently only defines "context".
	Type string `json:"type,omitempty"`
	// Size is the prompt token count the request must exceed for the tier
	// to apply.
	Size int64 `json:"size"`
}

// tierTypeContext is the only tier dimension models.dev defines; the schema
// defaults an absent type to it.
const tierTypeContext = "context"

// RatesFor returns the price band that applies to a request whose prompt
// (fresh + cached + cache-written input) is promptTokens long: the
// highest-threshold context tier the prompt exceeds, or the base rates.
func (c *Cost) RatesFor(promptTokens int64) Rates {
	var best *CostTier
	for i := range c.Tiers {
		t := &c.Tiers[i]
		if t.Tier.Type != "" && t.Tier.Type != tierTypeContext {
			continue
		}
		if promptTokens > t.Tier.Size && (best == nil || t.Tier.Size > best.Tier.Size) {
			best = t
		}
	}
	if best != nil {
		return best.Rates
	}
	return Rates{Input: c.Input, Output: c.Output, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite}
}

// Limit represents the context and output limitations of a model
type Limit struct {
	Context int   `json:"context"`
	Output  int64 `json:"output"`
}

// Modalities represents the supported input and output types
type Modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// CachedData represents the cached models.dev data with metadata
type CachedData struct {
	Database    Database  `json:"database"`
	LastRefresh time.Time `json:"last_refresh"`
	ETag        string    `json:"etag,omitempty"`
}
