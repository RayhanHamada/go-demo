package channels_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"concurrency-demo/channels"
	"concurrency-demo/internal/testutil"
)

func TestPipelineSquaresEveryValue(t *testing.T) {
	testutil.NoLeak(t)

	got := channels.Pipeline(context.Background(), 1, 2, 3, 4)
	want := []int{1, 4, 9, 16}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("Pipeline = %v, want %v", got, want)
	}
}

func TestGenerateClosesOnCancel(t *testing.T) {
	testutil.NoLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	ch := channels.Generate(ctx, 1, 2, 3)

	// Nothing is reading, so the producer is parked in a send. Cancel must
	// unpark it and close the channel.
	cancel()

	// A send that was already selected before the cancel can still be
	// delivered, so the contract is "at most the offered values, then close" —
	// not "exactly zero values".
	got := channels.Collect(ch)
	if len(got) > 3 {
		t.Fatalf("received %v from a 3-value generator", got)
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel still open after cancel")
	}
}

func TestMergeCombinesAllChannels(t *testing.T) {
	testutil.NoLeak(t)

	ctx := context.Background()
	a := channels.Generate(ctx, 1, 2, 3)
	b := channels.Generate(ctx, 4, 5)
	c := channels.Generate(ctx, 6)

	got := channels.Collect(channels.Merge(ctx, a, b, c))
	slices.Sort(got)
	if want := []int{1, 2, 3, 4, 5, 6}; !slices.Equal(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
}

func TestMergeOfNothingCloses(t *testing.T) {
	testutil.NoLeak(t)

	ch := channels.Merge(context.Background())
	if _, ok := <-ch; ok {
		t.Fatal("empty merge produced a value")
	}
}

func TestFanOutAppliesFnToEveryValue(t *testing.T) {
	testutil.NoLeak(t)

	ctx := context.Background()
	in := channels.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9)

	var calls atomic.Int64
	out := channels.FanOut(ctx, 4, in, func(n int) int {
		calls.Add(1)
		return n * 10
	})
	got := channels.Collect(out)

	if calls.Load() != 9 {
		t.Errorf("fn calls = %d, want 9", calls.Load())
	}
	if len(got) != 9 {
		t.Errorf("results = %d, want 9", len(got))
	}
	slices.Sort(got)
	if want := []int{10, 20, 30, 40, 50, 60, 70, 80, 90}; !slices.Equal(got, want) {
		t.Errorf("FanOut = %v, want %v", got, want)
	}
}

func TestFanOutCancelledDoesNotStrandWorkers(t *testing.T) {
	testutil.NoLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	// An endless input that only cancellation can stop. The test must not
	// close it: the producer owns the channel and is the only closer.
	in := make(chan int)
	go func() {
		for i := 0; ; i++ {
			select {
			case in <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	out := channels.FanOut(ctx, 3, in, func(n int) int { return n })
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range out {
		}
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	// FanOut closes out once its workers exit, so this proves none is stranded.
	testutil.Within(t, drained)
	if _, ok := <-out; ok {
		t.Fatal("out produced a value after it was closed")
	}
}

func TestBoundedParallelRespectsLimit(t *testing.T) {
	testutil.NoLeak(t)

	const limit = 3
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)

	items := make([]int, 20)
	for i := range items {
		items[i] = i
	}

	err := channels.BoundedParallel(context.Background(), limit, items, func(_ context.Context, _ int) error {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()

		time.Sleep(2 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("BoundedParallel: %v", err)
	}
	if peak > limit {
		t.Fatalf("peak concurrency = %d, want <= %d", peak, limit)
	}
	if peak < 2 {
		t.Fatalf("peak concurrency = %d; work did not overlap", peak)
	}
}

func TestBoundedParallelProcessesEveryItem(t *testing.T) {
	testutil.NoLeak(t)

	var mu sync.Mutex
	seen := map[int]bool{}
	items := []int{1, 2, 3, 4, 5}

	err := channels.BoundedParallel(context.Background(), 2, items, func(_ context.Context, n int) error {
		mu.Lock()
		seen[n] = true
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("BoundedParallel: %v", err)
	}
	if len(seen) != len(items) {
		t.Fatalf("processed %d items, want %d", len(seen), len(items))
	}
}

func TestBoundedParallelJoinsErrors(t *testing.T) {
	testutil.NoLeak(t)

	errA := errors.New("a failed")
	errB := errors.New("b failed")

	err := channels.BoundedParallel(context.Background(), 2, []int{1, 2, 3}, func(_ context.Context, n int) error {
		switch n {
		case 1:
			return errA
		case 3:
			return errB
		}
		return nil
	})
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("err = %v, want both %v and %v", err, errA, errB)
	}
}

func TestBoundedParallelCancelledStartsNoWork(t *testing.T) {
	testutil.NoLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int64
	err := channels.BoundedParallel(ctx, 2, []int{1, 2, 3}, func(context.Context, int) error {
		calls.Add(1)
		return nil
	})
	if calls.Load() != 0 {
		t.Fatalf("fn calls = %d after pre-cancellation, want 0", calls.Load())
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWorkerPoolKeepsInputAssociation(t *testing.T) {
	testutil.NoLeak(t)

	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	got := channels.WorkerPool(context.Background(), 3, items, func(n int) int { return n * n })

	want := make([]int, len(items))
	for i, n := range items {
		want[i] = n * n
	}
	if !slices.Equal(got, want) {
		t.Fatalf("WorkerPool = %v, want %v", got, want)
	}
}

func TestWorkerPoolCancelledStopsDispatch(t *testing.T) {
	testutil.NoLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	items := make([]int, 100)
	for i := range items {
		items[i] = i
	}

	got := channels.WorkerPool(ctx, 2, items, func(n int) int { return n })
	for i, v := range got {
		if v != 0 {
			t.Fatalf("slot %d was processed after pre-cancellation", i)
		}
	}
}

func TestAwaitSignalWaitsForAllWorkers(t *testing.T) {
	testutil.NoLeak(t)

	stop := make(chan struct{})
	done := channels.AwaitSignal(stop, 5)

	select {
	case <-done:
		t.Fatal("done closed while workers were still blocked")
	case <-time.After(20 * time.Millisecond):
	}

	close(stop)
	testutil.Within(t, done)
}

func TestTrySend(t *testing.T) {
	ch := make(chan int, 1)
	if !channels.TrySend(ch, 1) {
		t.Fatal("first TrySend on an empty buffered channel failed")
	}
	if channels.TrySend(ch, 2) {
		t.Fatal("second TrySend on a full buffered channel succeeded")
	}
	if got := <-ch; got != 1 {
		t.Fatalf("received %d, want 1", got)
	}
}
