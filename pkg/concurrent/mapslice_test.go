package concurrent

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapSlice(t *testing.T) {
	t.Parallel()

	got := MapSlice([]int{1, 2, 3, 4}, func(v int) int { return v * v })
	assert.Equal(t, []int{1, 4, 9, 16}, got)
}

func TestMapSlice_Empty(t *testing.T) {
	t.Parallel()

	got := MapSlice(nil, func(int) int { t.Fatal("f must not be called"); return 0 })
	assert.Empty(t, got)
}

func TestMapSlice_RunsConcurrently(t *testing.T) {
	t.Parallel()

	const n = 8
	var running atomic.Int32
	start := make(chan struct{})

	// Every call blocks until all n are running; the test deadlocks (and
	// times out) if MapSlice were sequential.
	got := MapSlice(make([]struct{}, n), func(struct{}) int {
		if running.Add(1) == n {
			close(start)
		}
		<-start
		return 1
	})

	assert.Len(t, got, n)
}
