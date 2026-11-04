// Package mutexes contrasts shared-memory synchronisation styles: Mutex for
// exclusive access, RWMutex for read-mostly data, sharded mutexes for
// contention, sync.Once for run-once initialisation, and atomics where no lock
// is needed at all.
//
// Every exported type here is safe for concurrent use; none of them may be
// copied after first use, because the lock travels with the value.
package mutexes

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
)

// Counter is the smallest correct Mutex: the lock covers exactly the read or
// write of n, and no method returns a pointer to the protected field.
type Counter struct {
	mu sync.Mutex
	n  int
}

// Inc adds one to the counter.
func (c *Counter) Inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

// Add adds delta to the counter.
func (c *Counter) Add(delta int) {
	c.mu.Lock()
	c.n += delta
	c.mu.Unlock()
}

// Value returns the current count.
func (c *Counter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// AtomicCounter is the same counter without a lock. It is included as the
// baseline for the benchmarks: when the critical section is a single arithmetic
// operation, an atomic beats a mutex, and when it is not, atomics stop being
// applicable.
type AtomicCounter struct {
	n atomic.Int64
}

// Inc adds one to the counter.
func (c *AtomicCounter) Inc() { c.n.Add(1) }

// Value returns the current count.
func (c *AtomicCounter) Value() int64 { return c.n.Load() }

// Store is a concurrent map behind an RWMutex. Readers share the lock; writers
// take it exclusively. RWMutex pays off when reads dominate and the critical
// section is long enough to amortise the extra bookkeeping.
type Store[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

// NewStore returns an empty Store.
func NewStore[K comparable, V any]() *Store[K, V] {
	return &Store[K, V]{m: make(map[K]V)}
}

// Get returns the value for key and whether it was present.
func (s *Store[K, V]) Get(key K) (V, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	return v, ok
}

// Set stores v under key.
func (s *Store[K, V]) Set(key K, v V) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = v
}

// Delete removes key.
func (s *Store[K, V]) Delete(key K) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}

// Len returns the number of entries.
func (s *Store[K, V]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// Snapshot returns a shallow copy. Returning the internal map, or a value that
// aliased it, would hand callers a structure the mutex no longer protects.
func (s *Store[K, V]) Snapshot() map[K]V {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[K]V, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// GetOrLoad returns the value for key, calling load on a miss. It is
// double-checked locking: the read lock answers the common hit case, the write
// lock serialises misses and re-checks, so load runs once per key even under
// contention.
//
// load runs while the write lock is held. That is what makes "once per key"
// true, and it also means a slow loader blocks every other writer, including
// misses for unrelated keys. For slow loaders, swap in per-key singleflight.
func (s *Store[K, V]) GetOrLoad(key K, load func(K) V) V {
	s.mu.RLock()
	if v, ok := s.m[key]; ok {
		s.mu.RUnlock()
		return v
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key]; ok {
		return v
	}
	v := load(key)
	s.m[key] = v
	return v
}

// OnceLoader runs a fallible initialiser exactly once and caches both results.
// sync.Once provides the happens-before edge: every caller that returns from Get
// sees the same value and error. A failed load is not retried; wrap the load in
// a retry if that is what the caller needs.
type OnceLoader[T any] struct {
	once sync.Once
	load func() (T, error)
	val  T
	err  error
}

// NewOnceLoader returns a loader that will call load at most once.
func NewOnceLoader[T any](load func() (T, error)) *OnceLoader[T] {
	return &OnceLoader[T]{load: load}
}

// Get returns the initialised value, running load on the first call.
func (l *OnceLoader[T]) Get() (T, error) {
	l.once.Do(func() {
		l.val, l.err = l.load()
	})
	return l.val, l.err
}

type shard struct {
	mu sync.Mutex
	n  int
}

// ShardedCounter spreads increments across independent locks, chosen by hashing
// the key. Contention on one lock is what caps a single mutex, so sharding
// trades memory and a weaker read guarantee for throughput.
//
// The seed comes from maphash, so an attacker cannot precompute keys that all
// land in one shard.
type ShardedCounter struct {
	shards []shard
	seed   maphash.Seed
}

// NewShardedCounter returns a counter with n shards.
func NewShardedCounter(n int) *ShardedCounter {
	if n < 1 {
		n = 1
	}
	return &ShardedCounter{shards: make([]shard, n), seed: maphash.MakeSeed()}
}

// Inc increments the shard for key.
func (c *ShardedCounter) Inc(key string) {
	s := &c.shards[maphash.String(c.seed, key)%uint64(len(c.shards))]
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}

// Total sums the shards. It takes one shard lock at a time, so concurrent Inc
// calls can land in a shard that was already counted: Total is not a
// linearisable snapshot, only a quiescent-read exact count.
func (c *ShardedCounter) Total() int {
	total := 0
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		total += s.n
		s.mu.Unlock()
	}
	return total
}
