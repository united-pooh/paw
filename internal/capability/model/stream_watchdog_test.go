package model

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type watchdogTestBody struct {
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int32
	closeErr   error
	read       func([]byte) (int, error)
	onClose    func()
}

func newWatchdogTestBody() *watchdogTestBody {
	return &watchdogTestBody{closed: make(chan struct{})}
}

func (b *watchdogTestBody) Read(p []byte) (int, error) {
	if b.read != nil {
		return b.read(p)
	}
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *watchdogTestBody) Close() error {
	b.closeCalls.Add(1)
	if b.onClose != nil {
		b.onClose()
	}
	b.closeOnce.Do(func() { close(b.closed) })
	return b.closeErr
}

type watchdogTestContext struct {
	context.Context
	registrations atomic.Int32
	stops         atomic.Int32
}

// Hide the parent's cancelCtx so context.AfterFunc uses this context's hook.
func (c *watchdogTestContext) Value(any) any { return nil }

func (c *watchdogTestContext) AfterFunc(f func()) func() bool {
	c.registrations.Add(1)
	stop := context.AfterFunc(c.Context, f)
	return func() bool {
		c.stops.Add(1)
		return stop()
	}
}

func TestStreamIdleWatchdogIgnoresExpireAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := newWatchdogTestBody()
		watcher := newStreamIdleWatchdog(context.Background(), body, time.Second)
		if err := watcher.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		watcher.expire()
		if watcher.TimedOut() {
			t.Error("stale expiry marked a closed watchdog as timed out")
		}
		if got := body.closeCalls.Load(); got != 1 {
			t.Errorf("body.Close calls = %d, want 1", got)
		}
	})
}

func TestStreamIdleWatchdogIgnoresStaleExpireAfterRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := newWatchdogTestBody()
		body.read = func(p []byte) (int, error) {
			time.Sleep(750 * time.Millisecond)
			return copy(p, "x"), nil
		}
		watcher := newStreamIdleWatchdog(nil, body, time.Second)
		defer watcher.Close()
		if n, err := watcher.Read(make([]byte, 1)); n != 1 || err != nil {
			t.Fatalf("Read() = (%d, %v), want (1, nil)", n, err)
		}
		time.Sleep(250 * time.Millisecond)
		watcher.expire()
		if watcher.TimedOut() || body.closeCalls.Load() != 0 {
			t.Fatal("callback for the original deadline closed the recently active body")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if watcher.TimedOut() || body.closeCalls.Load() != 0 {
			t.Fatal("watchdog expired without a pending Read")
		}
		body.read = nil
		readDone := make(chan struct{})
		go func() {
			_, _ = watcher.Read(make([]byte, 1))
			close(readDone)
		}()
		synctest.Wait()
		watcher.expire()
		if watcher.TimedOut() || body.closeCalls.Load() != 0 {
			t.Fatal("stale callback closed the next Read before its deadline")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if !watcher.TimedOut() || body.closeCalls.Load() != 1 {
			t.Fatal("watchdog did not expire at the reset deadline")
		}
		<-readDone
	})
}

func TestStreamIdleWatchdogLateReadDoesNotRestartTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := newWatchdogTestBody()
		started, release := make(chan struct{}), make(chan struct{})
		body.read = func(p []byte) (int, error) {
			close(started)
			<-release
			return copy(p, "x"), nil
		}
		watcher := newStreamIdleWatchdog(nil, body, time.Second)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			if n, err := watcher.Read(make([]byte, 1)); n != 1 || err != nil {
				t.Errorf("Read() = (%d, %v), want (1, nil)", n, err)
			}
		}()
		<-started
		_ = watcher.Close()
		close(release)
		<-readDone
		watcher.mu.Lock()
		restarted := watcher.timer.Stop()
		watcher.mu.Unlock()
		if restarted {
			t.Error("Read restarted the timer after Close")
		}
		watcher.expire()
		if watcher.TimedOut() || body.closeCalls.Load() != 1 {
			t.Error("late Read or callback changed the closed watchdog")
		}
	})
}

func TestStreamIdleWatchdogTerminationClosesOnceAndUnblocksRead(t *testing.T) {
	for _, termination := range []string{"close", "cancel", "idle", "already_cancelled"} {
		t.Run(termination, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if termination == "already_cancelled" {
					cancel()
				}
				body := newWatchdogTestBody()
				watcher := newStreamIdleWatchdog(ctx, body, time.Second)
				readDone := make(chan error, 1)
				go func() {
					_, err := watcher.Read(make([]byte, 1))
					readDone <- err
				}()
				synctest.Wait()
				switch termination {
				case "close":
					_ = watcher.Close()
				case "cancel":
					cancel()
				case "idle":
					time.Sleep(time.Second)
				}
				synctest.Wait()
				select {
				case err := <-readDone:
					if !errors.Is(err, io.ErrClosedPipe) {
						t.Errorf("Read error = %v, want closed pipe", err)
					}
				default:
					t.Fatal("termination did not unblock Read")
				}
				for range 3 {
					watcher.expire()
					_ = watcher.Close()
				}
				cancel()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if got, want := watcher.TimedOut(), termination == "idle"; got != want {
					t.Errorf("TimedOut() = %v, want %v", got, want)
				}
				if got := body.closeCalls.Load(); got != 1 {
					t.Errorf("body.Close calls = %d, want 1", got)
				}
			})
		})
	}
}

func TestStreamIdleWatchdogStopsContextCallback(t *testing.T) {
	for _, termination := range []string{"close", "idle", "disabled_timeout"} {
		t.Run(termination, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx := &watchdogTestContext{Context: parent}
				timeout := time.Second
				if termination == "disabled_timeout" {
					timeout = 0
				}
				body := newWatchdogTestBody()
				watcher := newStreamIdleWatchdog(ctx, body, timeout)
				defer watcher.Close()
				if got := ctx.registrations.Load(); got != 1 {
					t.Fatalf("context callback registrations = %d, want 1", got)
				}
				if termination == "idle" {
					go func() { _, _ = watcher.Read(make([]byte, 1)) }()
					synctest.Wait()
					time.Sleep(time.Second)
					synctest.Wait()
				} else {
					_ = watcher.Close()
				}
				if got := ctx.stops.Load(); got != 1 {
					t.Errorf("context callback stops = %d, want 1 before parent cancellation", got)
				}
				cancel()
				synctest.Wait()
				if got := body.closeCalls.Load(); got != 1 {
					t.Errorf("body.Close calls = %d, want 1", got)
				}
			})
		})
	}
}

func TestStreamIdleWatchdogCloseErrorAndReentry(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "idle"}[idle], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := newWatchdogTestBody()
				body.closeErr = errors.New("close failed")
				var watcher *streamIdleWatchdog
				ready := make(chan struct{})
				body.onClose = func() {
					<-ready
					if got := watcher.TimedOut(); got != idle {
						t.Errorf("TimedOut() during body.Close = %v, want %v", got, idle)
					}
					if err := watcher.Close(); err != nil {
						t.Errorf("reentrant Close() = %v, want nil", err)
					}
				}
				watcher = newStreamIdleWatchdog(nil, body, time.Second)
				close(ready)
				if idle {
					go func() { _, _ = watcher.Read(make([]byte, 1)) }()
					synctest.Wait()
					time.Sleep(time.Second)
					synctest.Wait()
				} else if err := watcher.Close(); !errors.Is(err, body.closeErr) {
					t.Errorf("Close() = %v, want body close error", err)
				}
				if err := watcher.Close(); err != nil {
					t.Errorf("repeated Close() = %v, want nil", err)
				}
				if got := body.closeCalls.Load(); got != 1 {
					t.Errorf("body.Close calls = %d, want 1", got)
				}
			})
		})
	}
}

func TestStreamIdleWatchdogOnlyCountsPendingReads(t *testing.T) {
	for _, progress := range []bool{false, true} {
		t.Run(map[bool]string{false: "zero_read_keeps_budget", true: "data_resets_budget"}[progress], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				body := newWatchdogTestBody()
				watcher := newStreamIdleWatchdog(ctx, body, time.Second)
				defer watcher.Close()
				time.Sleep(2 * time.Second)
				if body.closeCalls.Load() != 0 {
					t.Fatal("body closed before its first Read")
				}
				body.read = func([]byte) (int, error) {
					time.Sleep(600 * time.Millisecond)
					return 0, nil
				}
				_, _ = watcher.Read(make([]byte, 1))
				if progress {
					body.read = func(p []byte) (int, error) {
						time.Sleep(300 * time.Millisecond)
						return copy(p, "x"), nil
					}
					_, _ = watcher.Read(make([]byte, 1))
				}
				time.Sleep(2 * time.Second)
				if body.closeCalls.Load() != 0 {
					t.Fatal("consumer time was charged to the read budget")
				}
				body.read = nil
				start := time.Now()
				_, err := watcher.Read(make([]byte, 1))
				want := 400 * time.Millisecond
				if progress {
					want = time.Second
				}
				if !errors.Is(err, io.ErrClosedPipe) || time.Since(start) != want || !watcher.TimedOut() {
					t.Fatalf("Read err=%v waited=%s timedOut=%v, want blocked read timeout after %s", err, time.Since(start), watcher.TimedOut(), want)
				}
			})
		})
	}
}

func TestStreamIdleWatchdogCancellationWhileConsumerIsPaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := newWatchdogTestBody()
		body.read = func(p []byte) (int, error) { return copy(p, "x"), nil }
		watcher := newStreamIdleWatchdog(ctx, body, time.Second)
		_, _ = watcher.Read(make([]byte, 1))
		time.Sleep(2 * time.Second)
		cancel()
		synctest.Wait()
		if watcher.TimedOut() || body.closeCalls.Load() != 1 {
			t.Fatalf("paused consumer cancellation: timedOut=%v closeCalls=%d", watcher.TimedOut(), body.closeCalls.Load())
		}
	})
}

func TestStreamIdleWatchdogConcurrentLifecycle(t *testing.T) {
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		body := newWatchdogTestBody()
		body.read = func(p []byte) (int, error) { return copy(p, "x"), nil }
		watcher := newStreamIdleWatchdog(ctx, body, time.Nanosecond)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for _, work := range []func(){
			func() { _, _ = watcher.Read(make([]byte, 1)) },
			watcher.expire,
			func() { _ = watcher.Close() },
			func() { _ = watcher.TimedOut() },
			cancel,
		} {
			workers.Go(func() {
				<-start
				for range 20 {
					work()
				}
			})
		}
		close(start)
		workers.Wait()
		_ = watcher.Close()
		select {
		case <-body.closed:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent termination did not close the body")
		}
		if got := body.closeCalls.Load(); got != 1 {
			t.Fatalf("body.Close calls = %d, want 1 under concurrent lifecycle operations", got)
		}
	}
}
