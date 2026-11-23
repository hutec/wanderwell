package strava

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// window tracks the usage of one Strava quota window (quarter-hour or daily).
type window struct {
	limit, usage int
}

type quota struct {
	quarterHour, daily window
}

// RateLimit tracks Strava's application-wide quotas. Reads count against both
// the overall and the read quota; writes only against the overall quota.
// See https://developers.strava.com/docs/rate-limits/
type RateLimit struct {
	mu               sync.Mutex
	overall, read    quota
	quarterHourReset time.Time
	dailyReset       time.Time
	cooldownUntil    time.Time // Set after a 429; applies to all requests.
}

// rateLimitTicket records the windows a request was counted in, so that a
// response arriving after a reset does not update the new window.
type rateLimitTicket struct {
	quarterHourReset, dailyReset time.Time
}

func NewRateLimit() *RateLimit {
	return &RateLimit{
		overall: quota{quarterHour: window{limit: 200}, daily: window{limit: 2000}},
		read:    quota{quarterHour: window{limit: 100}, daily: window{limit: 1000}},
	}
}

// Quarter-hour windows reset at :00, :15, :30, :45; daily windows at midnight UTC.
func nextQuarterHour(now time.Time) time.Time {
	return now.UTC().Truncate(15 * time.Minute).Add(15 * time.Minute)
}

func nextMidnight(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// rollover clears usage of expired windows. Caller must hold mu.
func (rl *RateLimit) rollover(now time.Time) {
	if !now.Before(rl.quarterHourReset) {
		rl.overall.quarterHour.usage, rl.read.quarterHour.usage = 0, 0
		rl.quarterHourReset = nextQuarterHour(now)
	}
	if !now.Before(rl.dailyReset) {
		rl.overall.daily.usage, rl.read.daily.usage = 0, 0
		rl.dailyReset = nextMidnight(now)
	}
}

// blockedUntil returns when the request may be retried, or zero if it can be
// sent now. Caller must hold mu.
func (rl *RateLimit) blockedUntil(now time.Time, isRead bool) time.Time {
	var until time.Time
	if rl.cooldownUntil.After(now) {
		until = rl.cooldownUntil
	}
	quotas := []*quota{&rl.overall}
	if isRead {
		quotas = append(quotas, &rl.read)
	}
	for _, q := range quotas {
		if q.quarterHour.usage >= q.quarterHour.limit && rl.quarterHourReset.After(until) {
			until = rl.quarterHourReset
		}
		if q.daily.usage >= q.daily.limit && rl.dailyReset.After(until) {
			until = rl.dailyReset
		}
	}
	return until
}

// Acquire blocks until a request fits in the quotas, then counts it.
func (rl *RateLimit) Acquire(isRead bool) rateLimitTicket {
	for {
		rl.mu.Lock()
		now := time.Now()
		rl.rollover(now)
		until := rl.blockedUntil(now, isRead)
		if until.IsZero() {
			rl.overall.quarterHour.usage++
			rl.overall.daily.usage++
			if isRead {
				rl.read.quarterHour.usage++
				rl.read.daily.usage++
			}
			ticket := rateLimitTicket{rl.quarterHourReset, rl.dailyReset}
			rl.mu.Unlock()
			return ticket
		}
		rl.mu.Unlock()

		slog.Info("Strava rate limit reached, waiting", "until", until, "isRead", isRead)
		// Re-check after waking: other waiters may have used the new window.
		time.Sleep(time.Until(until))
	}
}

// Update reconciles the local state with Strava's rate limit headers and
// starts a cooldown until the next quarter-hour on HTTP 429.
func (rl *RateLimit) Update(ticket rateLimitTicket, resp *http.Response) {
	if resp == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	rl.rollover(now)

	if resp.StatusCode == http.StatusTooManyRequests {
		if until := nextQuarterHour(now); until.After(rl.cooldownUntil) {
			rl.cooldownUntil = until
		}
	}
	sameQuarterHour := ticket.quarterHourReset.Equal(rl.quarterHourReset)
	sameDay := ticket.dailyReset.Equal(rl.dailyReset)
	for _, h := range []struct {
		prefix string
		q      *quota
	}{{"X-RateLimit", &rl.overall}, {"X-ReadRateLimit", &rl.read}} {
		if limits, ok := parsePair(resp.Header.Get(h.prefix + "-Limit")); ok && limits[0] > 0 && limits[1] > 0 {
			h.q.quarterHour.limit, h.q.daily.limit = limits[0], limits[1]
		}
		// Only trust usage reported for the window the request was counted in.
		if usage, ok := parsePair(resp.Header.Get(h.prefix + "-Usage")); ok {
			if sameQuarterHour {
				h.q.quarterHour.usage = max(h.q.quarterHour.usage, usage[0])
			}
			if sameDay {
				h.q.daily.usage = max(h.q.daily.usage, usage[1])
			}
		}
	}
}

// parsePair parses a "quarterHour,daily" header value.
func parsePair(value string) ([2]int, bool) {
	var pair [2]int
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return pair, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return pair, false
		}
		pair[i] = n
	}
	return pair, true
}
