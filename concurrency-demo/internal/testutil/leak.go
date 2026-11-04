// Package testutil holds helpers shared by the module's tests.
package testutil

import (
	"runtime"
	"testing"
	"time"
)

// NoLeak fails the test if goroutines started during it are still alive when it
// ends. The check polls because a goroutine can still be unwinding after its
// synchronisation point; it never waits forever.
//
// The caller's goroutine count is sampled when NoLeak is called, so call it at
// the top of the test. Do not combine with t.Parallel: other tests' goroutines
// would be counted.
func NoLeak(t *testing.T) {
	t.Helper()

	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(2 * time.Second)
		for {
			after := runtime.NumGoroutine()
			if after <= before {
				return
			}
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<20)
				n := runtime.Stack(buf, true)
				t.Errorf("goroutine leak: %d before, %d after\n%s", before, after, buf[:n])
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// Within fails the test if recv does not produce a value within 5 seconds. It
// turns a deadlocked test into a failure instead of a timeout of the whole
// package.
func Within[T any](t *testing.T, recv <-chan T) T {
	t.Helper()

	select {
	case v := <-recv:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for channel")
		var zero T
		return zero
	}
}
