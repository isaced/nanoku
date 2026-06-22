package api

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDeployLock_TryAcquireRelease(t *testing.T) {
	l := NewDeployLock()

	if !l.TryAcquire(1) {
		t.Fatal("first acquire should succeed")
	}
	if l.TryAcquire(1) {
		t.Fatal("second acquire on same app should fail")
	}
	if !l.IsBusy(1) {
		t.Fatal("IsBusy should be true while held")
	}
	l.Release(1)
	if l.IsBusy(1) {
		t.Fatal("IsBusy should be false after release")
	}
	if !l.TryAcquire(1) {
		t.Fatal("re-acquire after release should succeed")
	}
}

func TestDeployLock_IndependentApps(t *testing.T) {
	l := NewDeployLock()
	if !l.TryAcquire(1) {
		t.Fatal("app 1 acquire")
	}
	if !l.TryAcquire(2) {
		t.Fatal("app 2 acquire should not be blocked by app 1")
	}
	l.Release(1)
	l.Release(2)
}

func TestDeployLock_Concurrent(t *testing.T) {
	l := NewDeployLock()
	const goroutines = 100

	// Each goroutine spins until it acquires, then releases. The invariant is
	// that no two goroutines hold the lock simultaneously: track the
	// observed concurrency and assert it never exceeds 1.
	var (
		wg          sync.WaitGroup
		holding     int32
		maxHolding  int32
		successes   int32
	)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if l.TryAcquire(42) {
					break
				}
				runtime.Gosched()
			}
			n := atomic.AddInt32(&holding, 1)
			for {
				m := atomic.LoadInt32(&maxHolding)
				if n <= m || atomic.CompareAndSwapInt32(&maxHolding, m, n) {
					break
				}
			}
			// Brief critical section so the contention is observable.
			runtime.Gosched()
			atomic.AddInt32(&holding, -1)
			atomic.AddInt32(&successes, 1)
			l.Release(42)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&successes); got != goroutines {
		t.Errorf("expected %d successes, got %d", goroutines, got)
	}
	if got := atomic.LoadInt32(&maxHolding); got != 1 {
		t.Errorf("max concurrent holders = %d, want 1", got)
	}
	if l.IsBusy(42) {
		t.Error("lock should be free after all goroutines finished")
	}
}
