package lifecycle

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
)

// Classify maps an underlying error from a transport (stdio MCP, remote MCP,
// LSP) to one of the typed sentinels in this package.
//
// The returned error wraps the original via fmt.Errorf("%w: %w", sentinel,
// err) so that:
//   - errors.Is(returned, sentinel) is true,
//   - errors.Is(returned, original) is true,
//   - the original message is preserved for logs.
//
// Classify returns nil when err is nil. When err matches no known pattern it
// is returned unchanged so callers can decide policy (typically: treat as
// transport failure).
//
// Classify is conservative: it only recognizes patterns we have evidence of
// in the wild (the ones previously matched by the various ad-hoc helpers in
// pkg/tools/mcp). Substring matching is kept for messages emitted by
// upstream SDKs that wrap their errors with %v (dropping the chain).
func Classify(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, ErrServerUnavailable),
		errors.Is(err, ErrServerCrashed),
		errors.Is(err, ErrInitTimeout),
		errors.Is(err, ErrInitNotification),
		errors.Is(err, ErrCapabilityMissing),
		errors.Is(err, ErrAuthRequired),
		errors.Is(err, ErrSessionMissing),
		errors.Is(err, ErrTransport):
		// Already classified.
		return err
	}

	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return wrap(ErrServerUnavailable, err)
	}
	if errors.Is(err, io.EOF) {
		return wrap(ErrServerUnavailable, err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return wrap(ErrTransport, err)
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	if strings.Contains(lower, "failed to send initialized notification") {
		return wrap(ErrInitNotification, err)
	}

	if strings.Contains(lower, "session missing") || strings.Contains(lower, "session not found") {
		return wrap(ErrSessionMissing, err)
	}

	if strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(msg, "EOF") {
		return wrap(ErrTransport, err)
	}

	return err
}

// IsTransient reports whether err is one of the sentinel errors that the
// supervisor should retry on. Permanent failures (capability missing,
// auth required) return false.
func IsTransient(err error) bool {
	switch {
	case errors.Is(err, ErrTransport),
		errors.Is(err, ErrServerUnavailable),
		errors.Is(err, ErrServerCrashed),
		errors.Is(err, ErrInitTimeout),
		errors.Is(err, ErrInitNotification),
		errors.Is(err, ErrSessionMissing):
		return true
	}
	return false
}

// IsPermanent reports whether err is a sentinel that the supervisor should
// NOT retry on. The current set is { ErrCapabilityMissing, ErrAuthRequired }.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrCapabilityMissing) || errors.Is(err, ErrAuthRequired)
}

// wrap returns an error that satisfies errors.Is for both sentinel and
// underlying.
func wrap(sentinel, underlying error) error {
	// fmt.Errorf("%w: %w", a, b) produces an error whose Unwrap() returns
	// []error{a, b}, which errors.Is walks.
	return &classified{sentinel: sentinel, err: underlying}
}

// classified is a small error type that wraps an underlying error with a
// classification sentinel. It implements multi-target Unwrap so errors.Is
// matches both.
type classified struct {
	sentinel error
	err      error
}

func (c *classified) Error() string {
	return c.sentinel.Error() + ": " + c.err.Error()
}

func (c *classified) Unwrap() []error {
	return []error{c.sentinel, c.err}
}
