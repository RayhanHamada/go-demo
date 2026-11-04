// Package channels demonstrates the common shapes of channel use:
// producer/consumer, pipeline stages, fan-out/fan-in, counting semaphores,
// worker pools and closed-channel broadcast.
//
// Two rules run through every function here:
//
//   - The goroutine that writes a channel owns it and is the one that closes
//     it. Receivers must never close.
//   - A goroutine that can block on a send or receive also selects on
//     ctx.Done(), so a cancelled caller does not strand it.
package channels

import (
	"context"
	"errors"
	"sync"
)

// Generate sends nums on the returned channel and closes it. The channel is
// unbuffered, so production pace is the consumer's pace: a slow reader holds
// the producer in the send. Cancelling ctx abandons the remaining values and
// closes the channel; the producer never leaks.
func Generate(ctx context.Context, nums ...int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for _, n := range nums {
			select {
			case out <- n:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Square is one pipeline stage: it reads in, writes n*n, and closes its output
// when the input closes. Directional types (receive-only in, send-only out)
// document the data flow at compile time.
//
// Cancellation propagates backwards because every stage in the chain selects
// on the same ctx and stops reading; an upstream stage's blocked send then
// resolves as done. Cancel the whole chain, never a single stage.
func Square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for {
			select {
			case n, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- n * n:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Merge is fan-in: it forwards every channel into one output and closes the
// output once all inputs are closed (or ctx is cancelled). It needs a WaitGroup
// plus a dedicated closer goroutine, because closing from a forwarder that
// races another forwarder's send panics.
func Merge(ctx context.Context, chans ...<-chan int) <-chan int {
	out := make(chan int)

	var wg sync.WaitGroup
	for _, ch := range chans {
		wg.Go(func() {
			for {
				select {
				case v, ok := <-ch:
					if !ok {
						return
					}
					select {
					case out <- v:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// FanOut is the distribution half of fan-out/fan-in: workers goroutines pull
// from in and call fn. Order is not preserved, which is the point: results come
// back as soon as any worker finishes. Pair it with Merge to recombine streams
// or with Collect to wait for one.
func FanOut(ctx context.Context, workers int, in <-chan int, fn func(int) int) <-chan int {
	if workers < 1 {
		workers = 1
	}
	out := make(chan int)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				select {
				case v, ok := <-in:
					if !ok {
						return
					}
					select {
					case out <- fn(v):
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// Pipeline wires Generate -> Square -> Merge. The Merge step is redundant for a
// single stage; it is kept so the composition reads like the multi-stage case.
func Pipeline(ctx context.Context, nums ...int) []int {
	stage := Square(ctx, Generate(ctx, nums...))
	return Collect(Merge(ctx, stage))
}

// Collect drains a channel into a slice. It returns when the channel is closed,
// so it is only safe on a channel whose producer is guaranteed to close it.
func Collect(in <-chan int) []int {
	var out []int
	for v := range in {
		out = append(out, v)
	}
	return out
}

// BoundedParallel runs fn over items with at most limit calls in flight. The
// buffered channel is a counting semaphore: a send acquires a slot, a receive
// releases it. Unlike a worker pool this keeps the input order of dispatch and
// returns every error via errors.Join, plus ctx's own error when cancelled.
//
// fn receives ctx so in-flight work can abort. On cancellation the remaining
// items are not started; in-flight calls are waited for.
func BoundedParallel(ctx context.Context, limit int, items []int, fn func(context.Context, int) error) error {
	if limit < 1 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

dispatch:
	for _, item := range items {
		// Checking ctx before the select keeps behaviour deterministic: once
		// cancelled, no new work starts even though the semaphore has room.
		if ctx.Err() != nil {
			break dispatch
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Go(func() {
			defer func() { <-sem }()
			if err := fn(ctx, item); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	// No goroutine can append after Wait, so the slice is safe to read here.
	if err := ctx.Err(); err != nil {
		return errors.Join(append(errs, err)...)
	}
	return errors.Join(errs...)
}

// WorkerPool is the jobs/results shape. The producer owns jobs and closes it to
// tell the workers "no more work"; workers own their slice element, so results
// keep their association with the input index even though completion order is
// arbitrary.
//
// Cancelling ctx stops further dispatch; already-queued items are still
// processed because fn takes no ctx. Use BoundedParallel when work must be
// interruptible.
func WorkerPool(ctx context.Context, workers int, items []int, fn func(int) int) []int {
	if workers < 1 {
		workers = 1
	}

	out := make([]int, len(items))
	jobs := make(chan int)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				out[i] = fn(items[i])
			}
		})
	}

dispatch:
	for i := range items {
		if ctx.Err() != nil {
			break dispatch
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

// AwaitSignal broadcasts a stop to workers and reports when all of them have
// returned. A close is a broadcast: every receiver wakes, whereas a send wakes
// exactly one. Each worker therefore blocks on the same channel, and done
// closes only after the WaitGroup drains.
func AwaitSignal(stop <-chan struct{}, workers int) <-chan struct{} {
	done := make(chan struct{})

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { <-stop })
	}

	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

// TrySend offers v without blocking and reports whether it was queued. The
// default branch makes the select non-blocking; use it for telemetry or
// "best effort" queues that must never stall a caller.
func TrySend(ch chan<- int, v int) bool {
	select {
	case ch <- v:
		return true
	default:
		return false
	}
}
