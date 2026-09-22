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
