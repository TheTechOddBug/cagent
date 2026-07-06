package leantui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/docker-agent/pkg/leantui/ui"
	pathx "github.com/docker/docker-agent/pkg/path"
	"github.com/docker/docker-agent/pkg/tui/components/toolcommon"
)

// statusData is the snapshot of run state shown in the footer.
type statusData struct {
	workingDir string
	branch     string

	agent    string
	model    string
	thinking string

	contextLength int64
	contextLimit  int64
	tokens        int64 // input + output tokens used so far
	cost          float64
	costKnown     bool
}

// renderStatus builds the two-line footer:
//
//	<working dir>  ⎇ <branch>                          <agent>
//	<context bar> <pct> · <tokens> · <cost>  <model> · <effort>
func renderStatus(d statusData, width int) []string {
	dir := ui.StSecondary().Render(ui.Truncate(pathx.ShortenHome(d.workingDir), max(10, width/2)))
	left1 := dir
	if d.branch != "" {
		left1 += ui.StMuted().Render("  ⎇ " + d.branch)
	}

	right1 := ""
	if d.agent != "" {
		right1 = ui.StAccent().Render(d.agent)
	}

	left2 := renderContext(d)

	var rightParts []string
	if d.model != "" {
		rightParts = append(rightParts, d.model)
	}
	if d.thinking != "" {
		rightParts = append(rightParts, d.thinking)
	}
	right2 := ui.StMuted().Render(strings.Join(rightParts, " · "))

	return []string{
		composeLine(left1, right1, width),
		composeLine(left2, right2, width),
	}
}

func renderContext(d statusData) string {
	cost := renderCostSuffix(d)
	if d.contextLimit <= 0 {
		if d.tokens > 0 {
			return ui.StMuted().Render(formatTokens(d.tokens)+" tokens") + cost
		}
		return renderBar(0) + ui.StMuted().Render(" 0% · 0/0") + cost
	}

	pct := float64(d.contextLength) / float64(d.contextLimit)
	if pct > 1 {
		pct = 1
	}
	bar := renderBar(pct)
	label := fmt.Sprintf(" %d%% · %s/%s",
		int(pct*100+0.5),
		formatTokens(d.contextLength),
		formatTokens(d.contextLimit),
	)
	return bar + ui.StMuted().Render(label) + cost
}

func renderCostSuffix(d statusData) string {
	if !d.costKnown {
		return ""
	}
	return ui.StMuted().Render(" · ") + ui.StAccent().Render(toolcommon.FormatCostUSD(d.cost))
}

// contextBarWidth is the cell width of the context-usage gauge.
const contextBarWidth = 10

func renderBar(pct float64) string {
	filled := min(int(pct*float64(contextBarWidth)+0.5), contextBarWidth)
	style := ui.StSuccess()
	switch {
	case pct >= 0.85:
		style = ui.StError()
	case pct >= 0.6:
		style = ui.StWarning()
	}
	return style.Render(strings.Repeat("█", filled)) + ui.StMuted().Render(strings.Repeat("░", contextBarWidth-filled))
}

// composeLine right-aligns right within width, truncating left if necessary.
func composeLine(left, right string, width int) string {
	lw := ui.DisplayWidth(left)
	rw := ui.DisplayWidth(right)
	if rw > width {
		return ui.Truncate(right, width)
	}
	if lw+rw+1 > width {
		left = ui.Truncate(left, max(0, width-rw-1))
		lw = ui.DisplayWidth(left)
	}
	gap := max(1, width-lw-rw)
	return left + strings.Repeat(" ", gap) + right
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}
