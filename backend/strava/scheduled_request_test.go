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
		{"response", 2, &http.Response{StatusCode: http.StatusOK, Header: gateHeaders("", "2,2", "", "2,2")}, nil},
		{"response and error", 2, &http.Response{StatusCode: http.StatusInternalServerError, Header: gateHeaders("", "2,2", "", "2,2")}, requestErr},
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

func TestExecuteStravaRequestSharedCooldown(t *testing.T) {
	for _, date := range []bool{false, true} {
		name := "seconds"
		if date {
			name = "HTTP date"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := newTestRequestScheduler(t, 2, 1)
				quota := RateLimitGateQuota{QuarterHourLimit: 10, DailyLimit: 20}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				deadline := time.Now().Add(2 * time.Second)
				retryAfter := "2"
				if date {
					retryAfter = deadline.UTC().Format(http.TimeFormat)
				}
				var attempts, otherStarts atomic.Int32
				var workerContext context.Context
				callbackErr := errors.New("429 callback error")
				first := submitStravaRequest(context.Background(), s, g, func(ctx context.Context) (int, *http.Response, error) {
					if attempts.Add(1) == 1 {
						workerContext = ctx
						// No usage or limit headers: cooldown alone must protect peers.
						return 41, &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {retryAfter}}}, callbackErr
					}
					if ctx != workerContext {
						t.Error("retry did not use the same worker context")
					}
					return 42, &http.Response{StatusCode: http.StatusOK}, nil
				})
				synctest.Wait()
				assertGateUsage(t, g, 1, 1, 1, 1)
				other := make(chan requestOutcome, 1)
				go func() {
					value, err := ExecuteStravaRequest(context.Background(), s, g, RequestWrite, func(context.Context) (int, *http.Response, error) {
						otherStarts.Add(1)
						return 99, nil, nil
					})
					other <- requestOutcome{value, err}
				}()
				time.Sleep(time.Until(deadline) - time.Nanosecond)
				synctest.Wait()
				if attempts.Load() != 1 || otherStarts.Load() != 0 {
					t.Fatal("retry or another worker sent before the shared cooldown expired")
				}
				assertGateUsage(t, g, 1, 1, 1, 1) // Waiters hold no partial permission.
				time.Sleep(time.Nanosecond)
				if got := <-first; got.value != 42 || got.err != nil {
					t.Fatalf("retried request = %+v, want (42, nil)", got)
				}
				if got := <-other; got.value != 99 || got.err != nil {
					t.Fatalf("other request = %+v, want (99, nil)", got)
				}
				if attempts.Load() != 2 || otherStarts.Load() != 1 || !time.Now().Equal(deadline) {
					t.Fatal("requests did not resume exactly at the cooldown deadline")
				}
				assertGateUsage(t, g, 3, 3, 2, 2) // Every attempt acquired fresh permission.
			})
		})
	}
}

func TestExecuteStravaRequestRetryLimit(t *testing.T) {
	for _, test := range []struct {
		name, retryAfter, dailyQuota string
		callbackError                bool
	}{
		{name: "headerless"},
		{name: "malformed with callback error", retryAfter: "bad", callbackError: true},
		{name: "read daily quota wins", retryAfter: "1", dailyQuota: "read"},
		{name: "overall daily quota wins", retryAfter: "1", dailyQuota: "overall"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Keep the initial fallback distinct from the daily quota deadline.
				start := time.Now().UTC().Truncate(24 * time.Hour).Add(12*time.Hour + 7*time.Minute)
				time.Sleep(time.Until(start))
				s := newTestRequestScheduler(t, 1, 1)
				quota := RateLimitGateQuota{QuarterHourLimit: 10, DailyLimit: 20}
				config := RateLimitGateConfig{Overall: quota, Read: quota}
				if test.dailyQuota == "read" {
					config.Read.DailyLimit = 1
				} else if test.dailyQuota == "overall" {
					config.Overall.DailyLimit = 1
				}
				g := newTestRateLimitGate(t, config)
				var attempts atomic.Int32
				callbackErrors := []error{errors.New("first 429"), errors.New("second 429"), errors.New("final 429")}
				done := submitStravaRequest(context.Background(), s, g, func(context.Context) (int, *http.Response, error) {
					n := int(attempts.Add(1))
					var err error
					if test.callbackError && n <= len(callbackErrors) {
						err = callbackErrors[n-1]
					}
					var headers http.Header
					if test.retryAfter != "" {
						headers = http.Header{"Retry-After": {test.retryAfter}}
					}
					return n * 10, &http.Response{StatusCode: http.StatusTooManyRequests, Header: headers}, err
				})
				// Step each wait rather than receiving done and letting virtual time
				// hide an immediate retry storm or an extra (fourth) attempt.
				for attempt := int32(1); attempt <= 3; attempt++ {
					synctest.Wait()
					if got := attempts.Load(); got != attempt {
						t.Fatalf("attempts = %d, want %d", got, attempt)
					}
					cooldown := time.Now().UTC().Truncate(15 * time.Minute).Add(15 * time.Minute)
					if test.retryAfter == "1" {
						cooldown = time.Now().Add(time.Second)
					}
					g.mu.Lock()
					gotCooldown := g.cooldownUntil
					g.mu.Unlock()
					if !gotCooldown.Equal(cooldown) {
						t.Fatalf("cooldown = %v, want %v", gotCooldown, cooldown)
					}
					if attempt == 3 {
						got := <-done
						if got.value != 30 || got.err == nil || (test.callbackError && got.err != callbackErrors[2]) {
							t.Fatalf("final request = %+v, want final result and error", got)
						}
					}
					deadline := cooldown
					if test.dailyQuota != "" {
						deadline = time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
					}
					var next <-chan requestOutcome
					var nextStarts atomic.Int32
					if attempt == 3 {
						next = submitStravaRequest(context.Background(), s, g, func(context.Context) (int, *http.Response, error) {
							nextStarts.Add(1)
							return 99, nil, nil
						})
					}
					time.Sleep(time.Until(deadline) - time.Nanosecond)
					synctest.Wait()
					if attempts.Load() != attempt || nextStarts.Load() != 0 {
						t.Fatal("request bypassed cooldown or exhausted quota")
					}
					time.Sleep(time.Nanosecond)
					if next != nil {
						if got := <-next; got.value != 99 || got.err != nil {
							t.Fatalf("subsequent request = %+v", got)
						}
						if attempts.Load() != 3 || !time.Now().Equal(deadline) {
							t.Fatal("retried beyond three attempts or missed next admission deadline")
						}
					}
				}
			})
		})
	}
}

func TestExecuteStravaRequestInterruptsGateWait(t *testing.T) {
	for _, test := range []struct {
		name               string
		shutdown, cooldown bool
	}{
		{"quota/caller cancellation", false, false},
		{"quota/scheduler shutdown", true, false},
		{"cooldown/caller cancellation", false, true},
		{"cooldown/scheduler shutdown", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := newTestRequestScheduler(t, 1, 1)
				quota := RateLimitGateQuota{QuarterHourLimit: 2, DailyLimit: 20, QuarterHourUsage: 2, DailyUsage: 2}
				wantStarts, wantUsage := int32(0), 2
				if test.cooldown {
					quota.QuarterHourUsage, quota.DailyUsage = 0, 0
					wantStarts, wantUsage = 1, 1
				}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				start := time.Now()
				var starts atomic.Int32
				done := submitStravaRequest(ctx, s, g, func(context.Context) (int, *http.Response, error) {
					starts.Add(1)
					return 1, &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"3600"}}}, nil
				})
				assertPendingRequests(t, s, 0) // The only worker is waiting inside Acquire.
				select {
				case got := <-done:
					t.Fatalf("gate waiter returned early: %+v", got)
				default:
				}
				wantErr := context.Canceled
				if test.shutdown {
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
				if starts.Load() != wantStarts || !time.Now().Equal(start) {
					t.Fatalf("cancellation/shutdown: %d callbacks (want %d), or waited for reset/cooldown", starts.Load(), wantStarts)
				}
				assertGateUsage(t, g, wantUsage, wantUsage, wantUsage, wantUsage)
			})
		})
	}
}
