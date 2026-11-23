package strava

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type requestOutcome struct {
	value int
	err   error
}

func submitRequest(ctx context.Context, s *requestScheduler, request func(context.Context) (int, error)) <-chan requestOutcome {
	done := make(chan requestOutcome, 1)
	go func() {
		value, err := executeRequest(ctx, s, request)
		done <- requestOutcome{value, err}
	}()
	return done
}

func newTestRequestScheduler(t *testing.T, workers, capacity int) *requestScheduler {
	t.Helper()
	s, err := newRequestScheduler(workers, capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.shutdown)
	return s
}

func assertPendingRequests(t *testing.T, s *requestScheduler, want int) {
	t.Helper()
	synctest.Wait()
	s.mu.Lock()
	got := len(s.pending)
	s.mu.Unlock()
	if got != want {
		t.Fatalf("pending requests = %d, want %d", got, want)
	}
}

// holdRequest occupies one worker until release is closed or shutdown cancels it.
func holdRequest(s *requestScheduler) (release chan struct{}, done <-chan requestOutcome) {
	release = make(chan struct{})
	started := make(chan struct{})
	done = submitRequest(context.Background(), s, func(ctx context.Context) (int, error) {
		close(started)
		select {
		case <-release:
			return 1, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	<-started
	return release, done
}

func TestNewRequestSchedulerInvalidSettings(t *testing.T) {
	for _, settings := range [][2]int{{0, 1}, {-1, 1}, {1, 0}, {1, -1}} {
		if s, err := newRequestScheduler(settings[0], settings[1]); err == nil {
			s.shutdown()
			t.Errorf("newRequestScheduler(%d, %d) succeeded", settings[0], settings[1])
		}
	}
}

func TestRequestSchedulerResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTestRequestScheduler(t, 3, 2)
		value, err := executeRequest(context.Background(), s, func(context.Context) (string, error) {
			return "typed result", nil
		})
		if value != "typed result" || err != nil {
			t.Fatalf("executeRequest = (%q, %v)", value, err)
		}

		const callers = 24
		start := make(chan struct{})
		outcomes := make([]<-chan requestOutcome, callers)
		wantErrors := make([]error, callers)
		for i := range callers {
			if i%2 == 1 {
				wantErrors[i] = fmt.Errorf("request %d failed", i)
			}
			outcomes[i] = submitRequest(context.Background(), s, func(context.Context) (int, error) {
				<-start
				return i * i, wantErrors[i]
			})
		}
		synctest.Wait()
		close(start)
		for i, done := range outcomes {
			if got := <-done; got.value != i*i || got.err != wantErrors[i] {
				t.Errorf("caller %d got (%d, %v), want (%d, %v)", i, got.value, got.err, i*i, wantErrors[i])
			}
		}
	})
}

func TestRequestSchedulerConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const workers, capacity, callers = 3, 2, 12
		s := newTestRequestScheduler(t, workers, capacity)
		release := make(chan struct{})
		var active atomic.Int32
		outcomes := make([]<-chan requestOutcome, callers)
		for i := range callers {
			outcomes[i] = submitRequest(context.Background(), s, func(ctx context.Context) (int, error) {
				if n := active.Add(1); n > workers {
					t.Errorf("%d active requests exceed %d workers", n, workers)
				}
				defer active.Add(-1)
				select {
				case <-release:
					return i, nil
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			})
		}
		assertPendingRequests(t, s, capacity)
		if n := active.Load(); n != workers {
			t.Fatalf("active requests = %d, want %d (all workers in use)", n, workers)
		}
		close(release)
		for i, done := range outcomes {
			if got := <-done; got.value != i || got.err != nil {
				t.Errorf("caller %d got %+v", i, got)
			}
		}
	})
}

func TestRequestSchedulerCanceledQueuedRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTestRequestScheduler(t, 1, 1)
		release, running := holdRequest(s)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var executed atomic.Bool
		queued := submitRequest(ctx, s, func(context.Context) (int, error) {
			executed.Store(true)
			return 2, nil
		})
		assertPendingRequests(t, s, 1)
		cancel()
		if got := <-queued; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("queued request error = %v, want context.Canceled", got.err)
		}
		close(release)
		if got := <-running; got.err != nil {
			t.Fatal(got.err)
		}
		// A subsequent result proves the worker has passed the canceled entry.
		if _, err := executeRequest(context.Background(), s, func(context.Context) (int, error) {
			return 3, nil
		}); err != nil {
			t.Fatal(err)
		}
		if executed.Load() {
			t.Fatal("canceled queued request executed")
		}
	})
}

func TestRequestSchedulerCancellationWhileQueueFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTestRequestScheduler(t, 1, 1)
		release, running := holdRequest(s)
		queued := submitRequest(context.Background(), s, func(context.Context) (int, error) {
			return 2, nil
		})
		assertPendingRequests(t, s, 1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var executed atomic.Bool
		blocked := submitRequest(ctx, s, func(context.Context) (int, error) {
			executed.Store(true)
			return 3, nil
		})
		assertPendingRequests(t, s, 1)
		select {
		case got := <-blocked:
			t.Fatalf("full-queue submitter returned early: %+v", got)
		default:
		}
		cancel()
		if got := <-blocked; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("blocked submitter error = %v, want context.Canceled", got.err)
		}
		close(release)
		for _, done := range []<-chan requestOutcome{running, queued} {
			if got := <-done; got.err != nil {
				t.Fatal(got.err)
			}
		}
		if executed.Load() {
			t.Fatal("canceled blocked request executed")
		}
	})
}

func TestRequestSchedulerCallerContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTestRequestScheduler(t, 1, 1)
		type contextKey struct{}
		deadline := time.Now().Add(time.Hour)
		ctx, cancel := context.WithDeadline(context.WithValue(context.Background(), contextKey{}, "caller"), deadline)
		defer cancel()
		started := make(chan struct{})
		canceled := make(chan error, 1)
		done := submitRequest(ctx, s, func(ctx context.Context) (int, error) {
			if ctx.Value(contextKey{}) != "caller" {
				t.Error("callback context lost caller's value")
			}
			if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
				t.Errorf("callback deadline = (%v, %v), want %v", got, ok, deadline)
			}
			close(started)
			<-ctx.Done()
			canceled <- ctx.Err()
			return 0, ctx.Err()
		})
		<-started
		cancel()
		if got := <-done; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("caller error = %v, want context.Canceled", got.err)
		}
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Fatalf("callback error = %v, want context.Canceled", err)
		}
		// An already canceled caller must not execute even with queue space.
		if _, err := executeRequest(ctx, s, func(context.Context) (int, error) {
			t.Error("already canceled request executed")
			return 0, nil
		}); !errors.Is(err, context.Canceled) {
			t.Fatalf("already canceled request error = %v", err)
		}
	})
}

func TestRequestSchedulerShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTestRequestScheduler(t, 1, 1)
		started, canceled, allowExit := make(chan struct{}), make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(allowExit) })
		defer release()
		running := submitRequest(context.Background(), s, func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-allowExit // Prove shutdown waits, but callers need not wait for callbacks.
			return 99, nil
		})
		<-started
		unexpected := func(context.Context) (int, error) {
			t.Error("queued, blocked, or post-shutdown callback executed")
			return 0, nil
		}
		queued := submitRequest(context.Background(), s, unexpected)
		assertPendingRequests(t, s, 1)
		blocked := submitRequest(context.Background(), s, unexpected)
		assertPendingRequests(t, s, 1)

		const shutdownCallers = 3
		stopped := make(chan struct{}, shutdownCallers)
		for range shutdownCallers {
			go func() {
				s.shutdown()
				stopped <- struct{}{}
			}()
		}
		synctest.Wait()
		for _, done := range []<-chan requestOutcome{running, queued, blocked} {
			if got := <-done; !errors.Is(got.err, ErrSchedulerClosed) {
				t.Fatalf("shutdown caller error = %v, want ErrSchedulerClosed", got.err)
			}
		}
		select {
		case <-canceled:
		default:
			t.Fatal("shutdown did not cancel active callback context")
		}
		select {
		case <-stopped:
			t.Fatal("shutdown returned before callback exited")
		default:
		}
		if _, err := executeRequest(context.Background(), s, unexpected); !errors.Is(err, ErrSchedulerClosed) {
			t.Fatalf("post-shutdown request error = %v, want ErrSchedulerClosed", err)
		}
		release()
		for range shutdownCallers {
			<-stopped
		}
		s.shutdown()
	})
}

func TestRequestSchedulerConcurrentSubmissionAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTestRequestScheduler(t, 3, 2)
		const callers = 24
		start := make(chan struct{})
		var callersDone sync.WaitGroup
		callersDone.Add(callers)
		for i := range callers {
			go func() {
				defer callersDone.Done()
				<-start
				value, err := executeRequest(context.Background(), s, func(context.Context) (int, error) {
					return i, nil
				})
				if err != nil && !errors.Is(err, ErrSchedulerClosed) {
					t.Errorf("caller %d error = %v", i, err)
				}
				if err == nil && value != i {
					t.Errorf("caller %d result = %d", i, value)
				}
			}()
		}
		go func() {
			<-start
			s.shutdown()
		}()
		close(start)
		callersDone.Wait()
		s.shutdown()
	})
}
