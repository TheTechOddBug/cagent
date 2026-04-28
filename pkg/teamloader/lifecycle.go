package teamloader

import (
	"log/slog"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/tools/lifecycle"
)

// lifecyclePolicyFromConfig converts a latest.LifecycleConfig into a
// lifecycle.Policy. nil cfg returns the resilient default policy used by
// pre-step-4 callers.
//
// The resolution order is:
//  1. Profile defaults (resilient/strict/best-effort).
//  2. Explicit field overrides from the YAML.
//
// Logger is always populated with a component-tagged slog so that
// supervisor messages identify which toolset produced them.
func lifecyclePolicyFromConfig(name string, cfg *latest.LifecycleConfig) lifecycle.Policy {
	policy := profilePolicy(profileOf(cfg))
	policy.Logger = slog.With("component", "supervisor", "toolset", name)

	if cfg == nil {
		return policy
	}

	if cfg.Restart != "" {
		policy.Restart = parseRestart(cfg.Restart)
	}
	if cfg.MaxRestarts != 0 {
		policy.MaxAttempts = cfg.MaxRestarts // -1 means "unlimited" in both
	}
	if cfg.Backoff != nil {
		if cfg.Backoff.Initial.Duration > 0 {
			policy.Backoff.Initial = cfg.Backoff.Initial.Duration
		}
		if cfg.Backoff.Max.Duration > 0 {
			policy.Backoff.Max = cfg.Backoff.Max.Duration
		}
		if cfg.Backoff.Multiplier > 0 {
			policy.Backoff.Multiplier = cfg.Backoff.Multiplier
		}
		if cfg.Backoff.Jitter > 0 {
			policy.Backoff.Jitter = cfg.Backoff.Jitter
		}
	}
	return policy
}

// profileOf returns the effective profile name for cfg, defaulting to
// "resilient" when cfg or cfg.Profile is empty.
func profileOf(cfg *latest.LifecycleConfig) string {
	if cfg == nil || cfg.Profile == "" {
		return latest.LifecycleProfileResilient
	}
	return cfg.Profile
}

// profilePolicy returns the lifecycle.Policy defaults for a profile name.
// Unknown names fall through to "resilient" (the validator rejects unknown
// profiles, so this is a defensive fallback).
func profilePolicy(profile string) lifecycle.Policy {
	switch profile {
	case latest.LifecycleProfileStrict:
		return lifecycle.Policy{
			Restart:     lifecycle.RestartNever,
			MaxAttempts: -1, // explicit "no restarts"; tryRestart still respects this via MaxAttempts=0 logic
		}
	case latest.LifecycleProfileBestEffort:
		return lifecycle.Policy{
			Restart:     lifecycle.RestartNever,
			MaxAttempts: -1,
		}
	default: // resilient
		return lifecycle.Policy{
			Restart:     lifecycle.RestartOnFailure,
			MaxAttempts: 5,
			Backoff: lifecycle.Backoff{
				Initial:    0, // 0 → supervisor default of 1s
				Max:        0, // 0 → supervisor default of 32s
				Multiplier: 0, // 0 → supervisor default of 2
			},
		}
	}
}

// parseRestart converts a YAML restart string into the supervisor enum.
func parseRestart(s string) lifecycle.Restart {
	switch s {
	case "never":
		return lifecycle.RestartNever
	case "always":
		return lifecycle.RestartAlways
	default:
		return lifecycle.RestartOnFailure
	}
}
