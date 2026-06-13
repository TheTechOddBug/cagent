package toolcommon

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestTruncateTextLeft(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		maxWidth int
		expected string
	}{
		{name: "text within width", text: "hello", maxWidth: 10, expected: "hello"},
		{name: "text exactly at width", text: "hello", maxWidth: 5, expected: "hello"},
		{name: "keeps the tail", text: "hello world", maxWidth: 8, expected: "…o world"},
		{name: "model identifier keeps suffix", text: "claude-sonnet-4-6", maxWidth: 13, expected: "…e-sonnet-4-6"},
		{name: "truncate to minimum", text: "hello", maxWidth: 2, expected: "…o"},
		{name: "empty string", text: "", maxWidth: 10, expected: ""},
		{name: "width of 1 returns ellipsis only", text: "hello", maxWidth: 1, expected: "…"},
		{name: "zero width", text: "hello", maxWidth: 0, expected: ""},
		{name: "negative width", text: "hello", maxWidth: -5, expected: ""},
		{name: "single character fits", text: "a", maxWidth: 1, expected: "a"},
		{name: "single character with larger width", text: "a", maxWidth: 10, expected: "a"},
		{name: "unicode within width", text: "héllo", maxWidth: 10, expected: "héllo"},
		{name: "unicode needs truncation", text: "héllo wörld", maxWidth: 8, expected: "…o wörld"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := TruncateTextLeft(tt.text, tt.maxWidth)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestTruncateTextLeft_NeverExceedsWidth guards the wide-character boundary
// case: cutting on a wide grapheme can momentarily overshoot, so the result
// must always measure at most maxWidth cells.
func TestTruncateTextLeft_NeverExceedsWidth(t *testing.T) {
	t.Parallel()

	inputs := []string{"你好世界", "hello你好世界", "claude-sonnet-4-6", "résumé wörld"}
	for _, s := range inputs {
		for w := 1; w <= lipgloss.Width(s)+2; w++ {
			out := TruncateTextLeft(s, w)
			assert.LessOrEqualf(t, lipgloss.Width(out), w,
				"TruncateTextLeft(%q, %d) = %q exceeded width", s, w, out)
		}
	}
}

// TestTruncateTextLeft_PreservesANSI verifies styled input keeps its escape
// sequences intact and reports the visible (ANSI-stripped) width correctly.
func TestTruncateTextLeft_PreservesANSI(t *testing.T) {
	t.Parallel()

	styled := lipgloss.NewStyle().Bold(true).Render("claude-sonnet-4-6")
	out := TruncateTextLeft(styled, 8)

	assert.LessOrEqual(t, lipgloss.Width(out), 8)
	stripped := ansi.Strip(out)
	assert.Contains(t, stripped, "…")
	assert.Contains(t, stripped, "-4-6", "informative tail should survive")
	assert.NotEqual(t, stripped, out, "ANSI styling should be preserved in the output")
}
