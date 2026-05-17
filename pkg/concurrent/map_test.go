package concurrent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMap_StoreLoad(t *testing.T) {
	m := NewMap[string, int]()

	m.Store("a", 1)
	m.Store("b", 2)

	val, ok := m.Load("a")
	assert.True(t, ok)
	assert.Equal(t, 1, val)

	val, ok = m.Load("b")
	assert.True(t, ok)
	assert.Equal(t, 2, val)

	_, ok = m.Load("missing")
	assert.False(t, ok)
}

func TestMap_StoreOverwrites(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("k", 1)
	m.Store("k", 2)

	val, ok := m.Load("k")
	assert.True(t, ok)
	assert.Equal(t, 2, val)
	assert.Equal(t, 1, m.Length())
}

func TestMap_Delete(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("a", 1)
	m.Store("b", 2)

	m.Delete("a")
	_, ok := m.Load("a")
	assert.False(t, ok)
	assert.Equal(t, 1, m.Length())

	// Deleting a missing key is a no-op.
	m.Delete("missing")
	assert.Equal(t, 1, m.Length())
}

func TestMap_Length(t *testing.T) {
	m := NewMap[string, int]()
	assert.Equal(t, 0, m.Length())

	m.Store("a", 1)
	m.Store("b", 2)
	m.Store("c", 3)
	assert.Equal(t, 3, m.Length())
}

func TestMap_Range(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("a", 1)
	m.Store("b", 2)
	m.Store("c", 3)

	collected := map[string]int{}
	m.Range(func(k string, v int) bool {
		collected[k] = v
		return true
	})
	assert.Equal(t, map[string]int{"a": 1, "b": 2, "c": 3}, collected)

	// Early termination: stop after the first element.
	count := 0
	m.Range(func(_ string, _ int) bool {
		count++
		return false
	})
	assert.Equal(t, 1, count)
}

func TestMap_RangeCallbackCanMutate(t *testing.T) {
	// Range iterates over a snapshot, so callbacks may safely mutate the map
	// without deadlocking.
	m := NewMap[string, int]()
	m.Store("a", 1)
	m.Store("b", 2)

	m.Range(func(k string, _ int) bool {
		m.Store(k+"!", 0)
		return true
	})

	assert.Equal(t, 4, m.Length())
}

func TestMap_ZeroValueStore(t *testing.T) {
	// The zero value of Map must be usable: Store should lazily initialise
	// the underlying map instead of panicking.
	var m Map[string, int]
	m.Store("a", 1)

	val, ok := m.Load("a")
	assert.True(t, ok)
	assert.Equal(t, 1, val)
}

func TestMap_Concurrent(t *testing.T) {
	m := NewMap[int, int]()
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.Store(n, n*2)
		}(i)
	}

	wg.Wait()
	require.Equal(t, 100, m.Length())

	for i := range 100 {
		val, ok := m.Load(i)
		require.True(t, ok)
		require.Equal(t, i*2, val)
	}
}
