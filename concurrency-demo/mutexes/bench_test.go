package mutexes_test

import (
	"sync/atomic"
	"testing"

	"concurrency-demo/mutexes"
)

// The benchmarks put the increment strategies under b.RunParallel, which is the
// contention pattern a mutex is actually judged on. Run them with -cpu values
// that span the machine, e.g. -cpu 1,8.
//
// Use -benchtime with a fixed number of iterations when comparing, since the
// sharded and atomic variants are fast enough that the default time-based run
// can finish before several goroutines have started.
func BenchmarkIncrement(b *testing.B) {
	b.Run("Mutex", func(b *testing.B) {
		var c mutexes.Counter
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.Inc()
			}
		})
		if got, want := c.Value(), b.N; got != want {
			b.Fatalf("Value = %d, want %d (lost updates)", got, want)
		}
	})

	b.Run("Atomic", func(b *testing.B) {
		var c mutexes.AtomicCounter
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.Inc()
			}
		})
		if got, want := c.Value(), int64(b.N); got != want {
			b.Fatalf("Value = %d, want %d", got, want)
		}
	})

	b.Run("Sharded", func(b *testing.B) {
		const shards = 16
		c := mutexes.NewShardedCounter(shards)

		// RunParallel does not expose a worker id, so each goroutine claims one
		// and uses it as its key: every worker then owns a shard. A single
		// shared key would just re-measure one mutex.
		var next atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			key := string(rune('a' + next.Add(1)%shards))
			for pb.Next() {
				c.Inc(key)
			}
		})
		if got, want := c.Total(), b.N; got != want {
			b.Fatalf("Total = %d, want %d (lost updates)", got, want)
		}
	})
}

// BenchmarkStoreReadHeavy measures the case RWMutex exists for: many readers,
// occasional writers. The writer path is taken 1% of the time.
func BenchmarkStoreReadHeavy(b *testing.B) {
	s := mutexes.NewStore[int, int]()
	for i := range 100 {
		s.Set(i, i)
	}

	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			if i%100 == 0 {
				s.Set(i%100, i)
			} else {
				s.Get(i % 100)
			}
			i++
		}
	})
}
