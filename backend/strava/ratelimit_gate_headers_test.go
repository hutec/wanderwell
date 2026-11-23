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

func acquireGateReceipt(t *testing.T, g *RateLimitGate, kind RequestKind) *RateLimitReservation {
	t.Helper()
	start := time.Now()
	r, err := g.Acquire(context.Background(), kind)
	if err != nil || r == nil {
		t.Fatalf("Acquire = %v, %v; want receipt and nil error", r, err)
	}
	if !time.Now().Equal(start) {
		t.Fatal("Acquire unexpectedly waited for a reset")
	}
	return r
}

func observeGate(t *testing.T, g *RateLimitGate, r *RateLimitReservation, headers http.Header) {
	t.Helper()
	if err := g.Observe(r, headers); err != nil {
		t.Fatal(err)
	}
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

func assertGateLimits(t *testing.T, g *RateLimitGate, overallQuarterHour, overallDaily, readQuarterHour, readDaily int) {
	t.Helper()
	g.mu.Lock()
	got := [4]int{g.overall.quarterHour.limit, g.overall.daily.limit, g.read.quarterHour.limit, g.read.daily.limit}
	g.mu.Unlock()
	if want := [4]int{overallQuarterHour, overallDaily, readQuarterHour, readDaily}; got != want {
		t.Fatalf("limits [overall quarter-hour/daily, read quarter-hour/daily] = %v, want %v", got, want)
	}
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
		g := newTestRateLimitGate(t, RateLimitGateConfig{
			Overall: RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 200, QuarterHourUsage: 5, DailyUsage: 10},
			Read:    RateLimitGateQuota{QuarterHourLimit: 60, DailyLimit: 100, QuarterHourUsage: 2, DailyUsage: 4},
		})
		read := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, read, gateHeaders("80,150", "8,20", "50,90", "4,6"))
		assertGateUsage(t, g, 8, 20, 4, 6)
		assertGateLimits(t, g, 80, 150, 50, 90)

		write := acquireGateReceipt(t, g, RequestWrite)
		assertGateUsage(t, g, 9, 21, 4, 6)
		observeGate(t, g, write, gateHeaders("90,160", "9,21", "55,95", "7,12"))
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
				quota := RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				a := acquireGateReceipt(t, g, RequestRead)
				b := acquireGateReceipt(t, g, RequestRead)
				assertGateUsage(t, g, 2, 2, 2, 2)
				first, second := a, b
				if test.firstIsB {
					first, second = b, a
				}
				observeGate(t, g, first, gateHeaders("", test.first, "", test.first))
				n := test.intermediate
				assertGateUsage(t, g, n, n, n, n)
				var later *RateLimitReservation
				if test.laterGrant {
					later = acquireGateReceipt(t, g, RequestRead)
					assertGateUsage(t, g, n+1, n+1, n+1, n+1)
				}
				observeGate(t, g, second, gateHeaders("", test.second, "", test.second))
				n = test.final
				assertGateUsage(t, g, n, n, n, n)
				if later != nil {
					observeGate(t, g, later, gateHeaders("", "13,13", "", "13,13"))
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
				quota := RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				a := acquireGateReceipt(t, g, RequestRead)
				observeGate(t, g, a, gateHeaders("", "10,10", "", "10,10"))
				b := acquireGateReceipt(t, g, RequestRead)
				observeGate(t, g, b, test.headers)
				assertGateUsage(t, g, 11, 11, 11, 11)
				c := acquireGateReceipt(t, g, RequestRead)
				// Valid newer headers cannot erase B's unconfirmed grant. The
				// possible overlap stays conservative for the rest of the window.
				observeGate(t, g, c, gateHeaders("", "12,12", "", "12,12"))
				assertGateUsage(t, g, 13, 13, 13, 13)
				if err := g.Observe(b, gateHeaders("", "11,11", "", "11,11")); err == nil {
					t.Fatal("completion without usage did not finalize receipt")
				}
				assertGateUsage(t, g, 13, 13, 13, 13)
			})
		})
	}
}

func TestRateLimitGateMalformedConcurrentCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 4, DailyLimit: 4}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, RequestRead)
		b := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, a, gateHeaders("", "1,bad", "", "1,bad"))
		assertGateUsage(t, g, 2, 2, 2, 2)
		c := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, b, gateHeaders("", "2,2", "", "2,2"))
		assertGateUsage(t, g, 4, 4, 4, 4) // Protect unknown A and outstanding C.
		observeGate(t, g, c, gateHeaders("", "3,3", "", "3,3"))
		assertGateUsage(t, g, 4, 4, 4, 4) // Unknown A remains, but C is not added twice.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, RequestWrite)
		assertGateWaiting(t, done)
		cancel()
		assertGateResult(t, done, context.Canceled)
	})
}

func TestRateLimitGateHeaderPairsAreIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 50, DailyLimit: 80, QuarterHourUsage: 2, DailyUsage: 3}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, a, gateHeaders("10,bad", "11,12", "20,30", "13,bad"))
		assertGateLimits(t, g, 50, 80, 20, 30)
		assertGateUsage(t, g, 11, 12, 3, 4)

		b := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, b, gateHeaders("9,10", "bad,13", "bad,40", "7,9"))
		assertGateLimits(t, g, 9, 10, 20, 30)
		assertGateUsage(t, g, 12, 13, 8, 10)
		// Usage above a newly reduced limit is exhausted, not reset or clamped.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, RequestWrite)
		assertGateWaiting(t, done)
		cancel()
		assertGateResult(t, done, context.Canceled)
		assertGateUsage(t, g, 12, 13, 8, 10)
	})
}

func TestRateLimitGateUsageSaturates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: math.MaxInt, DailyLimit: math.MaxInt}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, RequestRead)
		b := acquireGateReceipt(t, g, RequestRead)
		pair := strconv.Itoa(math.MaxInt) + "," + strconv.Itoa(math.MaxInt)
		observeGate(t, g, a, gateHeaders(pair, pair, pair, pair))
		assertGateUsage(t, g, math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, RequestWrite)
		assertGateWaiting(t, done)
		observeGate(t, g, b, gateHeaders("", "0,0", "", "0,0"))
		assertGateWaiting(t, done)
		assertGateUsage(t, g, math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt)
		cancel()
		assertGateResult(t, done, context.Canceled)
	})
}

func TestRateLimitGateReceiptOwnershipAndIdempotency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		other := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		foreign := acquireGateReceipt(t, other, RequestRead)
		headers := gateHeaders("20,20", "10,10", "20,20", "10,10")
		for _, invalid := range []*RateLimitReservation{nil, {}, foreign} {
			if err := g.Observe(invalid, headers); err == nil {
				t.Fatal("accepted invalid or foreign receipt")
			}
		}
		assertGateUsage(t, g, 0, 0, 0, 0)
		assertGateLimits(t, g, 100, 100, 100, 100)
		assertGateUsage(t, other, 1, 1, 1, 1)
		observeGate(t, other, foreign, nil) // Foreign rejection did not finalize it.

		r := acquireGateReceipt(t, g, RequestRead)
		const observers = 12
		results := make(chan error, observers)
		start := make(chan struct{})
		for range observers {
			copy := *r // Copies must not bypass completion tracking.
			go func() {
				<-start
				results <- g.Observe(&copy, headers)
			}()
		}
		close(start)
		synctest.Wait()
		successes := 0
		for range observers {
			if err := <-results; err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("successful completions = %d, want 1", successes)
		}
		assertGateUsage(t, g, 10, 10, 10, 10)
		if err := g.Observe(r, gateHeaders("1,1", "90,90", "1,1", "90,90")); err == nil {
			t.Fatal("accepted duplicate completion")
		}
		assertGateUsage(t, g, 10, 10, 10, 10)
		assertGateLimits(t, g, 20, 20, 20, 20)
	})
}

func TestRateLimitGateIncreasedLimitsWakeWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		r := acquireGateReceipt(t, g, RequestRead)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		read := startGateAcquire(ctx, g, RequestRead)
		write := startGateAcquire(ctx, g, RequestWrite)
		assertGateWaiting(t, read)
		assertGateWaiting(t, write)
		start := time.Now()
		// A limit-only response must wake both waiters, without refunding R.
		observeGate(t, g, r, gateHeaders("3,3", "", "2,2", ""))
		assertGateResult(t, read, nil)
		assertGateResult(t, write, nil)
		assertGateUsage(t, g, 3, 3, 2, 2)
		if !time.Now().Equal(start) {
			t.Fatal("observation did not wake waiters immediately")
		}
		blocked := startGateAcquire(ctx, g, RequestWrite)
		assertGateWaiting(t, blocked)
		cancel()
		assertGateResult(t, blocked, context.Canceled)
	})
}

func TestRateLimitGateReconciledOverlapWakesWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 13, DailyLimit: 13}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		a := acquireGateReceipt(t, g, RequestRead)
		b := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, b, gateHeaders("", "12,12", "", "12,12"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startGateAcquire(ctx, g, RequestRead)
		assertGateWaiting(t, done)
		start := time.Now()
		observeGate(t, g, a, gateHeaders("", "11,11", "", "11,11"))
		assertGateResult(t, done, nil)
		assertGateUsage(t, g, 13, 13, 13, 13)
		if !time.Now().Equal(start) {
			t.Fatal("reconciliation did not wake waiter immediately")
		}
	})
}

func TestRateLimitGateConcurrentAcquireObserve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quota := RateLimitGateQuota{QuarterHourLimit: 1, DailyLimit: 1}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		r := acquireGateReceipt(t, g, RequestRead)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const callers = 8
		start := make(chan struct{})
		results := make(chan error, callers+1)
		for range callers {
			go func() {
				<-start
				_, err := g.Acquire(ctx, RequestRead)
				results <- err
			}()
		}
		go func() {
			<-start
			results <- g.Observe(r, gateHeaders("9,9", "1,1", "9,9", "1,1"))
		}()
		before := time.Now()
		close(start) // Observation can race any waiter's check-and-subscribe.
		for range callers + 1 {
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
				quota := RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
				g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
				a := acquireGateReceipt(t, g, RequestRead)
				b := acquireGateReceipt(t, g, RequestRead)
				c := acquireGateReceipt(t, g, RequestRead)
				observeGate(t, g, b, gateHeaders("8,8", "2,2", "8,8", "2,2"))
				pair := strconv.Itoa(oldLimit) + "," + strconv.Itoa(oldLimit)
				observeGate(t, g, a, gateHeaders(pair, "1,1", pair, "1,1"))
				limit := min(oldLimit, 8) // Old reductions apply; old increases cannot reopen.
				assertGateLimits(t, g, limit, limit, limit, limit)
				assertGateUsage(t, g, 3, 3, 3, 3)
				observeGate(t, g, c, gateHeaders("12,12", "3,3", "12,12", "3,3"))
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
		quota := RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 100}
		g := newTestRateLimitGate(t, RateLimitGateConfig{Overall: quota, Read: quota})
		oldQuarter := acquireGateReceipt(t, g, RequestRead)
		time.Sleep(time.Nanosecond)
		current := acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, current, gateHeaders("50,80", "3,10", "40,70", "2,8"))
		assertGateUsage(t, g, 3, 11, 2, 9) // Old request still unresolved for this day.
		observeGate(t, g, oldQuarter, gateHeaders("99,60", "99,12", "99,50", "99,10"))
		assertGateUsage(t, g, 3, 12, 2, 10)
		assertGateLimits(t, g, 50, 60, 40, 50) // Ignore old short-term limits as well.

		priorDay := acquireGateReceipt(t, g, RequestRead)
		midnight := boundary.Truncate(24 * time.Hour).Add(24 * time.Hour)
		time.Sleep(time.Until(midnight))
		current = acquireGateReceipt(t, g, RequestRead)
		observeGate(t, g, current, gateHeaders("20,30", "4,5", "15,25", "2,3"))
		assertGateUsage(t, g, 4, 5, 2, 3)
		observeGate(t, g, priorDay, gateHeaders("1,1", "1000,1000", "1,1", "1000,1000"))
		assertGateUsage(t, g, 4, 5, 2, 3)
		assertGateLimits(t, g, 20, 30, 15, 25)
		if err := g.Observe(priorDay, nil); err == nil {
			t.Fatal("stale completion was not finalized")
		}

		// Observe itself must normalize idle windows, not only Acquire.
		idle := acquireGateReceipt(t, g, RequestRead)
		time.Sleep(48 * time.Hour)
		observeGate(t, g, idle, gateHeaders("1,1", "1000,1000", "1,1", "1000,1000"))
		assertGateUsage(t, g, 0, 0, 0, 0)
		assertGateLimits(t, g, 20, 30, 15, 25)
		acquireGateReceipt(t, g, RequestRead)
		assertGateUsage(t, g, 1, 1, 1, 1)
	})
}
