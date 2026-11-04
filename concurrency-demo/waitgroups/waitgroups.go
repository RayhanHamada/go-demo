// Package waitgroups shows the shapes of sync.WaitGroup use: manual Add/Done,
// WaitGroup.Go for "one goroutine per task", a semaphore-bounded group, error
// collection, and reuse across rounds.
//
// The invariant that matters everywhere below: Add happens before the goroutine
// is started, or before Wait for goroutines started later, and Done runs on
// every return path. A Wait that races the Add of a goroutine it is supposed to
// wait for can return immediately.
package waitgroups

import (
	"errors"
	"sync"
)

// SumParallel adds nums with roughly workers chunks. It is the classic manual
// shape: Add(1) before starting each goroutine, defer Done() inside it, and one
// Wait at the end. The shared total needs its own mutex; a WaitGroup orders
// lifecycles, it does not protect data.
func SumParallel(nums []int, workers int) int {
	if len(nums) == 0 {
		return 0
	}
	if workers < 1 {
		workers = 1
	}
	workers = min(workers, len(nums))

	chunk := (len(nums) + workers - 1) / workers
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)

	for start := 0; start < len(nums); start += chunk {
		end := min(start+chunk, len(nums))
		wg.Add(1)
		go func() {
			defer wg.Done()
			sum := 0
			for _, n := range nums[start:end] {
				sum += n
			}
			mu.Lock()
			total += sum
			mu.Unlock()
		}()
	}

	wg.Wait()
	return total
}

// RunParallel calls fn(i) for every i in [0, n). WaitGroup.Go folds Add and the
// goroutine startup into one call, so the group cannot be waited on before the
// task is registered. Use it when the number of goroutines is the number of
// tasks and no fancier control is needed.
func RunParallel(n int, fn func(int)) {
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { fn(i) })
	}
	wg.Wait()
}

// RunAll runs tasks concurrently and returns all their errors joined. A
// WaitGroup alone has no result slot, so the error slice needs a mutex; the
// WaitGroup only guarantees the writers are finished before the caller reads.
func RunAll(tasks []func() error) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, task := range tasks {
		wg.Go(func() {
			if err := task(); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	return errors.Join(errs...)
}

// BoundedRun runs every task with at most limit in flight. Acquiring the
// buffered channel happens before wg.Go, so the goroutine count is bounded by
// construction rather than by a task deciding to yield.
func BoundedRun(limit int, tasks []func()) {
	if limit < 1 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, task := range tasks {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			task()
		})
	}
	wg.Wait()
}

// RunRounds demonstrates safe reuse: a WaitGroup may be reused after Wait has
// returned, never before. Waiting between rounds is what makes the next
// round's Go calls legal.
func RunRounds(rounds, perRound int, fn func(round, i int)) {
	var wg sync.WaitGroup
	for r := range rounds {
		for i := range perRound {
			wg.Go(func() { fn(r, i) })
		}
		wg.Wait()
	}
}
