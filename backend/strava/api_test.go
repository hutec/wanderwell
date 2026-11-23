package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	swagger "wanderwell/backend/client"
	"wanderwell/backend/config"
	"wanderwell/backend/db"

	"github.com/jackc/pgx/v5/pgtype"
)

type tokenQueriesStub struct {
	getAthleteTokens    func(context.Context, int64) (db.GetAthleteTokensRow, error)
	updateAthleteTokens func(context.Context, db.UpdateAthleteTokensParams) error
}

func (q *tokenQueriesStub) GetAthleteTokens(ctx context.Context, id int64) (db.GetAthleteTokensRow, error) {
	return q.getAthleteTokens(ctx, id)
}

func (q *tokenQueriesStub) UpdateAthleteTokens(ctx context.Context, arg db.UpdateAthleteTokensParams) error {
	return q.updateAthleteTokens(ctx, arg)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestStravaAPIRequestWiring(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, err := NewStravaAPI(nil, &config.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(api.Shutdown)
		assertGateLimits(t, api.gate, 200, 2000, 100, 1000)

		const athleteID int64 = 42
		const description = "🧭 New ground: 1.25 km\nA & B"
		type contextKey struct{}
		ctx := context.WithValue(context.Background(), contextKey{}, "preserved")
		start := time.Now()
		var lookups, requests atomic.Int32
		api.queries = &tokenQueriesStub{getAthleteTokens: func(ctx context.Context, id int64) (db.GetAthleteTokensRow, error) {
			lookups.Add(1)
			if id != athleteID || ctx.Value(contextKey{}) != "preserved" {
				t.Errorf("token lookup lost athlete/context: %d, %v", id, ctx)
			}
			return db.GetAthleteTokensRow{
				AccessToken: pgtype.Text{String: fmt.Sprintf("token-%d", time.Now().Unix()), Valid: true},
				ExpiresAt:   pgtype.Int8{Int64: time.Now().Add(time.Hour).Unix(), Valid: true},
			}, nil
		}}
		steps := []struct {
			method, path, query string
			status              int
			body                string
			headers             http.Header
		}{
			{http.MethodGet, "/api/v3/athlete/activities", "page=1&per_page=200", 200, `[{"id":123}]`, gateHeaders("200,2000", "1,1", "1,1000", "1,1")},
			{http.MethodPut, "/api/v3/activities/123", "", 429, `{"message":"Rate Limit Exceeded"}`, gateHeaders("", "2,2", "", "")},
			{http.MethodPut, "/api/v3/activities/123", "", 200, `{"id":123}`, gateHeaders("", "3,3", "", "")},
			{http.MethodGet, "/api/v3/athlete/activities", "page=1&per_page=200", 200, `[{"id":456}]`, gateHeaders("200,2000", "1,4", "3,1000", "1,2")},
			{http.MethodGet, "/api/v3/athlete/activities", "page=2&per_page=200", 200, `[]`, gateHeaders("", "2,5", "", "2,3")},
			{http.MethodGet, "/api/v3/activities/456", "", 200, `{"id":456,"name":"Morning ride"}`, gateHeaders("", "3,6", "", "3,4")},
		}
		steps[1].headers.Set("Retry-After", "1")
		clientConfig := swagger.NewConfiguration()
		clientConfig.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			n := int(requests.Add(1)) - 1
			if n >= len(steps) {
				return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL)
			}
			step := steps[n]
			if req.Method != step.method || req.URL.Path != step.path || req.URL.RawQuery != step.query {
				t.Errorf("request %d = %s %s, want %s %s?%s", n, req.Method, req.URL, step.method, step.path, step.query)
			}
			if req.Context().Value(contextKey{}) != "preserved" {
				t.Error("generated HTTP request lost context values")
			}
			if got, want := req.Header.Get("Authorization"), fmt.Sprintf("Bearer token-%d", time.Now().Unix()); got != want {
				t.Errorf("request %d authorization = %q, want fresh %q", n, got, want)
			}
			if req.Method == http.MethodPut {
				var body swagger.UpdatableActivity
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Description != description {
					t.Errorf("description body = %+v, error %v", body, err)
				}
			}
			headers := step.headers.Clone()
			headers.Set("Content-Type", "application/json")
			return &http.Response{StatusCode: step.status, Status: http.StatusText(step.status), Header: headers, Body: io.NopCloser(strings.NewReader(step.body))}, nil
		})}
		api.apiClient = swagger.NewAPIClient(clientConfig)

		// A finite import must not fetch page 2, even though page 1 is nonempty.
		activities, err := api.GetAthleteSummaryActivities(ctx, athleteID, 1)
		if err != nil || len(activities) != 1 || activities[0].Id != 123 {
			t.Fatalf("limited import = %+v, %v", activities, err)
		}
		assertGateLimits(t, api.gate, 200, 2000, 1, 1000)
		importDone := make(chan error, 1)
		go func() {
			activities, err := api.GetAthleteSummaryActivities(ctx, athleteID, 0)
			if err == nil && (len(activities) != 1 || activities[0].Id != 456) {
				err = fmt.Errorf("unlimited import = %+v", activities)
			}
			importDone <- err
		}()
		synctest.Wait()
		if requests.Load() != 1 || lookups.Load() != 1 {
			t.Fatal("read bypassed exhausted quota or fetched auth before admission")
		}

		// A write can use another worker despite the exhausted read quota. Its
		// 429 retry must reacquire permission and look up a fresh token.
		if err := api.UpdateActivityDescription(ctx, 123, athleteID, description); err != nil {
			t.Fatal(err)
		}
		if requests.Load() != 3 || lookups.Load() != 3 || !time.Now().Equal(start.Add(time.Second)) {
			t.Fatal("write did not retry once after cooldown independently of the read budget")
		}
		reset := start.UTC().Truncate(15 * time.Minute).Add(15 * time.Minute)
		time.Sleep(time.Until(reset)) // Virtual time only; the import resumes at reset.
		if err := <-importDone; err != nil {
			t.Fatal(err)
		}
		activity, err := api.GetDetailedActivityByID(ctx, 456, athleteID)
		if err != nil || activity == nil || activity.Id != 456 || activity.Name != "Morning ride" {
			t.Fatalf("detail = %+v, %v", activity, err)
		}
		assertGateUsage(t, api.gate, 3, 6, 3, 4)

		// Both read wrappers must wait without looking up tokens, and caller
		// cancellation and API shutdown must interrupt those actual API calls.
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		pageDone, detailDone := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := api.GetAthleteSummaryActivitiesByPage(cancelCtx, athleteID, 7)
			pageDone <- err
		}()
		go func() {
			_, err := api.GetDetailedActivityByID(ctx, 789, athleteID)
			detailDone <- err
		}()
		synctest.Wait()
		if requests.Load() != int32(len(steps)) || lookups.Load() != int32(len(steps)) {
			t.Fatal("read bypassed quota or fetched auth while waiting")
		}
		cancel()
		if err := <-pageDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled page: %v", err)
		}
		api.Shutdown()
		if err := <-detailDone; !errors.Is(err, ErrSchedulerClosed) && !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown detail: %v", err)
		}
		for _, call := range []func() error{
			func() error { _, err := api.GetAthleteSummaryActivities(ctx, athleteID, 0); return err },
			func() error { _, err := api.GetDetailedActivityByID(ctx, 456, athleteID); return err },
			func() error { return api.UpdateActivityDescription(ctx, 456, athleteID, description) },
		} {
			if err := call(); !errors.Is(err, ErrSchedulerClosed) {
				t.Fatalf("API call bypassed shut-down scheduler: %v", err)
			}
		}
		if requests.Load() != int32(len(steps)) || lookups.Load() != int32(len(steps)) || !time.Now().Equal(reset) {
			t.Fatal("cancellation/shutdown performed more work or waited for quota reset")
		}
	})
}

func TestStravaAPIRefreshSerializationAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstRefresh, release := make(chan struct{}), make(chan struct{})
		secondRefresh := make(chan struct{})
		var refreshes, activityRequests atomic.Int32
		var cancelRefresh atomic.Bool
		oldTransport := http.DefaultTransport
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"id":123}`
			if req.URL.Path == "/api/v3/oauth/token" {
				n := refreshes.Add(1)
				wantToken := "rotating-token"
				if n == 1 {
					wantToken = "original-token"
				}
				if err := req.ParseForm(); err != nil || req.Form.Get("refresh_token") != wantToken {
					t.Errorf("refresh form = %v, %v; want token %q", req.Form, err, wantToken)
				}
				if n == 1 {
					close(firstRefresh)
					select {
					case <-release:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
					body = fmt.Sprintf(`{"access_token":"fresh","refresh_token":"rotating-token","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				} else {
					if !cancelRefresh.Load() {
						return nil, errors.New("duplicate token refresh")
					}
					close(secondRefresh)
					<-req.Context().Done()
					return nil, req.Context().Err()
				}
			} else {
				activityRequests.Add(1)
				if req.Header.Get("Authorization") != "Bearer fresh" {
					t.Errorf("activity authorization = %q", req.Header.Get("Authorization"))
				}
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		t.Cleanup(func() { http.DefaultTransport = oldTransport })
		api, err := NewStravaAPI(nil, &config.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(api.Shutdown)
		row := db.GetAthleteTokensRow{
			RefreshToken: pgtype.Text{String: "original-token", Valid: true},
			ExpiresAt:    pgtype.Int8{Int64: time.Now().Unix(), Valid: true}, // Expired at this exact boundary.
		}
		lookups, updates := 0, 0 // Access serialized by the production token mutex.
		api.queries = &tokenQueriesStub{
			getAthleteTokens: func(ctx context.Context, _ int64) (db.GetAthleteTokensRow, error) {
				lookups++
				return row, ctx.Err()
			},
			updateAthleteTokens: func(ctx context.Context, arg db.UpdateAthleteTokensParams) error {
				updates++
				row.AccessToken, row.RefreshToken, row.ExpiresAt = arg.AccessToken, arg.RefreshToken, arg.ExpiresAt
				return ctx.Err()
			},
		}
		fetch := func() <-chan error {
			done := make(chan error, 1)
			go func() {
				_, err := api.GetDetailedActivityByID(context.Background(), 123, 42)
				done <- err
			}()
			return done
		}
		first := fetch()
		<-firstRefresh
		second := fetch()
		close(release)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		if lookups != 2 || updates != 1 || refreshes.Load() != 1 || activityRequests.Load() != 2 || row.RefreshToken.String != "rotating-token" {
			t.Fatal("concurrent expired-token requests did not share one persisted refresh")
		}

		// Shutdown reaches an OAuth request inside a worker, not just gate waits.
		cancelRefresh.Store(true)
		row.ExpiresAt.Int64 = time.Now().Unix()
		third := fetch()
		<-secondRefresh
		api.Shutdown()
		if err := <-third; !errors.Is(err, ErrSchedulerClosed) && !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown during refresh: %v", err)
		}
		if updates != 1 || activityRequests.Load() != 2 {
			t.Fatal("canceled refresh updated credentials or sent an activity request")
		}
	})
}
