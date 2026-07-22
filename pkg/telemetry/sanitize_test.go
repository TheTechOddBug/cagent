package telemetry

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeLogValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "clean string unchanged", input: "hello world", want: "hello world"},
		{name: "empty string unchanged", input: "", want: ""},
		{name: "LF replaced with space", input: "foo\nbar", want: "foo bar"},
		{name: "CR replaced with space", input: "foo\rbar", want: "foo bar"},
		{name: "CRLF both replaced", input: "foo\r\nbar", want: "foo  bar"},
		{name: "multiple newlines all replaced", input: "a\nb\nc", want: "a b c"},
		{name: "injection attempt neutralised", input: "ok\nlevel=ERROR fake=injection", want: "ok level=ERROR fake=injection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeLogValue(tt.input))
		})
	}
}

// stringerWithNewline implements fmt.Stringer and carries a newline in its value.
type stringerWithNewline struct{ s string }

func (sw stringerWithNewline) String() string { return sw.s }

func TestSanitizeLogArgs(t *testing.T) {
	t.Run("nil args returned unchanged", func(t *testing.T) {
		assert.Nil(t, sanitizeLogArgs(nil))
	})

	t.Run("empty slice returned unchanged", func(t *testing.T) {
		assert.Equal(t, []any{}, sanitizeLogArgs([]any{}))
	})

	t.Run("plain string values are sanitized", func(t *testing.T) {
		args := []any{"key", "val\nwith\nnewlines"}
		got := sanitizeLogArgs(args)
		assert.Equal(t, "key", got[0])
		assert.Equal(t, "val with newlines", got[1])
	})

	t.Run("numeric and bool values pass through unchanged (preserve log typing)", func(t *testing.T) {
		args := []any{"count", 42, "flag", true, "pi", 3.14}
		got := sanitizeLogArgs(args)
		assert.Equal(t, args, got)
	})

	t.Run("named string types (EventType) are sanitised via default branch", func(t *testing.T) {
		malicious := EventType("session\nfake=injection")
		args := []any{"event", malicious}
		got := sanitizeLogArgs(args)
		assert.Equal(t, "session fake=injection", got[1])
	})

	t.Run("error values are sanitised via default branch", func(t *testing.T) {
		err := errors.New("boom\ninjected")
		args := []any{"error", err}
		got := sanitizeLogArgs(args)
		assert.Equal(t, "boom injected", got[1])
	})

	t.Run("fmt.Stringer values with newlines are sanitised via default branch", func(t *testing.T) {
		s := stringerWithNewline{"detail\r\nmore"}
		args := []any{"detail", s}
		got := sanitizeLogArgs(args)
		assert.Equal(t, "detail  more", got[1])
	})

	t.Run("mixed args: strings sanitized, numerics unchanged, named types sanitized", func(t *testing.T) {
		args := []any{"event_type", EventType("cmd\ninjected"), "count", 5, "session_id", "abc\nxyz"}
		got := sanitizeLogArgs(args)
		// event_type key (string): sanitized (no newlines → unchanged)
		assert.Equal(t, "event_type", got[0])
		// EventType value: sanitized via default branch
		assert.Equal(t, "cmd injected", got[1])
		// count key (string): unchanged
		assert.Equal(t, "count", got[2])
		// 5 (int): passthrough
		assert.Equal(t, 5, got[3])
		// session_id key (string): unchanged
		assert.Equal(t, "session_id", got[4])
		// string value: sanitized
		assert.Equal(t, "abc xyz", got[5])
	})
}

// Verify fmt is importable — used in stringerWithNewline.String() declaration.
var _ fmt.Stringer = stringerWithNewline{}
