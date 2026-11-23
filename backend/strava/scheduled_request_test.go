package strava

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func submitStravaRequest(ctx context.Context, s *RequestScheduler, g *RateLimitGate, request func(context.Context) (int, *http.Response, error)) <-chan requestOutcome {
	done := make(chan requestOutcome, 1)
	go func() {
		value, err := ExecuteStravaRequest(ctx, s, g, RequestRead, request)
		done <- requestOutcome{value, err}
	}()
	return done
}

func TestExecuteStravaRequestConcurrentBudgetAndReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const callers = 12
		s := newTestRequestScheduler(t, callers, 2)
		quota := RateLimitGateQuota{QuarterHourLimit: 2, DailyLimit: 20, QuarterHourUsage: 1, DailyUsage: 1}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var starts atomic.Int32
		outcomes := make([]<-chan requestOutcome, callers)
		for i := range callers {
			outcomes[i] = submitStravaRequest(ctx, s, g, func(context.Context) (int, *http.Response, error) {
				starts.Add(1)
				return i*i + 1, nil, nil
			})
		}
		synctest.Wait()
		if got := starts.Load(); got != 1 {
			t.Fatalf("started %d requests with one permission remaining, want 1", got)
		}
		assertGateUsage(t, g, 2, 2, 2, 2)

		reset := time.Now().UTC().Truncate(15 * time.Minute).Add(15 * time.Minute)
		time.Sleep(time.Until(reset) - time.Nanosecond)
		synctest.Wait()
		if got := starts.Load(); got != 1 {
			t.Fatalf("started %d requests before reset, want 1", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := starts.Load(); got != 3 {
			t.Fatalf("started %d requests after reset, want 3 (exactly two more)", got)
		}
		assertGateUsage(t, g, 2, 4, 2, 4)

		cancel()
		synctest.Wait()
		completed := 0
		for i, done := range outcomes {
			got := <-done
			if got.err == nil {
				completed++
				if got.value != i*i+1 {
					t.Errorf("caller %d got result %d, want %d", i, got.value, i*i+1)
				}
			} else if !errors.Is(got.err, context.Canceled) {
				t.Errorf("caller %d error = %v, want context.Canceled", i, got.err)
			}
		}
		if completed != 3 || starts.Load() != 3 {
			t.Fatalf("completed %d requests, started %d; want 3 each", completed, starts.Load())
		}
		s.Shutdown()
		assertGateUsage(t, g, 2, 4, 2, 4)
		if !time.Now().Equal(reset) {
			t.Fatal("cancellation/shutdown waited for another reset")
		}
	})
}

func TestExecuteStravaRequestObservesCompletion(t *testing.T) {
	requestErr := errors.New("request failed")
	for _, test := range []struct {
		name     string
		limit    int
		response *http.Response
		err      error
	}{
		{"response", 2, &http.Response{Header: gateHeaders("", "2,2", "", "2,2")}, nil},
		{"response and error", 2, &http.Response{Header: gateHeaders("", "2,2", "", "2,2")}, requestErr},
		{"nil response and error", 1, nil, requestErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := newTestRequestScheduler(t, 1, 1)
				quota := RateLimitGateQuota{QuarterHourLimit: test.limit, DailyLimit: 20}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				started, release := make(chan struct{}), make(chan struct{})
				var starts atomic.Int32
				first := submitStravaRequest(context.Background(), s, g, func(ctx context.Context) (int, *http.Response, error) {
					starts.Add(1)
					close(started)
					select {
					case <-release:
						return 41, test.response, test.err
					case <-ctx.Done():
						return 0, nil, ctx.Err()
					}
				})
				<-started
				next := submitStravaRequest(context.Background(), s, g, func(context.Context) (int, *http.Response, error) {
					starts.Add(1)
					return 99, nil, nil
				})
				assertPendingRequests(t, s, 1)
				close(release)
				if got := <-first; got.value != 41 || got.err != test.err {
					t.Fatalf("first request = %+v, want (41, %v)", got, test.err)
				}
				assertWaiting := func() {
					t.Helper()
					synctest.Wait()
					if got := starts.Load(); got != 1 {
						t.Fatalf("started %d requests before reset, want 1", got)
					}
					select {
					case got := <-next:
						t.Fatalf("next request returned before reset: %+v", got)
					default:
					}
				}
				assertWaiting()
				reset := time.Now().UTC().Truncate(15 * time.Minute).Add(15 * time.Minute)
				time.Sleep(time.Until(reset) - time.Nanosecond)
				assertWaiting()
				time.Sleep(time.Nanosecond)
				if got := <-next; got.value != 99 || got.err != nil {
					t.Fatalf("next request = %+v, want (99, nil)", got)
				}
				if starts.Load() != 2 || !time.Now().Equal(reset) {
					t.Fatalf("started %d requests at %v, want 2 at %v", starts.Load(), time.Now(), reset)
				}
			})
		})
	}
}

func TestExecuteStravaRequestInterruptsGateWait(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "caller cancellation"
		if shutdown {
			name = "scheduler shutdown"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := newTestRequestScheduler(t, 1, 1)
				quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 20, QuarterHourUsage: 1, DailyUsage: 1}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				start := time.Now()
				var sent atomic.Bool
				done := submitStravaRequest(ctx, s, g, func(context.Context) (int, *http.Response, error) {
					sent.Store(true)
					return 1, nil, nil
				})
				assertPendingRequests(t, s, 0) // The only worker is waiting inside Acquire.
				select {
				case got := <-done:
					t.Fatalf("gate waiter returned early: %+v", got)
				default:
				}
				wantErr := context.Canceled
				if shutdown {
					wantErr = ErrSchedulerClosed
					s.Shutdown()
				} else {
					cancel()
					// Prove the worker was released by caller cancellation alone.
					if _, err := ExecuteRequest(context.Background(), s, func(context.Context) (int, error) {
						return 0, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				if got := <-done; !errors.Is(got.err, wantErr) {
					t.Fatalf("gate waiter error = %v, want %v", got.err, wantErr)
				}
				if sent.Load() || !time.Now().Equal(start) {
					t.Fatal("cancellation/shutdown sent the callback or waited for reset")
				}
				assertGateUsage(t, g, 1, 1, 1, 1)
			})
		})
	}
}
