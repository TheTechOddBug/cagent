package anthropic

import (
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/docker/docker-agent/pkg/model/provider/providerutil"
)

// serverSideFallbackBeta enables the `fallbacks` request parameter, which
// lets Anthropic reroute refused requests (e.g. Claude Fable 5 safety
// refusals) to backup models in a single round trip.
const serverSideFallbackBeta anthropic.AnthropicBeta = "server-side-fallback-2026-06-01"

// fallbackModels extracts the `fallbacks` provider_opt: a list of model IDs,
// in priority order. Returns nil when the option is absent or malformed.
//
// Fallback models receive the exact same request as the primary model
// (thinking configuration, task budget, beta features, ...), so they must
// accept the same request shape — Anthropic rejects the fallback attempt
// otherwise.
func fallbackModels(opts map[string]any) []string {
	models, ok := providerutil.GetProviderOptStringSlice(opts, "fallbacks")
	if !ok || len(models) == 0 {
		return nil
	}
	slog.Debug("Anthropic provider_opts: enabling server-side fallbacks", "models", models)
	return models
}

// fallbacksBody returns the request option that injects the `fallbacks` body
// field. The wire shape is [{"model": "..."}, ...].
func fallbacksBody(models []string) option.RequestOption {
	fallbacks := make([]map[string]string, len(models))
	for i, m := range models {
		fallbacks[i] = map[string]string{"model": m}
	}
	return option.WithJSONSet("fallbacks", fallbacks)
}
