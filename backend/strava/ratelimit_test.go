package strava

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Inside a synctest bubble the clock starts at 2000-01-01 00:00:00 UTC.

func response(status int, headers map[string]string) *http.Response {
	resp := &http.Response{StatusCode: status, Header: make(http.Header)}
	for k, v := range headers {
		resp.Header.Set(k, v)
	}
	return resp
}

// acquireAt returns how long Acquire blocked.
func acquireAt(rl *RateLimit, isRead bool) time.Duration {
	start := time.Now()
	rl.Acquire(isRead)
	return time.Since(start)
}

func TestRateLimitQuarterHourExhaustionOnlyBlocksReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit()
		rl.read.quarterHour.limit = 2

		for range 2 {
			if waited := acquireAt(rl, true); waited != 0 {
				t.Fatalf("read within quota waited %v", waited)
			}
		}
		if waited := acquireAt(rl, false); waited != 0 {
			t.Fatalf("write waited %v, but only the read quota is exhausted", waited)
		}
		if waited := acquireAt(rl, true); waited != 15*time.Minute {
			t.Fatalf("read waited %v, want until the next quarter-hour", waited)
		}
	})
}

func TestRateLimitDailyExhaustionWaitsUntilMidnightUTC(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit()
		ticket := rl.Acquire(true)
		rl.Update(ticket, response(http.StatusOK, map[string]string{"X-ReadRateLimit-Usage": "1,1000"}))

		if waited := acquireAt(rl, true); waited != 24*time.Hour {
			t.Fatalf("read waited %v, want until midnight UTC", waited)
		}
	})
}

func TestRateLimitBacklogSpansWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit() // 100 reads per quarter-hour.
		start := time.Now()
		var mu sync.Mutex
		admitted := map[time.Duration]int{}
		var wg sync.WaitGroup
		for range 250 {
			wg.Go(func() {
				rl.Acquire(true)
				mu.Lock()
				admitted[time.Since(start)]++
				mu.Unlock()
			})
		}
		wg.Wait()

		want := map[time.Duration]int{0: 100, 15 * time.Minute: 100, 30 * time.Minute: 50}
		if len(admitted) != len(want) {
			t.Fatalf("admitted = %v, want %v", admitted, want)
		}
		for at, n := range want {
			if admitted[at] != n {
				t.Fatalf("admitted = %v, want %v", admitted, want)
			}
		}
	})
}

func TestRateLimitIgnoresUsageFromPreviousWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit()
		time.Sleep(14 * time.Minute)
		ticket := rl.Acquire(true)
		time.Sleep(2 * time.Minute) // Response arrives after the quarter-hour reset.
		rl.Update(ticket, response(http.StatusOK, map[string]string{"X-ReadRateLimit-Usage": "100,500"}))

		if got := rl.read.quarterHour.usage; got != 0 {
			t.Fatalf("quarter-hour usage = %d, want 0 (stale header ignored)", got)
		}
		if got := rl.read.daily.usage; got != 500 {
			t.Fatalf("daily usage = %d, want 500 (same day)", got)
		}
	})
}

func TestRateLimitHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit()
		ticket := rl.Acquire(true)
		rl.Update(ticket, response(http.StatusOK, map[string]string{
			"X-RateLimit-Limit":     "600,6000",
			"X-RateLimit-Usage":     "10,20",
			"X-ReadRateLimit-Limit": "300,3000",
			"X-ReadRateLimit-Usage": "malformed",
		}))

		want := [2]quota{
			{quarterHour: window{600, 10}, daily: window{6000, 20}},
			{quarterHour: window{300, 1}, daily: window{3000, 1}}, // Local count kept.
		}
		if got := [2]quota{rl.overall, rl.read}; got != want {
			t.Fatalf("quotas = %+v, want %+v", got, want)
		}
	})
}

func TestRateLimit429StartsCooldownForAllRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit()
		time.Sleep(5 * time.Minute)
		ticket := rl.Acquire(true)
		rl.Update(ticket, response(http.StatusTooManyRequests, nil))

		if waited := acquireAt(rl, false); waited != 10*time.Minute {
			t.Fatalf("write waited %v, want until the next quarter-hour", waited)
		}
	})
}

func TestDoWithRateLimitRetriesOn429(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := NewRateLimit()
		start := time.Now()
		calls := 0
		err := doWithRateLimit(rl, true, func() (*http.Response, error) {
			calls++
			if calls < 3 {
				return response(http.StatusTooManyRequests, nil), errors.New("429")
			}
			return response(http.StatusOK, nil), nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("err = %v, calls = %d; want success after 3 calls", err, calls)
		}
		if waited := time.Since(start); waited != 30*time.Minute {
			t.Fatalf("waited %v, want two quarter-hour cooldowns", waited)
		}
	})
}

func TestDoWithRateLimitGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		err := doWithRateLimit(NewRateLimit(), false, func() (*http.Response, error) {
			calls++
			return response(http.StatusTooManyRequests, nil), errors.New("429")
		})
		if err == nil || calls != maxRequestAttempts {
			t.Fatalf("err = %v, calls = %d; want error after %d calls", err, calls, maxRequestAttempts)
		}
	})
}

func TestDoWithRateLimitRejectsNon200(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		err := doWithRateLimit(NewRateLimit(), true, func() (*http.Response, error) {
			return response(http.StatusCreated, nil), nil
		})
		if err == nil {
			t.Fatal("want error for non-200 status")
		}
	})
}
