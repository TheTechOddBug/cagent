package modelpicker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/runtime"
)

func TestFilterUsesFuzzyModelMetadataSearch(t *testing.T) {
	t.Parallel()

	choices := []runtime.ModelChoice{
		{Name: "Claude Sonnet", Ref: "sonnet", Provider: "anthropic", Model: "claude-sonnet-4-6"},
		{Name: "GPT Five", Ref: "gpt", Provider: "openai", Model: "gpt-5"},
	}

	matches := Filter(choices, "cldsn46")
	require.Len(t, matches, 1)
	assert.Equal(t, "sonnet", matches[0].Ref)
}

func TestFilterStableRanking(t *testing.T) {
	t.Parallel()

	low := runtime.ModelChoice{Name: "m-o-d-e-l"}
	high := runtime.ModelChoice{Name: "model"}
	lowScore, ok := Score(low, "model")
	require.True(t, ok)
	highScore, ok := Score(high, "model")
	require.True(t, ok)
	require.Greater(t, highScore, lowScore)

	choices := []runtime.ModelChoice{low, high, high, {Name: "unrelated"}}
	assert.Equal(t, []runtime.ModelChoice{high, high, low}, Filter(choices, "model"))
	assert.Equal(t, low, choices[0], "filtering must not reorder the input")
	assert.Empty(t, Filter(choices, "missing"))
	assert.Empty(t, Filter(nil, "model"))
}

func TestFilterEqualScoresPreservesOrder(t *testing.T) {
	t.Parallel()

	choices := []runtime.ModelChoice{
		{Name: "model", Ref: "second"},
		{Name: "model", Ref: "first"},
	}
	firstScore, firstOK := Score(choices[0], "model")
	secondScore, secondOK := Score(choices[1], "model")
	require.True(t, firstOK && secondOK)
	require.Equal(t, firstScore, secondScore)
	assert.Equal(t, choices, Filter(choices, "model"))
}

func TestFilterEmptyQueryPreservesOrder(t *testing.T) {
	t.Parallel()

	choices := []runtime.ModelChoice{{Ref: "second"}, {Ref: "first"}}
	assert.Equal(t, choices, Filter(choices, ""))
}
