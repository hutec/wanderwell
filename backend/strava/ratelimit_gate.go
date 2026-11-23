package strava

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
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
	mu                sync.Mutex
	overall           gateQuota
	read              gateQuota
	quarterHourReset  time.Time
	dailyReset        time.Time
	quarterHourIssued int
	dailyIssued       int
	changed           chan struct{} // Closed and replaced under mu on observation.
}

type gateQuota struct {
	quarterHour gateBudget
	daily       gateBudget
}

// Each window keeps independent source data, not a previously derived total:
//   - server is the configured baseline / server usage high-water mark;
//   - unresolved counts grants without valid matching usage, even after a nil
//     or malformed completion (an aggregate, not retained per-request records);
//   - floor counts local grants, rebased to at least server before each grant.
//
// Effective usage is max(floor, saturating(server + unresolved)). A matching
// usage observation removes only its own unresolved contribution. This protects
// concurrent grants without permanently adding all completed requests to server
// totals. The floor also preserves grants made after an external high-water mark,
// even if their responses have no usage or arrive behind newer observations.
// Out-of-order responses can temporarily overcount overlap; missing usage can
// overcount until reset. Never rebase floor onto the derived effective total:
// that would permanently retain temporary overlap.
type gateBudget struct {
	limit      int
	floor      int
	server     int
	unresolved int
	limitOrder int
}

func (b *gateBudget) usage() int {
	if b.server > math.MaxInt-b.unresolved {
		return math.MaxInt
	}
	return max(b.floor, b.server+b.unresolved)
}

func (b *gateBudget) reserve() {
	// Admission checked usage < limit <= MaxInt, so both increments are safe.
	b.floor = max(b.floor, b.server) + 1
	b.unresolved++
}

func (b *gateBudget) reset() {
	*b = gateBudget{limit: b.limit}
}

// RateLimitReservation is an opaque permission returned by Acquire. Copies
// share completion state; only the issuing gate can observe it successfully.
type RateLimitReservation struct {
	state *gateReservation
}

type gateReservation struct {
	owner            *RateLimitGate
	kind             RequestKind
	quarterHourReset time.Time
	dailyReset       time.Time
	quarterHourOrder int
	dailyOrder       int
	completed        bool // Protected by owner's mu.
}

// NewRateLimitGate validates explicit initial quotas and creates a gate without
// starting any goroutines. Later observations can update limits and usage.
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
	newQuota := func(q RateLimitGateQuota) gateQuota {
		return gateQuota{
			quarterHour: gateBudget{limit: q.QuarterHourLimit, floor: q.QuarterHourUsage, server: q.QuarterHourUsage},
			daily:       gateBudget{limit: q.DailyLimit, floor: q.DailyUsage, server: q.DailyUsage},
		}
	}
	g := &RateLimitGate{changed: make(chan struct{})}
	g.resetExpired(time.Now().UTC())
	g.overall, g.read = newQuota(config.Overall), newQuota(config.Read)
	return g, nil
}

// Acquire waits for all applicable budgets, then atomically reserves one request
// in each and returns its receipt. It never partially reserves while waiting.
// Permissions are not refunded, even if never sent. Callers must acquire once
// per attempt, send promptly, and call Observe exactly once after completion,
// passing nil headers on failure or if the permission was not used. Do not reuse
// a receipt for retries. The gate retains no per-request records.
//
// Unknown request kinds return an error without charging. Cancellation observed
// before reservation returns ctx.Err() and no receipt; cancellation racing a
// successful reservation may still return a receipt and nil error.
//
// Quarter-hour windows reset at :00, :15, :30, and :45 UTC; daily windows reset
// at 00:00 UTC. At the exact boundary the new window is available, with no buffer.
func (g *RateLimitGate) Acquire(ctx context.Context, kind RequestKind) (*RateLimitReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if kind != RequestRead && kind != RequestWrite {
		return nil, fmt.Errorf("unknown strava request kind: %d", kind)
	}
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return nil, err
		}
		g.resetExpired(time.Now().UTC())

		var reset time.Time
		if g.overall.quarterHour.usage() >= g.overall.quarterHour.limit ||
			(kind == RequestRead && g.read.quarterHour.usage() >= g.read.quarterHour.limit) {
			reset = g.quarterHourReset
		}
		if g.overall.daily.usage() >= g.overall.daily.limit ||
			(kind == RequestRead && g.read.daily.usage() >= g.read.daily.limit) {
			// Midnight is at or after the next quarter-hour boundary. Wait for
			// every exhausted budget, not just the first one to reset.
			reset = g.dailyReset
		}
		if reset.IsZero() {
			g.overall.quarterHour.reserve()
			g.overall.daily.reserve()
			if kind == RequestRead {
				g.read.quarterHour.reserve()
				g.read.daily.reserve()
			}
			// Each counter is bounded by the overall window's local floor.
			g.quarterHourIssued++
			g.dailyIssued++
			receipt := &RateLimitReservation{state: &gateReservation{
				owner: g, kind: kind,
				quarterHourReset: g.quarterHourReset, dailyReset: g.dailyReset,
				quarterHourOrder: g.quarterHourIssued, dailyOrder: g.dailyIssued,
			}}
			g.mu.Unlock()
			return receipt, nil
		}
		changed := g.changed // Capture under mu, so Observe cannot lose a wakeup.
		g.mu.Unlock()

		// Each caller waits on its own timer, without holding mu or spawning
		// a goroutine. Recheck under mu after waking: other callers may win.
		timer := time.NewTimer(time.Until(reset))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Observe completes a reservation and reconciles the four Strava rate-limit
// headers. Nil, zero, foreign, or already completed receipts return an error
// without changing the gate. Missing/malformed pairs leave that known state
// intact; limits and usages, and overall/read pairs, are parsed independently.
// Valid read headers on a write can inform read budgets without settling a read
// grant. Nil/malformed usage never refunds permission. Completion is final, even
// without headers: later calls cannot supply a missing observation.
//
// Headers have no request/window sequence number. Attribute each window to the
// reservation's acquisition time, independently: ignore expired quarter-hour
// data but still accept same-day data, and ignore prior-day data entirely. Limits
// follow the same attribution and persist across resets. Always accept limit
// reductions; accept increases only from reservations at least as new (in local
// admission order) as the newest limit-bearing reservation observed for that
// window. Admission order is only a conservative proxy for server order.
//
// A request sent/processed across a reset may actually consume a different
// server window. This gate cannot infer that, recover missing external usage,
// or guarantee exact server accounting. Callers must send promptly; boundary
// execution and retry policy belong to a future integration, not this gate.
func (g *RateLimitGate) Observe(receipt *RateLimitReservation, headers http.Header) error {
	if receipt == nil || receipt.state == nil || receipt.state.owner != g {
		return fmt.Errorf("invalid or foreign strava rate-limit reservation")
	}
	overall := parseGateObservation(headers, "X-RateLimit")
	read := parseGateObservation(headers, "X-ReadRateLimit")
	g.mu.Lock()
	defer g.mu.Unlock()
	r := receipt.state
	if r.completed {
		return fmt.Errorf("strava rate-limit reservation already completed")
	}
	r.completed = true
	g.resetExpired(time.Now().UTC())
	if r.quarterHourReset.Equal(g.quarterHourReset) {
		g.overall.quarterHour.observe(overall, 0, r.quarterHourOrder, true)
		g.read.quarterHour.observe(read, 0, r.quarterHourOrder, r.kind == RequestRead)
	}
	if r.dailyReset.Equal(g.dailyReset) {
		g.overall.daily.observe(overall, 1, r.dailyOrder, true)
		g.read.daily.observe(read, 1, r.dailyOrder, r.kind == RequestRead)
	}
	close(g.changed)
	g.changed = make(chan struct{})
	return nil
}

type gateObservation struct {
	limits, usage     [2]int
	limitsOK, usageOK bool
}

func parseGateObservation(headers http.Header, prefix string) gateObservation {
	limits, limitsOK := parseGatePair(headers, prefix+"-Limit", 1)
	usage, usageOK := parseGatePair(headers, prefix+"-Usage", 0)
	return gateObservation{limits: limits, limitsOK: limitsOK, usage: usage, usageOK: usageOK}
}

func parseGatePair(headers http.Header, name string, minimum int) ([2]int, bool) {
	var pair [2]int
	values := headers.Values(name)
	if len(values) != 1 {
		return pair, false // Missing or ambiguous repeated header.
	}
	parts := strings.Split(values[0], ",")
	if len(parts) != 2 {
		return pair, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < minimum {
			return [2]int{}, false
		}
		pair[i] = n
	}
	return pair, true
}

func (b *gateBudget) observe(o gateObservation, window, order int, charged bool) {
	if o.limitsOK {
		if o.limits[window] < b.limit || order >= b.limitOrder {
			b.limit = o.limits[window]
		}
		b.limitOrder = max(b.limitOrder, order)
	}
	if o.usageOK {
		b.server = max(b.server, o.usage[window])
		if charged {
			b.unresolved--
		}
	}
}

// resetExpired normalizes directly to the windows containing now (in UTC),
// even after many idle windows. The caller must hold mu or own a new gate.
func (g *RateLimitGate) resetExpired(now time.Time) {
	if !now.Before(g.quarterHourReset) {
		g.overall.quarterHour.reset()
		g.read.quarterHour.reset()
		g.quarterHourIssued = 0
		g.quarterHourReset = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	}
	if !now.Before(g.dailyReset) {
		g.overall.daily.reset()
		g.read.daily.reset()
		g.dailyIssued = 0
		g.dailyReset = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	}
}
