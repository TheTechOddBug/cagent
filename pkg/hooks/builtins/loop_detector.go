package builtins

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"

	"github.com/docker/docker-agent/pkg/hooks"
)

// LoopDetector is the registered name of the loop_detector builtin.
const LoopDetector = "loop_detector"

// defaultLoopDetectorThreshold matches the historical default of the
// inline tool-loop detector previously baked into pkg/runtime.
const defaultLoopDetectorThreshold = 5

// loopDetectorBuiltin is the post_tool_use builtin that terminates the
// run when the model issues the same tool call (name + canonical args)
// `threshold` times in a row.
//
// Args layout: `[threshold, exempt1, exempt2, ...]`. An invalid or
// missing threshold falls back to [defaultLoopDetectorThreshold].
//
// State is per-session, keyed by [hooks.Input.SessionID], and cleared
// from session_end via [State.ClearSession].
//
// Detection is per-call, not per-batch: single-tool repetition and
// parallel-identical batches still trip; alternating multi-tool
// patterns like `[A,B] [A,B]` do not — those should be caught by
// max_iterations or manual threshold tuning. Tools listed in `args`
// after the threshold (e.g. background-task pollers) neither
// increment nor reset the counter.
type loopDetectorBuiltin struct {
	mu     sync.Mutex
	states map[string]*loopState // SessionID -> state
}

type loopState struct {
	sig   string
	count int
}

func newLoopDetector() *loopDetectorBuiltin {
	return &loopDetectorBuiltin{states: map[string]*loopState{}}
}

func (d *loopDetectorBuiltin) hook(_ context.Context, in *hooks.Input, args []string) (*hooks.Output, error) {
	if in == nil || in.SessionID == "" || in.ToolName == "" {
		// Defensive: post_tool_use always carries SessionID and
		// ToolName today. Skipping unkeyed events keeps the state
		// map from filling with anonymous entries.
		return nil, nil
	}

	threshold := defaultLoopDetectorThreshold
	var exempt []string
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			threshold = n
		}
		exempt = args[1:]
	}
	if slices.Contains(exempt, in.ToolName) {
		return nil, nil
	}

	sig := in.ToolName + "\x00" + canonicalToolInput(in.ToolInput)

	d.mu.Lock()
	state, ok := d.states[in.SessionID]
	if !ok {
		state = &loopState{}
		d.states[in.SessionID] = state
	}
	if sig == state.sig {
		state.count++
	} else {
		state.sig = sig
		state.count = 1
	}
	count := state.count
	d.mu.Unlock()

	if count < threshold {
		return nil, nil
	}

	slog.Warn("loop_detector tripped",
		"tool", in.ToolName, "consecutive", count,
		"threshold", threshold, "session_id", in.SessionID)

	return &hooks.Output{
		Decision: hooks.DecisionBlockValue,
		Reason: fmt.Sprintf(
			"Agent terminated: detected %d consecutive identical calls to %s. "+
				"This indicates a degenerate loop where the model is not making progress.",
			count, in.ToolName),
	}, nil
}

func (d *loopDetectorBuiltin) clearSession(sessionID string) {
	d.mu.Lock()
	delete(d.states, sessionID)
	d.mu.Unlock()
}
