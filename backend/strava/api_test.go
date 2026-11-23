package strava

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	swagger "wanderwell/backend/client"
	"wanderwell/backend/db"

	"github.com/jackc/pgx/v5/pgtype"
)

type tokenQueriesStub struct {
	tokenQueries
	getAthleteTokens func(context.Context, int64) (db.GetAthleteTokensRow, error)
}

func (q *tokenQueriesStub) GetAthleteTokens(ctx context.Context, id int64) (db.GetAthleteTokensRow, error) {
	return q.getAthleteTokens(ctx, id)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGetDetailedActivityRecoversFromRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const athleteID int64 = 42
		// Strava resets the short-term budget at quarter-hour boundaries.
		start := time.Now()
		resetTime := start.Truncate(15 * time.Minute).Add(15 * time.Minute)
		var requests atomic.Int32
		cfg := swagger.NewConfiguration()
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempt := requests.Add(1)
			if req.Method != http.MethodGet || req.URL.Path != "/api/v3/activities/123" {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q, want Bearer test-token", got)
			}
			headers := make(http.Header)
			headers.Set("Content-Type", "application/json")
			headers.Set("X-ReadRateLimit-Limit", "300,3000")
			if attempt == 1 {
				headers.Set("X-ReadRateLimit-Usage", "300,300")
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     headers,
					Body:       io.NopCloser(strings.NewReader(`{"message":"Rate Limit Exceeded"}`)),
				}, nil
			}
			if now := time.Now(); now.Before(resetTime.Add(time.Second)) {
				t.Errorf("retried at %v, before reset plus the one-second buffer", now)
			}
			headers.Set("X-ReadRateLimit-Usage", "1,301")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"id":123,"name":"Morning ride"}`)),
			}, nil
		})}
		api := &StravaAPI{
			queries: &tokenQueriesStub{
				getAthleteTokens: func(_ context.Context, id int64) (db.GetAthleteTokensRow, error) {
					if id != athleteID {
						t.Errorf("GetAthleteTokens(%d), want %d", id, athleteID)
					}
					return db.GetAthleteTokensRow{
						AccessToken: pgtype.Text{String: "test-token", Valid: true},
						ExpiresAt:   pgtype.Int8{Int64: start.Add(time.Hour).Unix(), Valid: true},
					}, nil
				},
			},
			apiClient: swagger.NewAPIClient(cfg),
			RateLimit: NewRateLimit(),
		}

		var activity *swagger.DetailedActivity
		var err error
		done := make(chan struct{})
		go func() {
			activity, err = api.GetDetailedActivityByID(123, athleteID)
			close(done)
		}()

		assertWaiting := func() {
			t.Helper()
			synctest.Wait()
			if got := requests.Load(); got != 1 {
				t.Fatalf("sent %d requests before reset plus the one-second buffer, want 1", got)
			}
			select {
			case <-done:
				t.Fatal("activity fetch returned instead of waiting to retry")
			default:
			}
		}
		assertWaiting()
		time.Sleep(resetTime.Sub(start))
		assertWaiting()

		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("activity fetch did not retry after rate limit reset")
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := requests.Load(); got != 2 {
			t.Fatalf("sent %d requests, want 2", got)
		}
		if activity == nil || activity.Id != 123 || activity.Name != "Morning ride" {
			t.Errorf("unexpected activity: %+v", activity)
		}
	})
}

func TestGetDetailedActivityWaitsForRateLimitReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const athleteID int64 = 42
		limiter := NewRateLimit()
		limiter.minuteReadRateLimitUsage = limiter.minuteReadRateLimit
		limiter.minuteResetTime = time.Now().Add(10 * time.Minute)

		var requests atomic.Int32
		cfg := swagger.NewConfiguration()
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			if req.Method != http.MethodGet || req.URL.Path != "/api/v3/activities/123" {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q, want Bearer test-token", got)
			}
			headers := make(http.Header)
			headers.Set("Content-Type", "application/json")
			headers.Set("X-ReadRateLimit-Usage", "1,1")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"id":123,"name":"Morning ride"}`)),
			}, nil
		})}
		api := &StravaAPI{
			queries: &tokenQueriesStub{
				getAthleteTokens: func(_ context.Context, id int64) (db.GetAthleteTokensRow, error) {
					if id != athleteID {
						t.Errorf("GetAthleteTokens(%d), want %d", id, athleteID)
					}
					return db.GetAthleteTokensRow{
						AccessToken: pgtype.Text{String: "test-token", Valid: true},
						ExpiresAt:   pgtype.Int8{Int64: time.Now().Add(time.Hour).Unix(), Valid: true},
					}, nil
				},
			},
			apiClient: swagger.NewAPIClient(cfg),
			RateLimit: limiter,
		}

		var activity *swagger.DetailedActivity
		var err error
		done := make(chan struct{})
		go func() {
			activity, err = api.GetDetailedActivityByID(123, athleteID)
			close(done)
		}()

		assertWaiting := func() {
			t.Helper()
			synctest.Wait()
			if got := requests.Load(); got != 0 {
				t.Fatalf("sent %d requests before reset plus the one-second buffer", got)
			}
			select {
			case <-done:
				t.Fatal("activity fetch returned before rate limit reset")
			default:
			}
		}
		assertWaiting()
		time.Sleep(10 * time.Minute)
		assertWaiting()

		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("activity fetch did not resume after rate limit reset")
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := requests.Load(); got != 1 {
			t.Fatalf("sent %d requests, want 1", got)
		}
		if activity == nil || activity.Id != 123 || activity.Name != "Morning ride" {
			t.Errorf("unexpected activity: %+v", activity)
		}
	})
}
