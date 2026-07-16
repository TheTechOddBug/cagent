package concurrent

import "sync"

// MapSlice applies f to every element of items concurrently and returns the
// results in input order (results[i] = f(items[i])). It blocks until every
// call has returned.
//
// f must be safe to call concurrently. There is no bound on parallelism: one
// goroutine is spawned per element, which suits the small fan-outs this is
// meant for (toolsets, hooks, tool calls) — not thousands of items.
func MapSlice[T, R any](items []T, f func(T) R) []R {
	results := make([]R, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Go(func() {
			results[i] = f(item)
		})
	}
	wg.Wait()
	return results
}

// ForEach calls f on every element of items concurrently and blocks until
// every call has returned. Same parallelism caveats as [MapSlice].
func ForEach[T any](items []T, f func(T)) {
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Go(func() {
			f(item)
		})
	}
	wg.Wait()
}
