package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/components/tool/defaulttool"
	"github.com/docker/docker-agent/pkg/tui/core/layout"
	"github.com/docker/docker-agent/pkg/tui/service"
	tuitypes "github.com/docker/docker-agent/pkg/tui/types"
)

func TestToolViewReusesInstanceRegistry(t *testing.T) {
	t.Parallel()

	call := tools.ToolCall{ID: "call", Function: tools.FunctionCall{Name: "shell", Arguments: `{"cmd":"pwd"}`}}
	view := NewToolView("root", call, tools.Tool{Name: "shell"}, tuitypes.ToolStatusCompleted)
	other := NewToolView("root", call, tools.Tool{Name: "shell"}, tuitypes.ToolStatusCompleted)
	registry := view.renderers
	require.NotNil(t, registry)
	require.NotSame(t, registry, other.renderers)

	calls := 0
	registry.Register("shell", func(ar *animation.Runtime, msg *tuitypes.Message, state service.SessionStateReader) layout.Model {
		calls++
		return defaulttool.New(ar, msg, state)
	})
	assert.Zero(t, calls)
	for frame := range 3 {
		assert.NotEmpty(t, RenderToolWithState(view, 80, frame, nil))
		assert.Same(t, registry, view.renderers)
		assert.Equal(t, frame+1, calls, "reuse the registry, not the rendered model")
	}
	RenderTool(*view, 80)
	assert.Equal(t, 4, calls, "value copies must retain the instance registry")
	RenderTool(*other, 80)
	assert.Equal(t, 4, calls, "other views must not inherit registrations")
}

func TestToolViewSnapshotsRetainRegistry(t *testing.T) {
	t.Parallel()

	for _, finalize := range []bool{false, true} {
		name := "finish"
		if finalize {
			name = "finalize all"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker := NewToolTracker()
			call := tools.ToolCall{ID: "call", Function: tools.FunctionCall{Name: "shell", Arguments: `{"cmd":"pwd"}`}}
			def := tools.Tool{Name: "shell"}
			tracker.Upsert("root", call, def, tuitypes.ToolStatusRunning)
			original := tracker.Get(call.ID)
			require.NotNil(t, original)
			var snapshot *ToolView
			if finalize {
				views := tracker.FinalizeAll(tuitypes.ToolStatusCompleted)
				require.Len(t, views, 1)
				snapshot = views[0]
			} else {
				snapshot = tracker.Finish(call.ID, ToolResult{ToolDefinition: def})
			}
			require.NotNil(t, snapshot)
			assert.True(t, tracker.Empty())
			assert.NotSame(t, original.message, snapshot.message)
			assert.Same(t, original.renderers, snapshot.renderers)
			assert.Equal(t, RenderTool(*original, 80), RenderTool(*snapshot, 80))
			assert.Contains(t, strings.Join(RenderTool(*snapshot, 80), "\n"), "pwd")
		})
	}
}

func TestRenderToolWithoutRegistry(t *testing.T) {
	t.Parallel()

	assert.Nil(t, RenderToolWithState(nil, 0, 0, nil))
	var empty ToolView
	assert.Nil(t, RenderToolWithState(&empty, 0, 0, nil))
	assert.Nil(t, empty.renderers)

	view := ToolView{message: tuitypes.ToolCallMessage("root", tools.ToolCall{
		Function: tools.FunctionCall{Name: "shell", Arguments: `{"cmd":"pwd"}`},
	}, tools.Tool{Name: "shell"}, tuitypes.ToolStatusCompleted)}
	first := RenderToolWithState(&view, 80, 0, nil)
	require.NotNil(t, view.renderers)
	registry := view.renderers
	assert.Equal(t, first, RenderToolWithState(&view, 80, 0, nil))
	assert.Same(t, registry, view.renderers)
	assert.Contains(t, strings.Join(first, "\n"), "pwd")
}
