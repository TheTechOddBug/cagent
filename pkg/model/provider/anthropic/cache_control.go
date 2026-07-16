package anthropic

import (
	"github.com/anthropics/anthropic-sdk-go"

	"github.com/docker/docker-agent/pkg/tools"
)

// Anthropic allows at most 4 cache_control breakpoints per request; a
// request exceeding the limit is rejected outright. The budget is
// allocated in one place — this file — as follows:
//
//	tool list       0 or 1 (only when deferred tools are in play)
//	system blocks   up to maxSystemCacheBreakpoints, honoring upstream
//	                CacheControl marks (excess marks are dropped)
//	message tail    the remainder (messageCacheBreakpoints)
const maxSystemCacheBreakpoints = 2

// messageCacheBreakpoints returns how many message-level cache_control
// breakpoints a request may use: what remains of the budget after the
// tool list and system blocks take their share.
func messageCacheBreakpoints(requestTools []tools.Tool) int {
	if containsDeferredTool(requestTools) {
		return 1
	}
	return 2
}

// markSystemBlockCacheControl marks the block as a cache breakpoint if the
// system-block budget allows it, returning the updated count of marked
// blocks. Re-marking an already-marked block is a no-op so that duplicate
// upstream marks don't burn budget.
func markSystemBlockCacheControl(block *anthropic.TextBlockParam, marked int) int {
	if block.CacheControl.Type != "" {
		return marked
	}
	if marked >= maxSystemCacheBreakpoints {
		return marked
	}
	block.CacheControl = anthropic.NewCacheControlEphemeralParam()
	return marked + 1
}

// applyMessageCacheControl adds ephemeral cache control to the last content block
// of the last `breakpoints` messages for prompt caching.
func applyMessageCacheControl(messages []anthropic.MessageParam, breakpoints int) {
	for i := len(messages) - 1; i >= 0 && i >= len(messages)-breakpoints; i-- {
		content := messages[i].Content
		if len(content) == 0 {
			continue
		}
		// nil for block kinds without cache control (e.g. thinking blocks).
		if cc := content[len(content)-1].GetCacheControl(); cc != nil {
			*cc = anthropic.NewCacheControlEphemeralParam()
		}
	}
}

// applyBetaMessageCacheControl adds ephemeral cache control to the last content block
// of the last `breakpoints` messages for prompt caching.
func applyBetaMessageCacheControl(messages []anthropic.BetaMessageParam, breakpoints int) {
	for i := len(messages) - 1; i >= 0 && i >= len(messages)-breakpoints; i-- {
		content := messages[i].Content
		if len(content) == 0 {
			continue
		}
		// nil for block kinds without cache control (e.g. thinking blocks).
		if cc := content[len(content)-1].GetCacheControl(); cc != nil {
			*cc = anthropic.NewBetaCacheControlEphemeralParam()
		}
	}
}
