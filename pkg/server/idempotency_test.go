package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdempotencyCache_ReserveDedupes(t *testing.T) {
	t.Parallel()
	c := newIdempotencyCache(8)

	assert.False(t, c.reserve("a"), "first sighting is not a duplicate")
	assert.True(t, c.reserve("a"), "second sighting is a duplicate")
	assert.False(t, c.reserve("b"), "a different key is not a duplicate")
}

func TestIdempotencyCache_ReleaseAllowsRetry(t *testing.T) {
	t.Parallel()
	c := newIdempotencyCache(8)

	assert.False(t, c.reserve("a"))
	c.release("a")
	assert.False(t, c.reserve("a"), "after release the key is processable again")
}

func TestIdempotencyCache_EvictsOldestPastCapacity(t *testing.T) {
	t.Parallel()
	c := newIdempotencyCache(2)

	assert.False(t, c.reserve("a"))
	assert.False(t, c.reserve("b"))
	assert.False(t, c.reserve("c")) // evicts "a"

	assert.True(t, c.reserve("b"), "still-cached key remains a duplicate")
	assert.False(t, c.reserve("a"), "evicted key is forgotten and processable again")
}
