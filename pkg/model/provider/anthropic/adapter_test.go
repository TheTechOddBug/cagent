package anthropic

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"

	"github.com/docker/docker-agent/pkg/chat"
)

func TestFinishReason(t *testing.T) {
	tests := []struct {
		name       string
		stopReason anthropic.StopReason
		sawToolUse bool
		want       chat.FinishReason
	}{
		{"end_turn", anthropic.StopReasonEndTurn, false, chat.FinishReasonStop},
		{"stop_sequence", anthropic.StopReasonStopSequence, false, chat.FinishReasonStop},
		{"tool_use", anthropic.StopReasonToolUse, true, chat.FinishReasonToolCalls},
		{"max_tokens", anthropic.StopReasonMaxTokens, false, chat.FinishReasonLength},
		{"refusal", anthropic.StopReasonRefusal, false, chat.FinishReasonRefusal},
		{"refusal with tool use seen", anthropic.StopReasonRefusal, true, chat.FinishReasonRefusal},
		{"missing stop reason without tool use", "", false, chat.FinishReasonStop},
		{"missing stop reason with tool use", "", true, chat.FinishReasonToolCalls},
		{"unknown stop reason falls back to tool use flag", "pause_turn", true, chat.FinishReasonToolCalls},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, finishReason(tt.stopReason, tt.sawToolUse))
		})
	}
}
