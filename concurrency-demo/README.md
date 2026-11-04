# concurrency-demo

Executable examples of Go's three concurrency primitives, each with the shapes
you actually meet in production code and a test that pins the contract.

```
channels/     producer/consumer, pipeline, fan-out/fan-in, semaphore,
              worker pool, closed-channel broadcast, non-blocking send
waitgroups/   manual Add/Done, WaitGroup.Go, bounded run, error collection,
              reuse across rounds
mutexes/      Mutex counter, RWMutex map (+ double-checked load), sync.Once,
              sharded counter, atomic baseline
cmd/demo/     runs every pattern once and prints its result
```

Run it:

```sh
go test -race ./...
go run ./cmd/demo
go test -run xxx -bench . -benchtime=200000x -cpu 1,8 ./mutexes
```

## When to use which

| Need | Use | Why not the alternative |
|---|---|---|
| Hand a stream of values to another goroutine | channel | shared memory needs a lock around every read |
| Wait for N goroutines to finish | `sync.WaitGroup` | a counting channel of completion signals is the same thing, noisier |
| Read-mostly map | `sync.RWMutex` | `Mutex` serialises readers that do not conflict |
| One integer counter | `atomic.Int64` | a mutex costs more than the operation it protects |
| High-contention counter keyed by user/id | sharded `Mutex` | one lock is one cache line all cores fight over |
| Initialise once (config, pool, conn) | `sync.Once` / `OnceValue` | a `Mutex` plus a bool is the same code with a bug waiting |
| One job, one result | plain goroutine + channel | worker pool is for many jobs |

## The rules the code enforces

Every function in `channels` follows these:

1. **The sender closes.** The goroutine that writes a channel closes it; a
   receiver that closes panics against the real sender. `Merge` and `FanOut`
   close from a dedicated goroutine after `wg.Wait()` for exactly this reason.
2. **Every blocking send selects on `ctx.Done()`.** Otherwise a cancelled caller
   leaks the producer parked in a send forever.
3. **`Add` before the goroutine starts** (or `WaitGroup.Go`, which does it for
   you). Otherwise `Wait` can return while the group is still empty.
4. **The lock covers exactly the invariant it protects** — no more (a lock held
   across I/O serialises the service) and no less (returning an internal map
   hands out unprotected state; see `Store.Snapshot`).
5. **Do not copy a type with a mutex.** All exported types here are used behind
   pointers, and `go vet` reports copies.

## Deliberate tradeoffs

- `WorkerPool` does not take `ctx` into the task function, so cancellation stops
  dispatch but queued work still runs. `BoundedParallel` is the interruptible
  variant.
- `Store.GetOrLoad` runs the loader under the write lock: one load per key, but a
  slow loader blocks unrelated writers. Per-key `singleflight` is the fix.
- `ShardedCounter.Total` takes one shard lock at a time, so its result is exact
  only when no increments are racing. Linearisable totals need either a global
  atomic or a stop-the-world sum.
- `sync.Map` (or an `atomic.Pointer` + copy-on-write map) beats both
  `RWMutex` and sharding for read-heavy maps with disjoint key sets; the
  `RWMutex` version is here because it is the widely-understood baseline.

## Tests

Each test asserts a contract a broken implementation would violate:

- `channels`: values all arrive, cancellation closes streams instead of leaking,
  the semaphore's peak concurrency never exceeds the limit, errors are joined.
- `waitgroups`: counts match sequential results, every index is visited exactly
  once, rounds do not interleave, errors are joined.
- `mutexes`: no lost increments at 10k increments across 50 goroutines, the
  loader runs once per key, snapshots are detached.

`internal/testutil.NoLeak` fails a test if goroutines started by it are still
alive at cleanup, so the cancellation tests prove the "no leak" claim rather
than assume it. Benchmarks in each package compare the strategies under
`b.RunParallel`; run them with `-cpu 1,8` to see contention.
