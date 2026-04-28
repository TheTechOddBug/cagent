package lifecycle

import (
	"fmt"
	"sync"
	"time"
)

// State is the high-level lifecycle state of a toolset, as observed by
// the supervisor and surfaced to logs, the TUI, and OTel attributes.
//
// The state machine is:
//
//	Stopped ──Start()──▶ Starting ──ok──▶ Ready
//	   ▲                    │ err          │ Wait()/Close()
//	   │                    ▼              ▼
//	   └─────── Stop() ── Failed ◀──── Restarting ──ok──▶ Ready
//	                       ▲              │
//	                       └── budget ────┘
//
// Degraded is a transient state surface used when a Ready toolset starts
// failing health checks but has not yet been demoted by the supervisor.
type State int32

const (
	// StateStopped is the initial state and the post-Stop state. No
	// resources are held.
	StateStopped State = iota

	// StateStarting is set while the supervisor is attempting the first
	// connect/initialize handshake.
	StateStarting

	// StateReady means the toolset is connected, initialized, and
	// answering requests.
	StateReady

	// StateDegraded means the toolset is still considered usable but the
	// last health check failed or a recent call returned a transport
	// error. The supervisor may move it back to Ready after a successful
	// call or down to Restarting after sustained failure.
	StateDegraded

	// StateRestarting means the supervisor is attempting to bring the
	// toolset back to Ready after a transport failure or crash.
	StateRestarting

	// StateFailed means the supervisor has given up restarting (typically
	// after exhausting the restart budget). The toolset is no longer
	// usable until an explicit Restart() or Stop()+Start() cycle.
	StateFailed
)

// String returns a short, lowercase human-readable name for s, suitable
// for logs and TUI status lines.
func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateReady:
		return "ready"
	case StateDegraded:
		return "degraded"
	case StateRestarting:
		return "restarting"
	case StateFailed:
		return "failed"
	default:
		return fmt.Sprintf("state(%d)", s)
	}
}

// IsTerminal reports whether s is a state from which the supervisor will
// not transition without external action (Start/Restart/Stop).
func (s State) IsTerminal() bool {
	return s == StateStopped || s == StateFailed
}

// IsUsable reports whether the toolset is expected to handle requests
// in this state. Ready and Degraded are both usable; the supervisor may
// still attempt requests against a Degraded toolset but should treat
// failures as expected.
func (s State) IsUsable() bool {
	return s == StateReady || s == StateDegraded
}

// StateInfo is a lightweight snapshot of a Tracker's current state and
// the most recent transition. It is safe to copy and pass around.
type StateInfo struct {
	State        State
	Since        time.Time
	LastError    error
	RestartCount int
}

// Tracker is a small, thread-safe state machine helper. It records the
// current state, the time it was entered, the most recent error (if any),
// and a restart counter. It does not enforce transition validity beyond
// what callers ask for; that is the supervisor's job.
//
// Tracker is intentionally minimal: it exists so that MCP, LSP, and
// future transports share a single vocabulary for status reporting.
//
// The zero value is a valid Tracker in StateStopped.
type Tracker struct {
	mu           sync.RWMutex
	state        State
	since        time.Time
	lastErr      error
	restartCount int
}

// NewTracker returns a Tracker initialised in StateStopped.
func NewTracker() *Tracker {
	return &Tracker{
		state: StateStopped,
		since: time.Now(),
	}
}

// Set transitions the tracker to a new state, recording the time of the
// transition and clearing the last error. If the new state equals the
// current state, Since and LastError are preserved.
func (t *Tracker) Set(s State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == s {
		return
	}
	t.state = s
	t.since = time.Now()
	t.lastErr = nil
}

// Fail transitions the tracker to s and records err as the last error.
// Fail is the standard way to enter StateFailed or StateRestarting after
// a failure: callers should always pass the underlying error so it can
// be surfaced to the user.
func (t *Tracker) Fail(s State, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = s
	t.since = time.Now()
	t.lastErr = err
}

// IncRestarts increments the restart counter and returns the new value.
// It does not change the state.
func (t *Tracker) IncRestarts() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restartCount++
	return t.restartCount
}

// ResetRestarts zeroes the restart counter. Supervisors call this after
// a successful sustained Ready period to forget transient incidents.
func (t *Tracker) ResetRestarts() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restartCount = 0
}

// Snapshot returns a point-in-time copy of the tracker state.
func (t *Tracker) Snapshot() StateInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return StateInfo{
		State:        t.state,
		Since:        t.since,
		LastError:    t.lastErr,
		RestartCount: t.restartCount,
	}
}

// State returns the current state.
func (t *Tracker) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// LastError returns the most recent error recorded by Fail, or nil if
// the tracker has never seen a failure (or transitioned to a clean state
// via Set since the last failure).
func (t *Tracker) LastError() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastErr
}
