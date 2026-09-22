package messages

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/service"
)

func TestMouseReleasePreservesScrollBeforeView(t *testing.T) {
	t.Parallel()

	for _, initial := range []struct {
		name    string
		content string
	}{
		{"short", "start\n\n"},
		{"long", strings.Repeat("history paragraph\n\n", 40)},
	} {
		t.Run(initial.name, func(t *testing.T) {
			m := NewScrollableView(animation.NewRuntime(), 80, 12, &service.SessionState{}).(*model)
			t.Cleanup(m.ar.Stop)
			m.AppendToLastMessage("root", initial.content)
			m.View()

			// Blur defers View while streaming continues to advance the transcript.
			m.AppendToLastMessage("root", strings.Repeat("new paragraph\n\n", 40))
			offset := m.scrollOffset
			require.Positive(t, offset)
			require.False(t, m.userHasScrolled)

			_, _ = m.Update(tea.MouseReleaseMsg{X: 20, Y: 6, Button: tea.MouseLeft})
			require.Equal(t, offset, m.scrollOffset, "an unrelated release must not import stale scrollbar state")
			require.False(t, m.userHasScrolled, "an unrelated release must not disable follow-tail")
			m.View()
			require.Equal(t, offset, m.scrollOffset)
		})
	}
}

func TestScrollbarReleaseEndsInterruptedSelection(t *testing.T) {
	t.Parallel()

	m := NewScrollableView(animation.NewRuntime(), 80, 12, &service.SessionState{}).(*model)
	t.Cleanup(m.ar.Stop)
	m.AppendToLastMessage("root", strings.Repeat("history paragraph\n\n", 40))
	m.View()

	_, _ = m.Update(tea.MouseClickMsg{X: 10, Y: 6, Button: tea.MouseLeft})
	require.True(t, m.IsSelecting())

	// A release outside the terminal can leave selection active on refocus.
	x := m.scrollview.ScrollbarX()
	_, _ = m.Update(tea.MouseClickMsg{X: x, Y: m.height - 1, Button: tea.MouseLeft})
	require.True(t, m.IsScrollbarDragging())
	_, _ = m.Update(tea.MouseMotionMsg{X: x, Y: m.height - 3, Button: tea.MouseLeft})
	_, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: m.height - 3, Button: tea.MouseLeft})
	require.False(t, m.IsScrollbarDragging())
	require.False(t, m.IsSelecting(), "release must also end the interrupted selection")

	offset := m.scrollOffset
	_, _ = m.Update(tea.MouseMotionMsg{X: 10, Y: 0})
	require.Equal(t, offset, m.scrollOffset, "button-free motion must not autoscroll")
}

func TestIgnoredScrollbarClickPreservesScrollBeforeView(t *testing.T) {
	t.Parallel()

	for _, button := range []tea.MouseButton{tea.MouseRight, tea.MouseMiddle} {
		t.Run(button.String(), func(t *testing.T) {
			m := NewScrollableView(animation.NewRuntime(), 80, 12, &service.SessionState{}).(*model)
			t.Cleanup(m.ar.Stop)
			m.AppendToLastMessage("root", "start\n\n")
			m.View()
			m.AppendToLastMessage("root", strings.Repeat("new paragraph\n\n", 40))
			offset := m.scrollOffset
			require.Positive(t, offset)

			x := m.scrollview.ScrollbarX()
			_, _ = m.Update(tea.MouseClickMsg{X: x, Y: 6, Button: button})
			_, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: 6, Button: button})
			require.Equal(t, offset, m.scrollOffset)
			require.False(t, m.userHasScrolled)
			require.False(t, m.IsScrollbarDragging())
		})
	}
}

func TestScrollbarUsesCurrentGeometryBeforeView(t *testing.T) {
	t.Parallel()

	for _, grabBeforeStream := range []bool{false, true} {
		t.Run(map[bool]string{false: "click after stream", true: "release after stream"}[grabBeforeStream], func(t *testing.T) {
			m := NewScrollableView(animation.NewRuntime(), 80, 12, &service.SessionState{}).(*model)
			t.Cleanup(m.ar.Stop)
			m.AppendToLastMessage("root", strings.Repeat("history paragraph\n\n", 40))
			m.View()
			x, y := m.scrollview.ScrollbarX(), m.height-1
			if grabBeforeStream {
				_, _ = m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				require.True(t, m.IsScrollbarDragging())
			}
			m.AppendToLastMessage("root", strings.Repeat("new paragraph\n\n", 40))
			offset := m.scrollOffset
			require.Positive(t, offset)
			if !grabBeforeStream {
				_, _ = m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				require.True(t, m.IsScrollbarDragging())
				require.Equal(t, offset, m.scrollOffset, "grabbing the thumb must not import stale geometry")
			}
			_, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			require.False(t, m.IsScrollbarDragging())
			require.Equal(t, offset, m.scrollOffset, "release without motion must preserve the current offset")
			require.False(t, m.userHasScrolled)
		})
	}
}
