package toolcommon

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/docker/docker-agent/pkg/effort"
	"github.com/docker/docker-agent/pkg/tui/styles"
)

// gaugeCounts returns the number of filled and empty cells in a rendered gauge,
// after stripping ANSI styling.
func gaugeCounts(s string) (filled, empty int) {
	plain := ansi.Strip(s)
	return strings.Count(plain, styles.GaugeFilled), strings.Count(plain, styles.GaugeEmpty)
}

// TestEffortGaugeLosslessMapping verifies the six effort levels map onto a
// distinct, monotonically increasing filled-cell count (1..6), so the gauge is
// lossless and every level reads differently by cell count alone.
func TestEffortGaugeLosslessMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		level  effort.Level
		filled int
	}{
		{effort.Minimal, 1},
		{effort.Low, 2},
		{effort.Medium, 3},
		{effort.High, 4},
		{effort.XHigh, 5},
		{effort.Max, 6},
	}
	for _, c := range cases {
		filled, empty := gaugeCounts(EffortGauge(c.level))
		assert.Equalf(t, c.filled, filled, "filled cells for %s", c.level)
		assert.Equalf(t, EffortGaugeCells-c.filled, empty, "empty cells for %s", c.level)
		assert.Equalf(t, EffortGaugeCells, filled+empty, "total cells for %s", c.level)
	}
}

// TestEffortGaugeConstantWidth verifies every level (and the empty gauge) render
// to the same printable cell width, keeping the badge column aligned.
func TestEffortGaugeConstantWidth(t *testing.T) {
	t.Parallel()

	for _, l := range []effort.Level{effort.Minimal, effort.Low, effort.Medium, effort.High, effort.XHigh, effort.Max} {
		assert.Lenf(t, []rune(ansi.Strip(EffortGauge(l))), EffortGaugeCells, "width for %s", l)
	}
	assert.Len(t, []rune(ansi.Strip(EffortGaugeEmpty())), EffortGaugeCells, "empty gauge width")
}

// TestEffortGaugeEmpty verifies the empty gauge is six faint empty cells and no
// filled cells: "capable but disabled".
func TestEffortGaugeEmpty(t *testing.T) {
	t.Parallel()

	filled, empty := gaugeCounts(EffortGaugeEmpty())
	assert.Zero(t, filled)
	assert.Equal(t, EffortGaugeCells, empty)
}

// TestThinkingMarker covers the marker vocabulary used by the card and dialog.
func TestThinkingMarker(t *testing.T) {
	t.Parallel()

	// Effort level → full gauge.
	filled, empty := gaugeCounts(ThinkingMarker("high"))
	assert.Equal(t, 4, filled)
	assert.Equal(t, 2, empty)

	// off → empty gauge.
	filled, empty = gaugeCounts(ThinkingMarker("off"))
	assert.Zero(t, filled)
	assert.Equal(t, EffortGaugeCells, empty)

	// adaptive → the word "auto", no gauge.
	assert.Equal(t, "auto", ansi.Strip(ThinkingMarker("adaptive")))

	// token budget → the token glyph, no gauge.
	assert.Equal(t, styles.TokenGlyph, ansi.Strip(ThinkingMarker("8192")))

	// empty → no marker.
	assert.Empty(t, ThinkingMarker(""))
}

// TestThinkingGaugeValue covers the shared "<marker> <description>" summary used
// by the focus card and the agent-details dialog for every vocabulary case.
func TestThinkingGaugeValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		want  string
	}{
		{"high", strings.Repeat(styles.GaugeFilled, 4) + strings.Repeat(styles.GaugeEmpty, 2) + " high"},
		{"off", strings.Repeat(styles.GaugeEmpty, EffortGaugeCells) + " off"},
		{"adaptive", "auto adaptive"},
		{"8192", styles.TokenGlyph + " 8.2K tokens"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, ansi.Strip(ThinkingGaugeValue(c.label)), "label %q", c.label)
	}

	assert.Empty(t, ThinkingGaugeValue(""), "empty label yields no summary")
}
