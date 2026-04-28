// Package lifecycle defines the shared lifecycle vocabulary used by
// long-running toolsets (MCP servers, remote MCP, LSP servers).
//
// It is purposely small: a handful of typed error sentinels for classifying
// transport/connection failures, plus a State enum used by supervisor and
// status surfaces (TUI, logs, OTel). Behaviour is intentionally orthogonal
// to existing toolset code: this package is consumed by callers, never the
// other way around, so dropping it in is a pure refactor.
package lifecycle

import "errors"

// Sentinel errors used to classify failures in toolset lifecycle code.
//
// Concrete transports (stdio MCP, remote MCP, LSP stdio) wrap their
// underlying SDK errors with these so that supervisors can decide policy
// (retry, park, or fail) via errors.Is rather than substring matching.
//
// These are stable contracts: callers may rely on errors.Is(err, ErrXxx)
// working across MCP and LSP stacks. New error categories should be added
// here rather than introduced as ad-hoc strings.
var (
	// ErrTransport indicates a transport-level failure: the underlying
	// connection (stdio pipe, HTTP, SSE) was lost or never established.
	// These errors are typically restartable: the supervisor should
	// reconnect with backoff.
	ErrTransport = errors.New("transport failure")

	// ErrServerUnavailable indicates the server process or endpoint could
	// not be reached at all (binary missing, immediate EOF on stdin,
	// connection refused). The supervisor should retry on a slower cadence
	// because nothing about restarting in a tight loop will help.
	ErrServerUnavailable = errors.New("server unavailable")

	// ErrServerCrashed indicates the server process started but exited
	// unexpectedly (i.e. without a Stop() request). The supervisor should
	// restart according to its policy.
	ErrServerCrashed = errors.New("server crashed")

	// ErrInitTimeout indicates the server initialize handshake did not
	// complete within the configured deadline. The supervisor should
	// abandon this attempt and reschedule.
	ErrInitTimeout = errors.New("initialize timed out")

	// ErrInitNotification indicates the server accepted the initialize
	// request but the client failed to send the followup "initialized"
	// notification. This is a retryable transient documented upstream.
	ErrInitNotification = errors.New("failed to send initialized notification")

	// ErrCapabilityMissing indicates the server does not advertise a
	// capability the caller requires. The supervisor should NOT restart on
	// this — restarting won't change the server's advertised capabilities.
	ErrCapabilityMissing = errors.New("capability not supported")

	// ErrAuthRequired indicates the server rejected the request because
	// authentication (typically OAuth) is required. The supervisor should
	// park the connection rather than spinning in a restart loop; resumption
	// happens after the user completes the auth flow.
	ErrAuthRequired = errors.New("authentication required")

	// ErrSessionMissing indicates the server lost the client's session
	// (e.g. a remote MCP server restarted). The supervisor should force
	// a reconnect.
	ErrSessionMissing = errors.New("session missing")

	// ErrNotStarted indicates an operation was attempted on a toolset that
	// has not yet successfully started.
	ErrNotStarted = errors.New("toolset not started")
)
