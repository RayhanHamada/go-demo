package channels_test

import (
	"context"
	"testing"

	"concurrency-demo/channels"
)

// BenchmarkBoundedParallel measures the semaphore+WaitGroup dispatch path with
// a no-op body, so it reports overhead rather than work.
func BenchmarkBoundedParallel(b *testing.B) {
	items := make([]int, 64)
	noop := func(context.Context, int) error { return nil }

	for b.Loop() {
		_ = channels.BoundedParallel(context.Background(), 8, items, noop)
	}
}

// BenchmarkWorkerPool measures pool startup plus job hand-off for the same
// amount of work.
func BenchmarkWorkerPool(b *testing.B) {
	items := make([]int, 64)
	square := func(n int) int { return n * n }

	for b.Loop() {
		_ = channels.WorkerPool(context.Background(), 8, items, square)
	}
}
