package strava

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func newTestRateLimitGate(t *testing.T, config RateLimitGateConfig) *RateLimitGate {
	t.Helper()
	g, err := NewRateLimitGate(config)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func startGateAcquire(ctx context.Context, g *RateLimitGate, kind RequestKind) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := g.Acquire(ctx, kind)
		done <- err
	}()
	return done
}

func assertGateWaiting(t *testing.T, done <-chan error) {
	t.Helper()
	synctest.Wait()
	select {
	case err := <-done:
		t.Fatalf("Acquire returned early: %v", err)
	default:
	}
}

func assertGateResult(t *testing.T, done <-chan error, want error) {
	t.Helper()
	synctest.Wait()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("Acquire error = %v, want %v", err, want)
		}
	default:
		t.Fatal("Acquire did not return")
	}
}

func assertGateUsage(t *testing.T, g *RateLimitGate, overallQuarterHour, overallDaily, readQuarterHour, readDaily int) {
	t.Helper()
	g.mu.Lock()
	got := [4]int{g.overall.quarterHour.usage(), g.overall.daily.usage(), g.read.quarterHour.usage(), g.read.daily.usage()}
	g.mu.Unlock()
	if want := [4]int{overallQuarterHour, overallDaily, readQuarterHour, readDaily}; got != want {
		t.Fatalf("usage [overall quarter-hour/daily, read quarter-hour/daily] = %v, want %v", got, want)
	}
}

func TestRateLimitGateAvailableCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newTestRateLimitGate(t, RateLimitGateConfig{
			Overall: RateLimitGateQuota{QuarterHourLimit: 5, DailyLimit: 10, QuarterHourUsage: 1, DailyUsage: 2},
			Read:    RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 8, DailyUsage: 1},
		})
		start := time.Now()
		if _, err := g.Acquire(context.Background(), RequestRead); err != nil {
			t.Fatal(err)
		}
		assertGateUsage(t, g, 2, 3, 1, 2)
		if _, err := g.Acquire(context.Background(), RequestWrite); err != nil {
			t.Fatal(err)
		}
		assertGateUsage(t, g, 3, 4, 1, 2)
		if !time.Now().Equal(start) {
			t.Fatal("available capacity did not allow immediate acquisition")
		}
	})
}

func TestRateLimitGateConcurrentReservationsAndReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 2, DailyLimit: 20, QuarterHourUsage: 1, DailyUsage: 1}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const callers = 12
		results := make(chan error, callers)
		start := make(chan struct{})
		for range callers {
			go func() {
				<-start
				_, err := g.Acquire(ctx, RequestRead)
				results <- err
			}()
		}
		close(start)
		synctest.Wait()
		if got := len(results); got != 1 {
			t.Fatalf("%d callers proceeded with one request remaining, want 1", got)
		}
		assertGateResult(t, results, nil)
		assertGateUsage(t, g, 2, 2, 2, 2)

		reset := time.Now().UTC().Truncate(15 * time.Minute).Add(15 * time.Minute)
		time.Sleep(time.Until(reset) - time.Nanosecond)
		assertGateWaiting(t, results)
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := len(results); got != 2 {
			t.Fatalf("%d callers proceeded at reset, want exactly 2", got)
		}
		for range 2 {
			assertGateResult(t, results, nil)
		}
		assertGateUsage(t, g, 2, 4, 2, 4)

		cancel()
		for range callers - 3 {
			assertGateResult(t, results, context.Canceled)
		}
		assertGateUsage(t, g, 2, 4, 2, 4)
		if !time.Now().Equal(reset) {
			t.Fatal("cancellation did not release waiters immediately")
		}
	})
}

func TestRateLimitGateDailyExhaustion(t *testing.T) {
	for _, both := range []bool{false, true} {
		name := "daily only"
		if both {
			name = "both windows and quotas"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				midnight := time.Now().UTC().Truncate(24 * time.Hour).Add(48 * time.Hour)
				time.Sleep(time.Until(midnight.Add(-20 * time.Minute))) // 23:40 UTC.
				config := RateLimitGateConfig{
					Overall: RateLimitGateQuota{QuarterHourLimit: 2, DailyLimit: 4, DailyUsage: 4},
					Read:    RateLimitGateQuota{QuarterHourLimit: 2, DailyLimit: 4},
				}
				kind := RequestWrite
				if both {
					config.Overall.QuarterHourUsage = 2
					config.Read = config.Overall
					kind = RequestRead
				}
				g := newTestRateLimitGate(t, config)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := startGateAcquire(ctx, g, kind)
				assertGateWaiting(t, done)
				time.Sleep(5 * time.Minute) // Quarter-hour reset is not enough.
				assertGateWaiting(t, done)
				time.Sleep(time.Until(midnight) - time.Nanosecond)
				assertGateWaiting(t, done)
				time.Sleep(time.Nanosecond)
				assertGateResult(t, done, nil)
				readUsage := 0
				if both {
					readUsage = 1
				}
				assertGateUsage(t, g, 1, 1, readUsage, readUsage)
			})
		})
	}
}

func TestRateLimitGateReadWriteAccounting(t *testing.T) {
	for _, test := range []struct {
		name         string
		config       RateLimitGateConfig
		writeAllowed bool
	}{
		{
			name: "read quarter-hour exhausted",
			config: RateLimitGateConfig{
				Overall: RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 4, QuarterHourUsage: 1, DailyUsage: 1},
				Read:    RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 4, QuarterHourUsage: 1, DailyUsage: 1},
			},
			writeAllowed: true,
		},
		{
			name: "read daily exhausted",
			config: RateLimitGateConfig{
				Overall: RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 4, QuarterHourUsage: 1, DailyUsage: 2},
				Read:    RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 2, QuarterHourUsage: 1, DailyUsage: 2},
			},
			writeAllowed: true,
		},
		{
			name: "overall quarter-hour exhausted",
			config: RateLimitGateConfig{
				Overall: RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 4, QuarterHourUsage: 1, DailyUsage: 1},
				Read:    RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 4},
			},
		},
		{
			name: "overall daily exhausted",
			config: RateLimitGateConfig{
				Overall: RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 2, QuarterHourUsage: 1, DailyUsage: 2},
				Read:    RateLimitGateQuota{QuarterHourLimit: 3, DailyLimit: 4},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				g := newTestRateLimitGate(t, test.config)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				start := time.Now()
				read := startGateAcquire(ctx, g, RequestRead)
				assertGateWaiting(t, read)
				write := startGateAcquire(ctx, g, RequestWrite)
				overall, readQuota := test.config.Overall, test.config.Read
				if test.writeAllowed {
					assertGateResult(t, write, nil)
					overall.QuarterHourUsage++
					overall.DailyUsage++
				} else {
					assertGateWaiting(t, write)
				}
				// A blocked read must not reserve any part of either quota.
				assertGateUsage(t, g, overall.QuarterHourUsage, overall.DailyUsage, readQuota.QuarterHourUsage, readQuota.DailyUsage)
				cancel()
				assertGateResult(t, read, context.Canceled)
				if !test.writeAllowed {
					assertGateResult(t, write, context.Canceled)
				}
				assertGateUsage(t, g, overall.QuarterHourUsage, overall.DailyUsage, readQuota.QuarterHourUsage, readQuota.DailyUsage)
				if !time.Now().Equal(start) {
					t.Fatal("cancellation waited for a window reset")
				}
			})
		})
	}
}

func TestRateLimitGateAlreadyCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if receipt, err := g.Acquire(ctx, RequestRead); receipt != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire error = %v, want context.Canceled", err)
		}
		deadlineCtx, stop := context.WithDeadline(context.Background(), time.Now())
		defer stop()
		if receipt, err := g.Acquire(deadlineCtx, RequestWrite); receipt != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Acquire error = %v, want context.DeadlineExceeded", err)
		}
		assertGateUsage(t, g, 0, 0, 0, 0)
		if _, err := g.Acquire(context.Background(), RequestRead); err != nil {
			t.Fatal(err)
		}
		assertGateUsage(t, g, 1, 1, 1, 1)
	})
}

func TestRateLimitGateExactBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset time.Duration
	}{
		{"quarter-hour", 15 * time.Minute},
		{"hour", time.Hour},
		{"midnight", 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				day := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
				boundary := day.Add(test.offset)
				time.Sleep(time.Until(boundary) - time.Nanosecond)
				quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 3, QuarterHourUsage: 1, DailyUsage: 2}
				config := RateLimitGateConfig{Overall: quota, Read: quota}
				g := newTestRateLimitGate(t, config)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := startGateAcquire(ctx, g, RequestRead)
				assertGateWaiting(t, done)
				time.Sleep(time.Nanosecond)
				assertGateResult(t, done, nil) // No one-second buffer.
				daily := 3
				if test.name == "midnight" {
					daily = 1
				}
				assertGateUsage(t, g, 1, daily, 1, daily)

				// Initial usage provided exactly on a boundary is for the NEW
				// window, so it must not be discarded by the first acquisition.
				fresh := newTestRateLimitGate(t, config)
				blocked := startGateAcquire(ctx, fresh, RequestRead)
				assertGateWaiting(t, blocked)
				assertGateUsage(t, fresh, 1, 2, 1, 2)
				cancel()
				assertGateResult(t, blocked, context.Canceled)
			})
		})
	}
}

func TestRateLimitGateNormalizesIdleWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1, QuarterHourUsage: 1, DailyUsage: 1}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		time.Sleep(72*time.Hour + 7*time.Minute)
		if _, err := g.Acquire(context.Background(), RequestRead); err != nil {
			t.Fatal(err)
		}
		assertGateUsage(t, g, 1, 1, 1, 1)
		g.mu.Lock()
		quarterHourReset, dailyReset := g.quarterHourReset, g.dailyReset
		g.mu.Unlock()
		now := time.Now().UTC()
		if want := now.Truncate(15 * time.Minute).Add(15 * time.Minute); !quarterHourReset.Equal(want) {
			t.Errorf("quarter-hour reset = %v, want %v", quarterHourReset, want)
		}
		if want := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC); !dailyReset.Equal(want) {
			t.Errorf("daily reset = %v, want %v", dailyReset, want)
		}
	})
}

func TestNewRateLimitGateValidation(t *testing.T) {
	for _, field := range []struct {
		name string
		set  func(*RateLimitGateConfig, int, int)
	}{
		{"overall quarter-hour", func(c *RateLimitGateConfig, limit, usage int) {
			c.Overall.QuarterHourLimit, c.Overall.QuarterHourUsage = limit, usage
		}},
		{"overall daily", func(c *RateLimitGateConfig, limit, usage int) {
			c.Overall.DailyLimit, c.Overall.DailyUsage = limit, usage
		}},
		{"read quarter-hour", func(c *RateLimitGateConfig, limit, usage int) {
			c.Read.QuarterHourLimit, c.Read.QuarterHourUsage = limit, usage
		}},
		{"read daily", func(c *RateLimitGateConfig, limit, usage int) {
			c.Read.DailyLimit, c.Read.DailyUsage = limit, usage
		}},
	} {
		for _, values := range []struct {
			name         string
			limit, usage int
		}{
			{"zero limit", 0, 0},
			{"negative limit", -1, 0},
			{"negative usage", 2, -1},
			{"usage exceeds limit", 2, 3},
			{"zero usage", 2, 0},
			{"usage equals limit", 2, 2},
		} {
			t.Run(field.name+"/"+values.name, func(t *testing.T) {
				quota := RateLimitGateQuota{QuarterHourLimit: 2, DailyLimit: 2}
				config := RateLimitGateConfig{Overall: quota, Read: quota}
				field.set(&config, values.limit, values.usage)
				g, err := NewRateLimitGate(config)
				valid := values.limit > 0 && values.usage >= 0 && values.usage <= values.limit
				if valid && (err != nil || g == nil) {
					t.Fatalf("valid config rejected: gate = %v, error = %v", g, err)
				}
				if !valid && (err == nil || g != nil) {
					t.Fatalf("invalid config accepted: gate = %v, error = %v", g, err)
				}
			})
		}
	}
}

func TestRateLimitGateUnknownRequestKind(t *testing.T) {
	quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1}
	g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
	for _, kind := range []RequestKind{0, -1, RequestWrite + 1} {
		if receipt, err := g.Acquire(context.Background(), kind); receipt != nil || err == nil {
			t.Errorf("Acquire accepted unknown request kind %d", kind)
		}
	}
	assertGateUsage(t, g, 0, 0, 0, 0)
}
