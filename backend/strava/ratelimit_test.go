package strava

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitForRateLimitReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewRateLimit()
		limiter.minuteReadRateLimitUsage = limiter.minuteReadRateLimit
		limiter.minuteResetTime = time.Now().Add(10 * time.Minute)

		limitType := limiter.IsRateLimitExceeded()
		if limitType != RateLimitMinute {
			t.Fatalf("IsRateLimitExceeded() = %v, want %v", limitType, RateLimitMinute)
		}

		done := make(chan struct{})
		go func() {
			limiter.WaitForRateLimitReset(limitType)
			close(done)
		}()

		assertWaiting := func() {
			t.Helper()
			synctest.Wait()
			select {
			case <-done:
				t.Fatal("WaitForRateLimitReset returned before reset plus the one-second buffer")
			default:
			}
		}

		// Ensure the waiting goroutine is sleeping before advancing virtual time.
		assertWaiting()
		time.Sleep(10*time.Minute - time.Second)
		assertWaiting()

		// Reaching the reset time must still leave the one-second buffer.
		time.Sleep(time.Second)
		assertWaiting()

		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("WaitForRateLimitReset did not resume after reset plus the one-second buffer")
		}
	})
}
