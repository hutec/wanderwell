package strava

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"
)

func acquireGateReceipt(t *testing.T, g *rateLimitGate, kind requestKind) gateReservation {
	t.Helper()
	start := time.Now()
	r, err := g.acquire(context.Background(), kind)
	if err != nil {
		t.Fatalf("acquire = %v, %v; want reservation and nil error", r, err)
	}
	if !time.Now().Equal(start) {
		t.Fatal("acquire unexpectedly waited for a reset")
	}
	return r
}

func observeGate(g *rateLimitGate, r gateReservation, headers http.Header) {
	g.observe(r, &http.Response{Header: headers})
}

func gateHeaders(overallLimit, overallUsage, readLimit, readUsage string) http.Header {
	headers := make(http.Header)
	for name, value := range map[string]string{
		"X-RateLimit-Limit":     overallLimit,
		"X-RateLimit-Usage":     overallUsage,
		"X-ReadRateLimit-Limit": readLimit,
		"X-ReadRateLimit-Usage": readUsage,
	} {
		if value != "" {
			headers.Set(name, value)
		}
	}
	return headers
}

func assertGateLimits(t *testing.T, g *rateLimitGate, overallQuarterHour, overallDaily, readQuarterHour, readDaily int) {
	t.Helper()
	g.mu.Lock()
	got := [4]int{g.overall.quarterHour.limit, g.overall.daily.limit, g.read.quarterHour.limit, g.read.daily.limit}
	g.mu.Unlock()
	if want := [4]int{overallQuarterHour, overallDaily, readQuarterHour, readDaily}; got != want {
		t.Fatalf("limits [overall quarter-hour/daily, read quarter-hour/daily] = %v, want %v", got, want)
	}
}

func TestGateRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 15, 0, 0, time.UTC) // Exactly on a boundary.
	fallback := now.Add(15 * time.Minute)
	for _, test := range []struct {
		value string
		want  time.Time
	}{
		{" 2 ", now.Add(2 * time.Second)},
		{now.Add(time.Hour).Format(http.TimeFormat), now.Add(time.Hour)},
		{"", fallback},
		{"bad", fallback},
		{"-1", fallback},
		{"0", fallback},
		{"18446744073709551616", fallback}, // Integer overflow.
		{"9223372037", fallback},           // Duration overflow.
		{now.Format(http.TimeFormat), fallback},
		{now.Add(-time.Second).Format(http.TimeFormat), fallback},
	} {
		t.Run(test.value, func(t *testing.T) {
			if got := gateRetryAfter(test.value, now); !got.Equal(test.want) {
				t.Fatalf("deadline = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRateLimitGateCooldownExtendsAcrossResets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		midnight := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		time.Sleep(time.Until(midnight.Add(-time.Minute)))
		quota := rateLimitGateQuota{QuarterHourLimit: 10, DailyLimit: 10}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		oldest := acquireGateReceipt(t, g, requestRead)
		middle := acquireGateReceipt(t, g, requestRead)
		newest := acquireGateReceipt(t, g, requestRead)
		observe := func(r gateReservation, until time.Time) {
			g.observe(r, &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{
				"Retry-After": {until.Format(http.TimeFormat)},
			}})
		}
		observe(newest, midnight.Add(time.Minute))
		observe(middle, midnight) // An older 429 cannot shorten the cooldown.
		if !g.cooldownUntil.Equal(midnight.Add(time.Minute)) {
			t.Fatal("shorter observation shortened cooldown")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, requestWrite)
		assertGateWaiting(t, done)
		time.Sleep(time.Until(midnight)) // Both quota windows reset, not cooldown.
		assertGateWaiting(t, done)
		until := midnight.Add(2 * time.Minute)
		observe(oldest, until)            // Even a prior-day receipt may extend cooldown.
		assertGateUsage(t, g, 0, 0, 0, 0) // But not apply stale quota data.
		time.Sleep(time.Minute)           // Original cooldown deadline.
		assertGateWaiting(t, done)
		time.Sleep(time.Until(until) - time.Nanosecond)
		assertGateWaiting(t, done)
		time.Sleep(time.Nanosecond)
		assertGateResult(t, done, nil)
		assertGateUsage(t, g, 1, 1, 0, 0)
	})
}

func TestParseGatePair(t *testing.T) {
	maxInt := strconv.Itoa(math.MaxInt)
	for _, test := range []struct {
		name    string
		values  []string
		minimum int
		want    [2]int
		ok      bool
	}{
		{name: "missing"},
		{name: "empty", values: []string{""}},
		{name: "whitespace", values: []string{" \t12 , 34\t "}, minimum: 1, want: [2]int{12, 34}, ok: true},
		{name: "zero usage", values: []string{"0,0"}, want: [2]int{}, ok: true},
		{name: "maximum", values: []string{maxInt + "," + maxInt}, minimum: 1, want: [2]int{math.MaxInt, math.MaxInt}, ok: true},
		{name: "zero first limit", values: []string{"0,2"}, minimum: 1},
		{name: "zero second limit", values: []string{"2,0"}, minimum: 1},
		{name: "negative first", values: []string{"-1,2"}},
		{name: "negative second", values: []string{"2,-1"}},
		{name: "one integer", values: []string{"2"}},
		{name: "three integers", values: []string{"1,2,3"}},
		{name: "missing first", values: []string{",2"}},
		{name: "missing second", values: []string{"2,"}},
		{name: "invalid first", values: []string{"bad,2"}},
		{name: "invalid second", values: []string{"2,bad"}},
		{name: "trailing garbage", values: []string{"2,3x"}},
		{name: "fractional", values: []string{"2,3.0"}},
		{name: "overflow", values: []string{"2," + maxInt + "0"}},
		{name: "repeated", values: []string{"1,2", "3,4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := make(http.Header)
			for _, value := range test.values {
				headers.Add("X-RateLimit-Usage", value)
			}
			got, ok := parseGatePair(headers, "X-RateLimit-Usage", test.minimum)
			if got != test.want || ok != test.ok {
				t.Fatalf("parseGatePair = %v, %v; want %v, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRateLimitGateObserveReadAndWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newTestRateLimitGate(t, rateLimitGateConfig{
			Overall: rateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 200, QuarterHourUsage: 5, DailyUsage: 10},
			Read:    rateLimitGateQuota{QuarterHourLimit: 60, DailyLimit: 100, QuarterHourUsage: 2, DailyUsage: 4},
		})
		read := acquireGateReceipt(t, g, requestRead)
		observeGate(g, read, gateHeaders("80,150", "8,20", "50,90", "4,6"))
		assertGateUsage(t, g, 8, 20, 4, 6)
		assertGateLimits(t, g, 80, 150, 50, 90)

		write := acquireGateReceipt(t, g, requestWrite)
		assertGateUsage(t, g, 9, 21, 4, 6)
		observeGate(g, write, gateHeaders("90,160", "9,21", "55,95", "7,12"))
		// Write responses may report read usage, but cannot settle a read grant.
		assertGateUsage(t, g, 9, 21, 7, 12)
		assertGateLimits(t, g, 90, 160, 55, 95)
	})
}

func TestRateLimitGateReconcilesConcurrentUsage(t *testing.T) {
	for _, test := range []struct {
		name          string
		first, second string
		firstIsB      bool
		laterGrant    bool
		intermediate  int
		final         int
	}{
		{"local only", "1,1", "2,2", false, false, 2, 2},
		{"external usage", "11,11", "12,12", false, false, 12, 12},
		{"out of order", "12,12", "11,11", true, false, 13, 12},
		{"out of order with later grant", "12,12", "11,11", true, true, 13, 13},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				quota := rateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
				g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
				a := acquireGateReceipt(t, g, requestRead)
				b := acquireGateReceipt(t, g, requestRead)
				assertGateUsage(t, g, 2, 2, 2, 2)
				first, second := a, b
				if test.firstIsB {
					first, second = b, a
				}
				observeGate(g, first, gateHeaders("", test.first, "", test.first))
				n := test.intermediate
				assertGateUsage(t, g, n, n, n, n)
				var later gateReservation
				if test.laterGrant {
					later = acquireGateReceipt(t, g, requestRead)
					assertGateUsage(t, g, n+1, n+1, n+1, n+1)
				}
				observeGate(g, second, gateHeaders("", test.second, "", test.second))
				n = test.final
				assertGateUsage(t, g, n, n, n, n)
				if test.laterGrant {
					observeGate(g, later, gateHeaders("", "13,13", "", "13,13"))
					assertGateUsage(t, g, 13, 13, 13, 13)
				}
			})
		})
	}
}

func TestRateLimitGateIncompleteUsageDoesNotRefund(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers http.Header
	}{
		{"nil", nil},
		{"missing", http.Header{}},
		{"limits only", gateHeaders("100,100", "", "100,100", "")},
		{"malformed", gateHeaders("", "11,bad", "", "bad,11")},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				quota := rateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
				g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
				a := acquireGateReceipt(t, g, requestRead)
				observeGate(g, a, gateHeaders("", "10,10", "", "10,10"))
				b := acquireGateReceipt(t, g, requestRead)
				observeGate(g, b, test.headers)
				assertGateUsage(t, g, 11, 11, 11, 11)
				c := acquireGateReceipt(t, g, requestRead)
				// Valid newer headers cannot erase B's unconfirmed grant. The
				// possible overlap stays conservative for the rest of the window.
				observeGate(g, c, gateHeaders("", "12,12", "", "12,12"))
				assertGateUsage(t, g, 13, 13, 13, 13)
			})
		})
	}
}

func TestRateLimitGateMalformedConcurrentCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := rateLimitGateQuota{QuarterHourLimit: 4, DailyLimit: 4}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, requestRead)
		b := acquireGateReceipt(t, g, requestRead)
		observeGate(g, a, gateHeaders("", "1,bad", "", "1,bad"))
		assertGateUsage(t, g, 2, 2, 2, 2)
		c := acquireGateReceipt(t, g, requestRead)
		observeGate(g, b, gateHeaders("", "2,2", "", "2,2"))
		assertGateUsage(t, g, 4, 4, 4, 4) // Protect unknown A and outstanding C.
		observeGate(g, c, gateHeaders("", "3,3", "", "3,3"))
		assertGateUsage(t, g, 4, 4, 4, 4) // Unknown A remains, but C is not added twice.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, requestWrite)
		assertGateWaiting(t, done)
		cancel()
		assertGateResult(t, done, context.Canceled)
	})
}

func TestRateLimitGateHeaderPairsAreIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := rateLimitGateQuota{QuarterHourLimit: 50, DailyLimit: 80, QuarterHourUsage: 2, DailyUsage: 3}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, requestRead)
		observeGate(g, a, gateHeaders("10,bad", "11,12", "20,30", "13,bad"))
		assertGateLimits(t, g, 50, 80, 20, 30)
		assertGateUsage(t, g, 11, 12, 3, 4)

		b := acquireGateReceipt(t, g, requestRead)
		observeGate(g, b, gateHeaders("9,10", "bad,13", "bad,40", "7,9"))
		assertGateLimits(t, g, 9, 10, 20, 30)
		assertGateUsage(t, g, 12, 13, 8, 10)
		// Usage above a newly reduced limit is exhausted, not reset or clamped.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, requestWrite)
		assertGateWaiting(t, done)
		cancel()
		assertGateResult(t, done, context.Canceled)
		assertGateUsage(t, g, 12, 13, 8, 10)
	})
}

func TestRateLimitGateUsageSaturates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := rateLimitGateQuota{QuarterHourLimit: math.MaxInt, DailyLimit: math.MaxInt}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, requestRead)
		b := acquireGateReceipt(t, g, requestRead)
		pair := strconv.Itoa(math.MaxInt) + "," + strconv.Itoa(math.MaxInt)
		observeGate(g, a, gateHeaders(pair, pair, pair, pair))
		assertGateUsage(t, g, math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, requestWrite)
		assertGateWaiting(t, done)
		observeGate(g, b, gateHeaders("", "0,0", "", "0,0"))
		assertGateWaiting(t, done)
		assertGateUsage(t, g, math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt)
		cancel()
		assertGateResult(t, done, context.Canceled)
	})
}

func TestRateLimitGateIncreasedLimitsWakeWaiters(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				quota := rateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1}
				g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
				r := acquireGateReceipt(t, g, requestRead)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				read := startGateAcquire(ctx, g, requestRead)
				write := startGateAcquire(ctx, g, requestWrite)
				assertGateWaiting(t, read)
				assertGateWaiting(t, write)
				start := time.Now()
				// A limit-only response wakes both waiters without refunding R,
				// but a simultaneous 429 must hold them until the cooldown ends.
				headers := gateHeaders("3,3", "", "2,2", "")
				headers.Set("Retry-After", "2")
				g.observe(r, &http.Response{StatusCode: status, Header: headers})
				if status == http.StatusTooManyRequests {
					time.Sleep(2*time.Second - time.Nanosecond)
					assertGateWaiting(t, read)
					assertGateWaiting(t, write)
					time.Sleep(time.Nanosecond)
					start = start.Add(2 * time.Second)
				}
				assertGateResult(t, read, nil)
				assertGateResult(t, write, nil)
				assertGateUsage(t, g, 3, 3, 2, 2)
				if !time.Now().Equal(start) {
					t.Fatal("waiters did not resume at the expected deadline")
				}
				blocked := startGateAcquire(ctx, g, requestWrite)
				assertGateWaiting(t, blocked)
				cancel()
				assertGateResult(t, blocked, context.Canceled)
			})
		})
	}
}

func TestRateLimitGateReconciledOverlapWakesWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := rateLimitGateQuota{QuarterHourLimit: 13, DailyLimit: 13}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, requestRead)
		b := acquireGateReceipt(t, g, requestRead)
		observeGate(g, b, gateHeaders("", "12,12", "", "12,12"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, requestRead)
		assertGateWaiting(t, done)
		start := time.Now()
		observeGate(g, a, gateHeaders("", "11,11", "", "11,11"))
		assertGateResult(t, done, nil)
		assertGateUsage(t, g, 13, 13, 13, 13)
		if !time.Now().Equal(start) {
			t.Fatal("reconciliation did not wake waiter immediately")
		}
	})
}

func TestRateLimitGateConcurrentAcquireObserve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := rateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		r := acquireGateReceipt(t, g, requestRead)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const callers = 8
		start := make(chan struct{})
		results := make(chan error, callers)
		for range callers {
			go func() {
				<-start
				_, err := g.acquire(ctx, requestRead)
				results <- err
			}()
		}
		go func() {
			<-start
			g.observe(r, &http.Response{Header: gateHeaders("9,9", "1,1", "9,9", "1,1")})
		}()
		before := time.Now()
		close(start) // Observation can race any waiter's check-and-subscribe.
		for range callers {
			assertGateResult(t, results, nil)
		}
		assertGateUsage(t, g, 9, 9, 9, 9)
		if !time.Now().Equal(before) {
			t.Fatal("concurrent observation lost a wakeup")
		}
	})
}

func TestRateLimitGateOutOfOrderLimits(t *testing.T) {
	for _, oldLimit := range []int{2, 20} {
		t.Run(strconv.Itoa(oldLimit), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				quota := rateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
				g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
				a := acquireGateReceipt(t, g, requestRead)
				b := acquireGateReceipt(t, g, requestRead)
				c := acquireGateReceipt(t, g, requestRead)
				observeGate(g, b, gateHeaders("8,8", "2,2", "8,8", "2,2"))
				pair := strconv.Itoa(oldLimit) + "," + strconv.Itoa(oldLimit)
				observeGate(g, a, gateHeaders(pair, "1,1", pair, "1,1"))
				limit := min(oldLimit, 8) // Old reductions apply; old increases cannot reopen.
				assertGateLimits(t, g, limit, limit, limit, limit)
				assertGateUsage(t, g, 3, 3, 3, 3)
				observeGate(g, c, gateHeaders("12,12", "3,3", "12,12", "3,3"))
				assertGateLimits(t, g, 12, 12, 12, 12)
				assertGateUsage(t, g, 3, 3, 3, 3)
			})
		})
	}
}

func TestRateLimitGateIndependentWindowAttribution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boundary := time.Now().UTC().Truncate(24 * time.Hour).Add(36*time.Hour + 15*time.Minute)
		time.Sleep(time.Until(boundary) - time.Nanosecond)
		quota := rateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
		g := newTestRateLimitGate(t, rateLimitGateConfig{Overall: quota, Read: quota})
		oldQuarter := acquireGateReceipt(t, g, requestRead)
		time.Sleep(time.Nanosecond)
		current := acquireGateReceipt(t, g, requestRead)
		observeGate(g, current, gateHeaders("50,80", "3,10", "40,70", "2,8"))
		assertGateUsage(t, g, 3, 11, 2, 9) // Old request still unresolved for this day.
		observeGate(g, oldQuarter, gateHeaders("99,60", "99,12", "99,50", "99,10"))
		assertGateUsage(t, g, 3, 12, 2, 10)
		assertGateLimits(t, g, 50, 60, 40, 50) // Ignore old short-term limits as well.

		priorDay := acquireGateReceipt(t, g, requestRead)
		midnight := boundary.Truncate(24 * time.Hour).Add(24 * time.Hour)
		time.Sleep(time.Until(midnight))
		current = acquireGateReceipt(t, g, requestRead)
		observeGate(g, current, gateHeaders("20,30", "4,5", "15,25", "2,3"))
		assertGateUsage(t, g, 4, 5, 2, 3)
		observeGate(g, priorDay, gateHeaders("1,1", "1000,1000", "1,1", "1000,1000"))
		assertGateUsage(t, g, 4, 5, 2, 3)
		assertGateLimits(t, g, 20, 30, 15, 25)

		// observe itself must normalize idle windows, not only acquire.
		idle := acquireGateReceipt(t, g, requestRead)
		time.Sleep(48 * time.Hour)
		observeGate(g, idle, gateHeaders("1,1", "1000,1000", "1,1", "1000,1000"))
		assertGateUsage(t, g, 0, 0, 0, 0)
		assertGateLimits(t, g, 20, 30, 15, 25)
		acquireGateReceipt(t, g, requestRead)
		assertGateUsage(t, g, 1, 1, 1, 1)
	})
}
