package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/api"
	"github.com/docker/docker-agent/pkg/chat"
)

func TestValidateSummaryAttribution(t *testing.T) {
	t.Parallel()

	t.Run("accepts valid attribution", func(t *testing.T) {
		t.Parallel()
		req := api.AddSummaryRequest{Model: "openai/gpt-4o-mini", Usage: &chat.Usage{InputTokens: 10}}
		require.NoError(t, validateSummaryAttribution(&req))
		assert.NotNil(t, req.Usage)
	})

	t.Run("normalizes all-zero usage to nil", func(t *testing.T) {
		t.Parallel()
		req := api.AddSummaryRequest{Usage: &chat.Usage{}}
		require.NoError(t, validateSummaryAttribution(&req))
		assert.Nil(t, req.Usage, "zero usage must look like no usage, matching local compaction")
	})

	t.Run("rejects negative token counts", func(t *testing.T) {
		t.Parallel()
		req := api.AddSummaryRequest{Usage: &chat.Usage{OutputTokens: -1}}
		assert.Error(t, validateSummaryAttribution(&req))
	})

	t.Run("rejects oversized model name", func(t *testing.T) {
		t.Parallel()
		req := api.AddSummaryRequest{Model: strings.Repeat("a", maxSummaryModelNameLen+1)}
		assert.Error(t, validateSummaryAttribution(&req))
	})

	t.Run("rejects control characters in model name", func(t *testing.T) {
		t.Parallel()
		req := api.AddSummaryRequest{Model: "gpt\x1b[31m"}
		assert.Error(t, validateSummaryAttribution(&req))
	})
}
