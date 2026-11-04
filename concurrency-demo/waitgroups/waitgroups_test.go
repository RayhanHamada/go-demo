package waitgroups_test

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"concurrency-demo/internal/testutil"
	"concurrency-demo/waitgroups"
)

func TestSumParallelMatchesSequential(t *testing.T) {
	nums := make([]int, 1000)
	want := 0
	for i := range nums {
		nums[i] = i + 1
		want += nums[i]
	}

	for _, workers := range []int{0, 1, 2, 7, 1000, 5000} {
		if got := waitgroups.SumParallel(nums, workers); got != want {
			t.Errorf("SumParallel(workers=%d) = %d, want %d", workers, got, want)
		}
	}
}

func TestSumParallelEmpty(t *testing.T) {
	if got := waitgroups.SumParallel(nil, 4); got != 0 {
		t.Errorf("SumParallel(nil) = %d, want 0", got)
	}
}

func TestRunParallelVisitsEveryIndexOnce(t *testing.T) {
	testutil.NoLeak(t)

	const n = 100
	seen := make([]atomic.Int64, n)

	waitgroups.RunParallel(n, func(i int) {
		seen[i].Add(1)
	})

	for i := range seen {
		if got := seen[i].Load(); got != 1 {
			t.Fatalf("index %d visited %d times, want 1", i, got)
		}
	}
}

func TestRunAllJoinsErrors(t *testing.T) {
	testutil.NoLeak(t)

	errBoom := errors.New("boom")
	errBang := errors.New("bang")

	err := waitgroups.RunAll([]func() error{
		func() error { return nil },
		func() error { return errBoom },
		func() error { return nil },
		func() error { return errBang },
	})
	if !errors.Is(err, errBoom) || !errors.Is(err, errBang) {
		t.Fatalf("err = %v, want both %v and %v", err, errBoom, errBang)
	}
}

func TestRunAllNoErrors(t *testing.T) {
	testutil.NoLeak(t)

	err := waitgroups.RunAll([]func() error{
		func() error { return nil },
		func() error { return nil },
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestBoundedRunRespectsLimit(t *testing.T) {
	testutil.NoLeak(t)

	const limit = 2
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)

	tasks := make([]func(), 12)
	for i := range tasks {
		tasks[i] = func() {
			mu.Lock()
			inFlight++
			peak = max(peak, inFlight)
			mu.Unlock()

			time.Sleep(time.Millisecond)

			mu.Lock()
			inFlight--
			mu.Unlock()
		}
	}

	waitgroups.BoundedRun(limit, tasks)
	if peak > limit {
		t.Fatalf("peak concurrency = %d, want <= %d", peak, limit)
	}
}

func TestBoundedRunRunsEveryTask(t *testing.T) {
	testutil.NoLeak(t)

	var count atomic.Int64
	tasks := make([]func(), 10)
	for i := range tasks {
		tasks[i] = func() { count.Add(1) }
	}

	waitgroups.BoundedRun(3, tasks)
	if got := count.Load(); got != 10 {
		t.Fatalf("ran %d tasks, want 10", got)
	}
}

func TestRunRoundsKeepsRoundsSequential(t *testing.T) {
	testutil.NoLeak(t)

	const rounds, perRound = 5, 8
	var (
		mu       sync.Mutex
		order    []int
		finished atomic.Int64
	)

	waitgroups.RunRounds(rounds, perRound, func(round, i int) {
		// Every task of round r must start after all tasks of r-1 finished.
		if round > 0 {
			if done := finished.Load(); done < int64(round*perRound) {
				t.Errorf("round %d started with only %d of %d tasks finished", round, done, round*perRound)
			}
		}
		mu.Lock()
		order = append(order, round)
		mu.Unlock()
		finished.Add(1)
	})

	if len(order) != rounds*perRound {
		t.Fatalf("tasks ran %d times, want %d", len(order), rounds*perRound)
	}
	if !slices.IsSorted(order) {
		t.Fatalf("rounds interleaved: %v", order)
	}
}
