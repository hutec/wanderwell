package strava

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RequestKind selects which Strava quotas a request consumes.
type RequestKind int

const (
	// RequestRead consumes both overall and read-specific quotas.
	RequestRead RequestKind = iota + 1
	// RequestWrite consumes only overall quotas.
	RequestWrite
)

// RateLimitGateQuota configures the limits and initial usage for one quota.
// Limits must be positive (zero does not mean unlimited or disabled), and each
// usage must be between zero and its corresponding limit, inclusive. The
// quarter-hour and daily values, and overall and read quotas, are validated
// independently; no ordering between their limits or usages is required.
type RateLimitGateQuota struct {
	QuarterHourLimit int
	DailyLimit       int
	QuarterHourUsage int
	DailyUsage       int
}

// RateLimitGateConfig supplies both required quotas. Initial usage belongs to
// the UTC quarter-hour and day containing the constructor's current time,
// including when constructed exactly at a window boundary.
type RateLimitGateConfig struct {
	Overall RateLimitGateQuota
	Read    RateLimitGateQuota
}

// RateLimitGate atomically reserves permission for Strava requests across
// shared overall and read-specific quotas. It must be constructed with
// NewRateLimitGate and must not be copied. It is safe for concurrent callers;
// admission order is not guaranteed. Existing API calls do not use it automatically.
type RateLimitGate struct {
	mu               sync.Mutex
	overall          RateLimitGateQuota
	read             RateLimitGateQuota
	quarterHourReset time.Time
	dailyReset       time.Time
}

// NewRateLimitGate validates config and creates a gate without starting any
// goroutines. The gate uses only configured quotas, not response headers.
func NewRateLimitGate(config RateLimitGateConfig) (*RateLimitGate, error) {
	for _, quota := range []struct {
		name string
		RateLimitGateQuota
	}{{"overall", config.Overall}, {"read", config.Read}} {
		if quota.QuarterHourLimit <= 0 || quota.DailyLimit <= 0 {
			return nil, fmt.Errorf("strava %s rate limits must be positive", quota.name)
		}
		if quota.QuarterHourUsage < 0 || quota.QuarterHourUsage > quota.QuarterHourLimit ||
			quota.DailyUsage < 0 || quota.DailyUsage > quota.DailyLimit {
			return nil, fmt.Errorf("strava %s usage must be between zero and its limit", quota.name)
		}
	}
	g := &RateLimitGate{}
	g.resetExpired(time.Now().UTC())
	g.overall, g.read = config.Overall, config.Read
	return g, nil
}

// Acquire waits for all applicable budgets, then reserves one request in each
// before returning nil. It never partially reserves capacity while waiting.
// Counts represent permissions granted: they are not refunded, even if the
// request is never sent. Unknown request kinds return an error without charging.
// Cancellation observed before reservation returns ctx.Err() without charging;
// cancellation racing a successful reservation may still return nil.
//
// Quarter-hour windows reset at :00, :15, :30, and :45 UTC; daily windows reset
// at 00:00 UTC. At the exact boundary the new window is available, with no buffer.
func (g *RateLimitGate) Acquire(ctx context.Context, kind RequestKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if kind != RequestRead && kind != RequestWrite {
		return fmt.Errorf("unknown strava request kind: %d", kind)
	}
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return err
		}
		g.resetExpired(time.Now().UTC())

		var reset time.Time
		if g.overall.QuarterHourUsage >= g.overall.QuarterHourLimit ||
			(kind == RequestRead && g.read.QuarterHourUsage >= g.read.QuarterHourLimit) {
			reset = g.quarterHourReset
		}
		if g.overall.DailyUsage >= g.overall.DailyLimit ||
			(kind == RequestRead && g.read.DailyUsage >= g.read.DailyLimit) {
			// Midnight is at or after the next quarter-hour boundary. Wait for
			// every exhausted budget, not just the first one to reset.
			reset = g.dailyReset
		}
		if reset.IsZero() {
			g.overall.QuarterHourUsage++
			g.overall.DailyUsage++
			if kind == RequestRead {
				g.read.QuarterHourUsage++
				g.read.DailyUsage++
			}
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()

		// Each caller waits on its own timer, without holding mu or spawning
		// a goroutine. Recheck under mu after waking: other callers may win.
		timer := time.NewTimer(time.Until(reset))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// resetExpired normalizes directly to the windows containing now (in UTC),
// even after many idle windows. The caller must hold mu or own a new gate.
func (g *RateLimitGate) resetExpired(now time.Time) {
	if !now.Before(g.quarterHourReset) {
		g.overall.QuarterHourUsage = 0
		g.read.QuarterHourUsage = 0
		g.quarterHourReset = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	}
	if !now.Before(g.dailyReset) {
		g.overall.DailyUsage = 0
		g.read.DailyUsage = 0
		g.dailyReset = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	}
}
