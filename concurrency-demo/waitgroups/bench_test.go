package waitgroups_test

import (
	"sync/atomic"
	"testing"

	"concurrency-demo/waitgroups"
)

// BenchmarkSumParallel shows the scaling of a CPU-bound chunked fan-out: the
// per-goroutine work shrinks with GOMAXPROCS while the mutex-guarded merge is
// constant. Run with -cpu 1,2,4,8.
func BenchmarkSumParallel(b *testing.B) {
	nums := make([]int, 100_000)
	for i := range nums {
		nums[i] = i
	}

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(workerName(workers), func(b *testing.B) {
			for b.Loop() {
				waitgroups.SumParallel(nums, workers)
			}
		})
	}
}

func workerName(n int) string {
	switch n {
	case 1:
		return "workers=1"
	case 2:
		return "workers=2"
	case 4:
		return "workers=4"
	default:
		return "workers=8"
	}
}

// BenchmarkBoundedRun isolates the semaphore overhead from the task body: each
// task is a single atomic add, so the number reports dispatch cost.
func BenchmarkBoundedRun(b *testing.B) {
	var n atomic.Int64
	for b.Loop() {
		tasks := make([]func(), 64)
		for i := range tasks {
			tasks[i] = func() { n.Add(1) }
		}
		waitgroups.BoundedRun(8, tasks)
	}
}
