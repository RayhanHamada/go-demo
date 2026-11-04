// Command demo runs each concurrency pattern from the module and prints its
// result, so the shapes can be observed instead of only read.
package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"concurrency-demo/channels"
	"concurrency-demo/mutexes"
	"concurrency-demo/waitgroups"
)

func main() {
	ctx := context.Background()

	fmt.Println("== channels ==")
	fmt.Printf("pipeline:        %v\n", channels.Pipeline(ctx, 1, 2, 3, 4, 5))
	fmt.Printf("fan-out squares: %v\n", fanOutSquares(ctx, 4, 1, 2, 3, 4, 5))
	fmt.Printf("worker pool:     %v\n", channels.WorkerPool(ctx, 3, []int{1, 2, 3, 4, 5}, func(n int) int { return n * n }))

	err := channels.BoundedParallel(ctx, 2, []int{1, 2, 3}, func(_ context.Context, n int) error {
		if n == 2 {
			return errors.New("item 2 failed")
		}
		return nil
	})
	fmt.Printf("bounded errors:  %v\n", err)

	stop := make(chan struct{})
	done := channels.AwaitSignal(stop, 3)
	close(stop)
	<-done
	fmt.Println("broadcast:       all 3 workers observed the close")

	fmt.Println("\n== waitgroups ==")
	fmt.Printf("sum parallel:    %d\n", waitgroups.SumParallel([]int{1, 2, 3, 4, 5, 6}, 3))

	var visited atomic.Int64
	waitgroups.RunParallel(10, func(int) { visited.Add(1) })
	fmt.Printf("run parallel:    %d visits\n", visited.Load())

	err = waitgroups.RunAll([]func() error{
		func() error { return nil },
		func() error { return errors.New("task 2 failed") },
	})
	fmt.Printf("run all:         %v\n", err)
	fmt.Printf("bounded peak:    %d (limit 2)\n", boundedPeak(6, 2))

	fmt.Println("\n== mutexes ==")
	var (
		counter mutexes.Counter
		wg      sync.WaitGroup
	)
	for range 8 {
		wg.Go(func() {
			for range 100 {
				counter.Inc()
			}
		})
	}
	wg.Wait()
	fmt.Printf("mutex counter:   %d\n", counter.Value())

	store := mutexes.NewStore[string, int]()
	store.Set("hits", 1)

	var loads atomic.Int64
	for range 5 {
		wg.Go(func() {
			store.GetOrLoad("computed", func(string) int {
				loads.Add(1)
				return 42
			})
		})
	}
	wg.Wait()
	computed, _ := store.Get("computed")
	fmt.Printf("get-or-load:     value=%d loads=%d\n", computed, loads.Load())

	once := mutexes.NewOnceLoader(func() (string, error) { return "connected", nil })
	_, _ = once.Get()
	v, _ := once.Get()
	fmt.Printf("once loader:     %s\n", v)

	sharded := mutexes.NewShardedCounter(4)
	for i := range 100 {
		sharded.Inc(fmt.Sprintf("k%d", i%8))
	}
	fmt.Printf("sharded total:   %d\n", sharded.Total())
}

// fanOutSquares shows fan-out/fan-in: FanOut reorders results, so the caller
// sorts when it needs a comparable line of output.
func fanOutSquares(ctx context.Context, workers int, nums ...int) []int {
	out := channels.FanOut(ctx, workers, channels.Generate(ctx, nums...), func(n int) int { return n * n })
	got := channels.Collect(out)
	slices.Sort(got)
	return got
}

// boundedPeak runs work with a 2-goroutine limit and returns the highest number
// of overlapping tasks observed, which must not exceed the limit.
func boundedPeak(n, limit int) int64 {
	var peak, inFlight atomic.Int64

	tasks := make([]func(), n)
	for i := range tasks {
		tasks[i] = func() {
			cur := inFlight.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			inFlight.Add(-1)
		}
	}

	waitgroups.BoundedRun(limit, tasks)
	return peak.Load()
}
