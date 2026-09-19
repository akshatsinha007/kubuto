package scanner

import "sync"

// scanConcurrency bounds how many per-resource network fetches
// (index.yaml lookups, git Chart.yaml fetches) any one scanner runs at
// once. Shared by helm.go/argocd.go/flux.go so a cluster with thousands
// of resources in one namespace doesn't fetch them one at a time, but
// also doesn't fire off unbounded simultaneous requests against a
// chart repo or git host.
const scanConcurrency = 20

// parallelMap calls fn once per item in items, running up to `limit`
// calls concurrently, and returns the results in the same order as
// items (results[i] corresponds to items[i]) regardless of completion
// order. A limit < 1 is treated as 1 (fully sequential) rather than
// "unbounded" — every call site must pick an explicit ceiling.
func parallelMap[T, R any](items []T, limit int, fn func(T) R) []R {
	if limit < 1 {
		limit = 1
	}

	results := make([]R, len(items))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, item T) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fn(item)
		}(i, item)
	}

	wg.Wait()
	return results
}
