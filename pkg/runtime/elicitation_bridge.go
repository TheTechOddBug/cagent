package runtime

import (
	"errors"
	"sync"
)

// elicitationBridge owns the events channel that the runtime's MCP
// elicitation handler sends requests to. Each RunStream call swaps in its
// own channel on entry and the previous one back on exit, so nested
// sub-session streams don't lose the parent's elicitation pipe.
//
// The bridge encapsulates a non-trivial concurrency contract: while a
// caller holds a reference to the current channel and is in the middle
// of sending an elicitation request, the swap-back must not racing with
// close(channel) on the inner stream. We achieve this by serializing
// send and swap with an RWMutex held across the send. Pushing this into
// a small standalone type keeps the contract testable in isolation
// (with the race detector) without spinning up a runtime, and keeps
// LocalRuntime free of the two raw fields it used to expose.
type elicitationBridge struct {
	mu sync.RWMutex
	ch chan Event
}

// swap atomically replaces the bridge's channel and returns the previous
// value. RunStream calls swap(events) on entry and swap(prev) on exit.
func (b *elicitationBridge) swap(ch chan Event) chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	prev := b.ch
	b.ch = ch
	return prev
}

// errNoElicitationChannel is returned when the bridge has no channel
// configured (no RunStream is active).
var errNoElicitationChannel = errors.New("no events channel available for elicitation")

// send delivers ev to the current channel, holding the read lock across
// the send. This blocks any concurrent swap until the send completes,
// preserving the invariant that the channel reference held by an
// in-flight sender stays open until that sender finishes.
//
// Returns errNoElicitationChannel when no channel is configured.
func (b *elicitationBridge) send(ev Event) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.ch == nil {
		return errNoElicitationChannel
	}
	b.ch <- ev
	return nil
}
