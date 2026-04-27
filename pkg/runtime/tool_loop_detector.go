package runtime

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/docker/docker-agent/pkg/tools"
)

// toolLoopDetector detects consecutive identical tool call batches.
// When the model issues the same tool call(s) N times in a row without
// making progress, the detector signals that the agent should be terminated.
type toolLoopDetector struct {
	lastSignature string
	consecutive   int
	threshold     int
	exemptTools   map[string]struct{}
}

// newToolLoopDetector creates a detector that triggers after threshold
// consecutive identical call batches. Tool names passed in exemptTools
// are polling-safe: batches composed entirely of exempt tools (e.g.
// view_background_agent, view_background_job) never count toward the
// consecutive-duplicate limit.
func newToolLoopDetector(threshold int, exemptTools ...string) *toolLoopDetector {
	exempt := make(map[string]struct{}, len(exemptTools))
	for _, name := range exemptTools {
		exempt[name] = struct{}{}
	}
	return &toolLoopDetector{threshold: threshold, exemptTools: exempt}
}

// reset clears the detector state so it can be reused after recovery.
func (d *toolLoopDetector) reset() {
	d.lastSignature = ""
	d.consecutive = 0
}

// record updates the detector with the latest tool call batch and returns
// true if the consecutive-duplicate threshold has been reached.
// Batches composed entirely of exempt (polling) tools are silently
// skipped so that expected polling patterns are not flagged.
func (d *toolLoopDetector) record(calls []tools.ToolCall) bool {
	if len(calls) == 0 {
		return false
	}

	// Polling tools are expected to be called repeatedly with identical
	// arguments while waiting for a background task to finish. Exempt batches
	// are completely invisible to the detector: they neither increment the
	// consecutive counter nor reset it, so a looping model cannot evade
	// detection by interleaving a single polling call.
	if d.isExemptBatch(calls) {
		return false
	}

	sig := callSignature(calls)
	if sig == d.lastSignature {
		d.consecutive++
	} else {
		d.lastSignature = sig
		d.consecutive = 1
	}

	return d.consecutive >= d.threshold
}

// isExemptBatch returns true when every call in the batch targets a
// polling-exempt tool.
func (d *toolLoopDetector) isExemptBatch(calls []tools.ToolCall) bool {
	if len(d.exemptTools) == 0 {
		return false
	}
	for _, c := range calls {
		if _, ok := d.exemptTools[c.Function.Name]; !ok {
			return false
		}
	}
	return true
}

// callSignature builds a composite key from the name and arguments of every
// tool call in the batch. Arguments are canonicalized (sorted keys) so that
// semantically identical JSON with different key ordering produces the same
// signature. Null-byte separators prevent ambiguity between different call
// structures that could otherwise produce the same string.
func callSignature(calls []tools.ToolCall) string {
	var b strings.Builder
	for i, c := range calls {
		if i > 0 {
			b.WriteByte(0)
		}
		b.WriteString(c.Function.Name)
		b.WriteByte(0)
		b.WriteString(canonicalJSON(c.Function.Arguments))
	}
	return b.String()
}

// canonicalJSON re-serializes a JSON string with sorted keys so that
// {"b":1,"a":2} and {"a":2,"b":1} produce identical output. If the input
// is not valid JSON, it is returned as-is (the signature still works for
// exact-match detection, just without key-order normalization).
func canonicalJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	normalized := sortKeys(v)
	out, err := json.Marshal(normalized)
	if err != nil {
		return s
	}
	return string(out)
}

// sortKeys recursively sorts map keys so json.Marshal produces deterministic output.
func sortKeys(v any) any {
	switch val := v.(type) {
	case map[string]any:
		sorted := make(map[string]any, len(val))
		for _, k := range slices.Sorted(maps.Keys(val)) {
			sorted[k] = sortKeys(val[k])
		}
		return sorted
	case []any:
		for i, item := range val {
			val[i] = sortKeys(item)
		}
		return val
	default:
		return v
	}
}
