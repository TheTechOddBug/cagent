package leantui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/docker/docker-agent/pkg/leantui/ui"
	"github.com/docker/docker-agent/pkg/tui/components/markdown"
)

// renderUserLines renders a submitted user message as committed scrollback,
// echoing it with the same prompt marker used by the input box.
func renderUserLines(text string, width int) []string {
	return renderUserLinesWith(text, width, ui.StAccent(), ui.StPrimary())
}

func renderPendingUserLines(text string, width int) []string {
	muted := ui.StMuted()
	return renderUserLinesWith(text, width, muted, muted)
}

func renderUserLinesWith(text string, width int, promptStyle, textStyle lipgloss.Style) []string {
	text = strings.TrimRight(text, "\n")
	wrapped := ui.WrapANSI(text, width-ui.PromptWidth)
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		prefix := promptStyle.Render(ui.PromptText)
		if i > 0 {
			prefix = ui.Continuation
		}
		out = append(out, prefix+textStyle.Render(line))
	}
	return out
}

// renderReasoningLines renders agent reasoning as dimmed italic text.
func renderReasoningLines(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	style := ui.StReasoning()
	var out []string
	for _, line := range ui.WrapANSI(text, width-2) {
		out = append(out, "  "+style.Render(line))
	}
	return out
}

// renderAssistantLines renders an assistant message as markdown. Each returned
// line is guaranteed to fit within width so the differential renderer's row
// accounting stays correct.
func renderAssistantLines(text string, width int) []string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}

	rendered, err := markdown.NewRenderer(width).Render(text)
	if err != nil {
		return ui.WrapANSI(text, width)
	}

	var out []string
	for line := range strings.SplitSeq(strings.Trim(rendered, "\n"), "\n") {
		if ui.DisplayWidth(line) > width {
			out = append(out, ui.WrapANSI(line, width)...)
			continue
		}
		out = append(out, line)
	}
	return out
}

func renderNoticeLines(prefix, text string, width int, style lipgloss.Style) []string {
	wrapped := ui.WrapANSI(text, width-ui.DisplayWidth(prefix))
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		p := prefix
		if i > 0 {
			p = strings.Repeat(" ", ui.DisplayWidth(prefix))
		}
		out = append(out, style.Render(p+line))
	}
	return out
}
