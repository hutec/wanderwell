package strava

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type RateLimitType int

const (
	RateLimitNone RateLimitType = iota
	RateLimitDaily
	RateLimitMinute
)

type RateLimit struct {
	mu                       sync.Mutex
	dailyReadRateLimit       int
	dailyReadRateLimitUsage  int
	minuteReadRateLimit      int
	minuteReadRateLimitUsage int
	minuteResetTime          time.Time
	dailyResetTime           time.Time
}

func NewRateLimit() *RateLimit {
	return &RateLimit{
		dailyReadRateLimit:       3000,
		dailyReadRateLimitUsage:  0,
		minuteReadRateLimit:      300,
		minuteReadRateLimitUsage: 0,
	}
}

func (rl *RateLimit) UpdateRateLimit(resp *http.Response) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	readRateLimit := resp.Header.Get("X-ReadRateLimit-Limit")
	readRateLimitUsage := resp.Header.Get("X-ReadRateLimit-Usage")

	// fmt.Sscanf(readRateLimit, "%d,%d", &rl.minuteReadRateLimit, &rl.dailyReadRateLimit)
	fmt.Sscanf(readRateLimitUsage, "%d,%d", &rl.minuteReadRateLimitUsage, &rl.dailyReadRateLimitUsage)

	now := time.Now()
	minute := now.Minute()
	nextResetMinute := (minute/15 + 1) * 15

	if nextResetMinute >= 60 {
		rl.minuteResetTime = now.Add(time.Duration(60-minute) * time.Minute)
	} else {
		rl.minuteResetTime = now.Truncate(time.Minute).Add(time.Duration(nextResetMinute-minute) * time.Minute)
	}

	// Calculate tomorrow at 00:00 UTC for daily limit
	tomorrow := now.AddDate(0, 0, 1)
	rl.dailyResetTime = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, time.UTC)

	slog.Info("Updated Rate Limit Info",
		"X-ReadRateLimit-Limit", readRateLimit,
		"X-ReadRateLimit-Usage", readRateLimitUsage,
		"minuteResetTime", rl.minuteResetTime,
		"dailyResetTime", rl.dailyResetTime,
	)
}

func (rl *RateLimit) IsRateLimitExceeded() RateLimitType {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// if uninitialized, do not block
	if rl.dailyReadRateLimit == 0 && rl.minuteReadRateLimit == 0 {
		return RateLimitNone
	}

	if rl.dailyReadRateLimitUsage >= rl.dailyReadRateLimit {
		return RateLimitDaily
	}

	if rl.minuteReadRateLimitUsage >= rl.minuteReadRateLimit {
		return RateLimitMinute
	}

	return RateLimitNone
}

func (rl *RateLimit) WaitForRateLimitReset(limitType RateLimitType) {
	rl.mu.Lock()
	var resetTime time.Time
	switch limitType {
	case RateLimitDaily:
		resetTime = rl.dailyResetTime
	case RateLimitMinute:
		resetTime = rl.minuteResetTime
	default:
		rl.mu.Unlock()
		return
	}
	rl.mu.Unlock()

	// TODO: Check if we need time.Now().UTC() instead
	now := time.Now()
	if resetTime.After(now) {
		waitDuration := resetTime.Sub(now) + time.Second
		limitTypeStr := "minute"
		if limitType == RateLimitDaily {
			limitTypeStr = "daily"
		}
		slog.Info("Rate limit exceeded, waiting for reset",
			"limitType", limitTypeStr,
			"resetTime", resetTime,
			"waitDuration", waitDuration,
		)
		time.Sleep(waitDuration)
		slog.Info("Rate limit reset, resuming activity processing", "limitType", limitTypeStr)
	}
}
