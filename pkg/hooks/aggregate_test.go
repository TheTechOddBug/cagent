package hooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAggregateTracksMostRestrictiveDecision pins the new
// Result.Decision contract: when multiple pre_tool_use hooks fire on a
// single tool call, the aggregated verdict is the most-restrictive
// (Deny > Ask > Allow). The runtime's tool-approval flow consults this
// to short-circuit the user prompt for Allow and to escalate Ask, so
// the ordering must be stable.
func TestAggregateTracksMostRestrictiveDecision(t *testing.T) {
	t.Parallel()

	mk := func(d Decision, reason string) hookResult {
		return hookResult{HandlerResult: HandlerResult{Output: &Output{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:            EventPreToolUse,
				PermissionDecision:       d,
				PermissionDecisionReason: reason,
			},
		}}}
	}

	cases := []struct {
		name        string
		results     []hookResult
		wantVerdict Decision
		wantReason  string
		wantAllowed bool
	}{
		{
			name:        "no decision: Allowed=true, Decision empty",
			results:     []hookResult{{}},
			wantVerdict: "",
			wantAllowed: true,
		},
		{
			name:        "single allow",
			results:     []hookResult{mk(DecisionAllow, "safe")},
			wantVerdict: DecisionAllow,
			wantReason:  "safe",
			wantAllowed: true,
		},
		{
			name:        "single ask escalates over no decision",
			results:     []hookResult{{}, mk(DecisionAsk, "unclear")},
			wantVerdict: DecisionAsk,
			wantReason:  "unclear",
			wantAllowed: true, // Ask doesn't flip Allowed; the runtime handles the prompt.
		},
		{
			name: "deny beats ask beats allow",
			results: []hookResult{
				mk(DecisionAllow, "looks fine"),
				mk(DecisionAsk, "second-guess"),
				mk(DecisionDeny, "destructive"),
			},
			wantVerdict: DecisionDeny,
			wantReason:  "destructive",
			wantAllowed: false,
		},
		{
			name: "first reason wins on ties",
			results: []hookResult{
				mk(DecisionAsk, "first ask"),
				mk(DecisionAsk, "second ask"),
			},
			wantVerdict: DecisionAsk,
			wantReason:  "first ask",
			wantAllowed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			final := aggregate(tc.results, EventPreToolUse)
			assert.Equal(t, tc.wantVerdict, final.Decision)
			assert.Equal(t, tc.wantReason, final.DecisionReason)
			assert.Equal(t, tc.wantAllowed, final.Allowed)
		})
	}
}

// TestAggregateDecisionEmptyForNonPreToolUse documents that
// Result.Decision is meaningful only for pre_tool_use and
// safety_check events. Other events (turn_start, post_tool_use, ...)
// MUST leave it empty so a runtime that consults it can't accidentally
// act on a stale verdict from an unrelated hook.
func TestAggregateDecisionEmptyForNonPreToolUse(t *testing.T) {
	t.Parallel()

	results := []hookResult{{HandlerResult: HandlerResult{Output: &Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:      EventTurnStart,
			PermissionDecision: DecisionAllow, // misconfigured but possible
		},
	}}}}

	final := aggregate(results, EventTurnStart)
	assert.Equal(t, Decision(""), final.Decision)
	assert.Empty(t, final.DecisionReason)
}

// TestAggregateMergesPermissionRequestMetadata pins the metadata
// contract for permission_request hooks: keys from every matching hook
// are merged, and on a clash the later hook in config order wins (results
// is iterated in registration order).
func TestAggregateMergesPermissionRequestMetadata(t *testing.T) {
	t.Parallel()

	mk := func(meta map[string]string) hookResult {
		return hookResult{HandlerResult: HandlerResult{Output: &Output{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName: EventPermissionRequest,
				Metadata:      meta,
			},
		}}}
	}

	results := []hookResult{
		mk(map[string]string{"a": "1", "shared": "first"}),
		mk(map[string]string{"b": "2", "shared": "second"}),
	}

	final := aggregate(results, EventPermissionRequest)
	assert.Equal(t, map[string]string{
		"a":      "1",
		"b":      "2",
		"shared": "second",
	}, final.Metadata)
}

// TestAggregateIgnoresMetadataForNonPermissionRequest documents that
// Metadata is only collected for permission_request and safety_check
// events.
func TestAggregateIgnoresMetadataForNonPermissionRequest(t *testing.T) {
	t.Parallel()

	results := []hookResult{{HandlerResult: HandlerResult{Output: &Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName: EventPreToolUse,
			Metadata:      map[string]string{"a": "1"},
		},
	}}}}

	final := aggregate(results, EventPreToolUse)
	assert.Nil(t, final.Metadata)
}

// TestAggregateSafetyCheck_DenyDeniesAndFlipsAllowed: a Deny verdict
// from any safety_check hook flips Allowed=false so the dispatcher
// short-circuits to errorResponse rather than askUser. Mirrors the
// pre_tool_use deny semantics but applies at the pre-Decide() stage.
func TestAggregateSafetyCheck_DenyDeniesAndFlipsAllowed(t *testing.T) {
	t.Parallel()

	mk := func(d Decision, reason string) hookResult {
		return hookResult{HandlerResult: HandlerResult{Output: &Output{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:            EventSafetyCheck,
				PermissionDecision:       d,
				PermissionDecisionReason: reason,
			},
		}}}
	}

	final := aggregate([]hookResult{
		mk(DecisionAllow, "first hook says ok"),
		mk(DecisionDeny, "second hook says no"),
		mk(DecisionAsk, "third hook would ask"),
	}, EventSafetyCheck)

	assert.Equal(t, DecisionDeny, final.Decision)
	assert.Equal(t, "second hook says no", final.DecisionReason)
	assert.False(t, final.Allowed, "any Deny must flip Allowed=false")
	assert.Contains(t, final.Message, "second hook says no")
}

// TestAggregateSafetyCheck_AllowDoesNotFlipAllowed pins the advisory
// semantics. An Allow verdict from safety_check is informational —
// the pipeline still proceeds to Decide() / pre_tool_use, so Allowed
// must stay true (Deny is the only verdict that short-circuits at
// this stage).
func TestAggregateSafetyCheck_AllowDoesNotFlipAllowed(t *testing.T) {
	t.Parallel()

	results := []hookResult{{HandlerResult: HandlerResult{Output: &Output{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:      EventSafetyCheck,
			PermissionDecision: DecisionAllow,
		},
	}}}}

	final := aggregate(results, EventSafetyCheck)
	assert.Equal(t, DecisionAllow, final.Decision)
	assert.True(t, final.Allowed, "Allow under safety_check is advisory; Allowed stays true")
}

// TestAggregateSafetyCheck_MergesMetadata pins the merge contract for
// safety_check metadata: keys from every hook are merged, with later
// hooks winning on key clashes (same shape as permission_request).
func TestAggregateSafetyCheck_MergesMetadata(t *testing.T) {
	t.Parallel()

	mk := func(meta map[string]string) hookResult {
		return hookResult{HandlerResult: HandlerResult{Output: &Output{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:      EventSafetyCheck,
				PermissionDecision: DecisionAsk,
				Metadata:           meta,
			},
		}}}
	}

	results := []hookResult{
		mk(map[string]string{"blast_radius": "medium", "category": "fs-modify"}),
		mk(map[string]string{"blast_radius": "high", "reason": "irreversible"}),
	}

	final := aggregate(results, EventSafetyCheck)
	assert.Equal(t, map[string]string{
		"blast_radius": "high",
		"category":     "fs-modify",
		"reason":       "irreversible",
	}, final.Metadata)
}
