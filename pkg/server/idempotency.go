package server

import "sync"

// defaultIdempotencyCapacity is how many recent follow-up keys are remembered
// per session. It only needs to cover the window in which a client might retry
// a request it is unsure landed, so a small bound is plenty.
const defaultIdempotencyCapacity = 256

// idempotencyCache remembers recently-seen idempotency keys for a single
// session so a retried request (e.g. after a network timeout that actually
// succeeded) is not processed twice. It is a bounded FIFO set: the oldest key
// is evicted once capacity is reached.
type idempotencyCache struct {
	capacity int

	mu   sync.Mutex
	set  map[string]struct{}
	ring []string
}

func newIdempotencyCache(capacity int) *idempotencyCache {
	if capacity <= 0 {
		capacity = defaultIdempotencyCapacity
	}
	return &idempotencyCache{
		capacity: capacity,
		set:      make(map[string]struct{}),
	}
}

// reserve records key and reports whether it had already been seen. The
// check-and-record is atomic so concurrent retries with the same key resolve
// to exactly one reservation: the first call returns false (caller should
// process), every other returns true (caller should treat as a duplicate).
func (c *idempotencyCache) reserve(key string) (duplicate bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.set[key]; ok {
		return true
	}
	c.set[key] = struct{}{}
	c.ring = append(c.ring, key)
	if len(c.ring) > c.capacity {
		oldest := c.ring[0]
		c.ring = c.ring[1:]
		delete(c.set, oldest)
	}
	return false
}

// release forgets key so a subsequent request with it is processed again.
// Used to roll back a reservation when the reserved operation fails, so a
// failure is retryable rather than being mistaken for a completed duplicate.
func (c *idempotencyCache) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.set[key]; !ok {
		return
	}
	delete(c.set, key)
	for i, k := range c.ring {
		if k == key {
			c.ring = append(c.ring[:i], c.ring[i+1:]...)
			break
		}
	}
}
