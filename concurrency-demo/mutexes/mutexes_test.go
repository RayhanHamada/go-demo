package mutexes_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"concurrency-demo/internal/testutil"
	"concurrency-demo/mutexes"
)

func TestCounterUnderConcurrency(t *testing.T) {
	testutil.NoLeak(t)

	var c mutexes.Counter
	const goroutines, perGoroutine = 50, 200

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				c.Inc()
			}
		})
	}
	wg.Wait()

	if got, want := c.Value(), goroutines*perGoroutine; got != want {
		t.Fatalf("Value = %d, want %d", got, want)
	}
}

func TestCounterAdd(t *testing.T) {
	var c mutexes.Counter
	c.Add(5)
	c.Add(-2)
	if got := c.Value(); got != 3 {
		t.Fatalf("Value = %d, want 3", got)
	}
}

func TestAtomicCounterUnderConcurrency(t *testing.T) {
	testutil.NoLeak(t)

	var c mutexes.AtomicCounter
	const goroutines, perGoroutine = 50, 200

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				c.Inc()
			}
		})
	}
	wg.Wait()

	if got, want := c.Value(), int64(goroutines*perGoroutine); got != want {
		t.Fatalf("Value = %d, want %d", got, want)
	}
}

func TestStoreBasicOperations(t *testing.T) {
	s := mutexes.NewStore[string, int]()

	if _, ok := s.Get("missing"); ok {
		t.Fatal("Get reported a missing key as present")
	}
	s.Set("a", 1)
	if v, ok := s.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}
	s.Delete("a")
	if _, ok := s.Get("a"); ok {
		t.Fatal("key survived Delete")
	}
	if got := s.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
}

func TestStoreSnapshotIsDetached(t *testing.T) {
	s := mutexes.NewStore[string, int]()
	s.Set("a", 1)

	snap := s.Snapshot()
	snap["a"] = 99
	snap["b"] = 2

	if v, _ := s.Get("a"); v != 1 {
		t.Fatalf("mutating the snapshot changed the store: a = %d", v)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d after snapshot mutation, want 1", s.Len())
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	testutil.NoLeak(t)

	s := mutexes.NewStore[int, int]()
	const writers, readers, perGoroutine = 8, 8, 200

	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range perGoroutine {
				s.Set(w*perGoroutine+i, i)
			}
		})
	}
	for range readers {
		wg.Go(func() {
			for range perGoroutine {
				s.Get(0)
				_ = s.Len()
				_ = s.Snapshot()
			}
		})
	}
	wg.Wait()

	if got, want := s.Len(), writers*perGoroutine; got != want {
		t.Fatalf("Len = %d, want %d", got, want)
	}
}

func TestGetOrLoadLoadsOncePerKey(t *testing.T) {
	testutil.NoLeak(t)

	s := mutexes.NewStore[string, int]()
	var loads atomic.Int64

	load := func(k string) int {
		loads.Add(1)
		return len(k)
	}

	const goroutines = 50
	results := make([]int, goroutines)

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			results[i] = s.GetOrLoad("key", load)
		})
	}
	wg.Wait()

	if got := loads.Load(); got != 1 {
		t.Fatalf("load ran %d times, want 1", got)
	}
	for i, v := range results {
		if v != 3 {
			t.Fatalf("results[%d] = %d, want 3", i, v)
		}
	}
}

func TestGetOrLoadHitSkipsLoad(t *testing.T) {
	s := mutexes.NewStore[string, int]()
	s.Set("key", 42)

	var loads atomic.Int64
	got := s.GetOrLoad("key", func(string) int {
		loads.Add(1)
		return 0
	})
	if got != 42 {
		t.Fatalf("GetOrLoad = %d, want 42", got)
	}
	if loads.Load() != 0 {
		t.Fatalf("load ran %d times on a hit, want 0", loads.Load())
	}
}

func TestOnceLoaderRunsOnce(t *testing.T) {
	testutil.NoLeak(t)

	var calls atomic.Int64
	l := mutexes.NewOnceLoader(func() (string, error) {
		calls.Add(1)
		return "configured", nil
	})

	const goroutines = 50
	results := make([]string, goroutines)

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			v, err := l.Get()
			if err != nil {
				t.Errorf("Get: %v", err)
			}
			results[i] = v
		})
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("load ran %d times, want 1", got)
	}
	for i, v := range results {
		if v != "configured" {
			t.Fatalf("results[%d] = %q, want %q", i, v, "configured")
		}
	}
}

func TestOnceLoaderCachesError(t *testing.T) {
	want := errors.New("unavailable")
	var calls atomic.Int64
	l := mutexes.NewOnceLoader(func() (int, error) {
		calls.Add(1)
		return 0, want
	})

	for range 3 {
		if _, err := l.Get(); !errors.Is(err, want) {
			t.Fatalf("Get err = %v, want %v", err, want)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("load ran %d times after failures, want 1", got)
	}
}

func TestShardedCounterAcrossKeys(t *testing.T) {
	testutil.NoLeak(t)

	c := mutexes.NewShardedCounter(8)
	const goroutines, perGoroutine = 16, 500

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			key := fmt.Sprintf("key-%d", g%4)
			for range perGoroutine {
				c.Inc(key)
			}
		})
	}
	wg.Wait()

	if got, want := c.Total(), goroutines*perGoroutine; got != want {
		t.Fatalf("Total = %d, want %d", got, want)
	}
}

func TestShardedCounterSingleShard(t *testing.T) {
	c := mutexes.NewShardedCounter(0)
	for range 10 {
		c.Inc("only")
	}
	if got := c.Total(); got != 10 {
		t.Fatalf("Total = %d, want 10", got)
	}
}
