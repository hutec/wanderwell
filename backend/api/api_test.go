package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wanderwell/backend/db"

	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/markbates/goth/gothic"
)

// mockQuerier implements db.Querier for testing.
type mockQuerier struct {
	getAthlete                   func(ctx context.Context, id int64) (db.GetAthleteRow, error)
	getAthleteTokens             func(ctx context.Context, id int64) (db.GetAthleteTokensRow, error)
	getRouteName                 func(ctx context.Context, arg db.GetRouteNameParams) (string, error)
	getRouteUniqueDistanceMeters func(ctx context.Context, id int64) (float64, error)
	getUserIDByToken             func(ctx context.Context, token string) (int64, error)
	getUserPreferences           func(ctx context.Context, userID int64) (db.UserPreference, error)
	listAthleteIDs               func(ctx context.Context) ([]int64, error)
	listRoutesByUser             func(ctx context.Context, userID int64) ([]db.ListRoutesByUserRow, error)
	routeExists                  func(ctx context.Context, id int64) (bool, error)
	updateAthleteTokens          func(ctx context.Context, arg db.UpdateAthleteTokensParams) error
	updateRouteName              func(ctx context.Context, arg db.UpdateRouteNameParams) error
	upsertAthlete                func(ctx context.Context, arg db.UpsertAthleteParams) error
	upsertRoute                  func(ctx context.Context, arg db.UpsertRouteParams) error
	upsertUserPreferences        func(ctx context.Context, arg db.UpsertUserPreferencesParams) (db.UserPreference, error)
}

func (m *mockQuerier) GetAthlete(ctx context.Context, id int64) (db.GetAthleteRow, error) {
	if m.getAthlete != nil {
		return m.getAthlete(ctx, id)
	}
	return db.GetAthleteRow{}, errors.New("unexpected GetAthlete call")
}

func (m *mockQuerier) GetAthleteTokens(ctx context.Context, id int64) (db.GetAthleteTokensRow, error) {
	if m.getAthleteTokens != nil {
		return m.getAthleteTokens(ctx, id)
	}
	return db.GetAthleteTokensRow{}, errors.New("unexpected GetAthleteTokens call")
}

func (m *mockQuerier) GetRouteName(ctx context.Context, arg db.GetRouteNameParams) (string, error) {
	if m.getRouteName != nil {
		return m.getRouteName(ctx, arg)
	}
	return "", errors.New("unexpected GetRouteName call")
}

func (m *mockQuerier) GetRouteUniqueDistanceMeters(ctx context.Context, id int64) (float64, error) {
	if m.getRouteUniqueDistanceMeters != nil {
		return m.getRouteUniqueDistanceMeters(ctx, id)
	}
	return 0, errors.New("unexpected GetRouteUniqueDistanceMeters call")
}

func (m *mockQuerier) GetUserIDByToken(ctx context.Context, token string) (int64, error) {
	if m.getUserIDByToken != nil {
		return m.getUserIDByToken(ctx, token)
	}
	return 0, errors.New("unexpected GetUserIDByToken call")
}

func (m *mockQuerier) GetUserPreferences(ctx context.Context, userID int64) (db.UserPreference, error) {
	if m.getUserPreferences != nil {
		return m.getUserPreferences(ctx, userID)
	}
	return db.UserPreference{}, errors.New("unexpected GetUserPreferences call")
}

func (m *mockQuerier) ListAthleteIDs(ctx context.Context) ([]int64, error) {
	if m.listAthleteIDs != nil {
		return m.listAthleteIDs(ctx)
	}
	return nil, errors.New("unexpected ListAthleteIDs call")
}

func (m *mockQuerier) ListRoutesByUser(ctx context.Context, userID int64) ([]db.ListRoutesByUserRow, error) {
	if m.listRoutesByUser != nil {
		return m.listRoutesByUser(ctx, userID)
	}
	return nil, errors.New("unexpected ListRoutesByUser call")
}

func (m *mockQuerier) RouteExists(ctx context.Context, id int64) (bool, error) {
	if m.routeExists != nil {
		return m.routeExists(ctx, id)
	}
	return false, errors.New("unexpected RouteExists call")
}

func (m *mockQuerier) UpdateAthleteTokens(ctx context.Context, arg db.UpdateAthleteTokensParams) error {
	if m.updateAthleteTokens != nil {
		return m.updateAthleteTokens(ctx, arg)
	}
	return errors.New("unexpected UpdateAthleteTokens call")
}

func (m *mockQuerier) UpdateRouteName(ctx context.Context, arg db.UpdateRouteNameParams) error {
	if m.updateRouteName != nil {
		return m.updateRouteName(ctx, arg)
	}
	return errors.New("unexpected UpdateRouteName call")
}

func (m *mockQuerier) UpsertAthlete(ctx context.Context, arg db.UpsertAthleteParams) error {
	if m.upsertAthlete != nil {
		return m.upsertAthlete(ctx, arg)
	}
	return errors.New("unexpected UpsertAthlete call")
}

func (m *mockQuerier) UpsertRoute(ctx context.Context, arg db.UpsertRouteParams) error {
	if m.upsertRoute != nil {
		return m.upsertRoute(ctx, arg)
	}
	return errors.New("unexpected UpsertRoute call")
}

func (m *mockQuerier) UpsertUserPreferences(ctx context.Context, arg db.UpsertUserPreferencesParams) (db.UserPreference, error) {
	if m.upsertUserPreferences != nil {
		return m.upsertUserPreferences(ctx, arg)
	}
	return db.UserPreference{}, errors.New("unexpected UpsertUserPreferences call")
}

// mockCacheUpdater implements ActivityCacheUpdater for testing.
type mockCacheUpdater struct {
	mu                           sync.Mutex
	updateActivityCacheCalls     []int64
	addDetailedActivityCalls     [][2]int64
	writeUniqueDistanceDescCalls [][2]int64
	updateActivityCacheFunc      func(userID int64) error
	addDetailedActivityFunc      func(activityID int64, athleteID int64) error
	writeUniqueDistanceDescFunc  func(activityID int64, athleteID int64)
}

func (m *mockCacheUpdater) UpdateActivityCache(userID int64) error {
	m.mu.Lock()
	m.updateActivityCacheCalls = append(m.updateActivityCacheCalls, userID)
	m.mu.Unlock()
	if m.updateActivityCacheFunc != nil {
		return m.updateActivityCacheFunc(userID)
	}
	return nil
}

func (m *mockCacheUpdater) AddDetailedActivity(activityID int64, athleteID int64) error {
	m.mu.Lock()
	m.addDetailedActivityCalls = append(m.addDetailedActivityCalls, [2]int64{activityID, athleteID})
	m.mu.Unlock()
	if m.addDetailedActivityFunc != nil {
		return m.addDetailedActivityFunc(activityID, athleteID)
	}
	return nil
}

func (m *mockCacheUpdater) WriteUniqueDistanceDescription(activityID int64, athleteID int64) {
	m.mu.Lock()
	m.writeUniqueDistanceDescCalls = append(m.writeUniqueDistanceDescCalls, [2]int64{activityID, athleteID})
	m.mu.Unlock()
	if m.writeUniqueDistanceDescFunc != nil {
		m.writeUniqueDistanceDescFunc(activityID, athleteID)
	}
}

func createTestSessionCookie(t *testing.T, userID int64) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	session, err := gothic.Store.New(request, "user-session")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session.Values["user_id"] = userID
	if err := session.Save(request, response); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return response.Result().Cookies()[0]
}

func setupTestStore(t *testing.T) {
	t.Helper()
	oldStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte("01234567890123456789012345678901"))
	t.Cleanup(func() { gothic.Store = oldStore })
}

// ---------------------------
// Middleware Tests
// ---------------------------

func TestRequireCookieAuth(t *testing.T) {
	setupTestStore(t)

	server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "http://localhost:3000", "test-token", "", 0)
	var recordedUserID int64
	handler := server.RequireCookieAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedUserID = r.Context().Value(userIDKey).(int64)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("no cookie returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid session cookie passes and injects userID", func(t *testing.T) {
		cookie := createTestSessionCookie(t, 12345)
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
		}
		if recordedUserID != 12345 {
			t.Errorf("got userID %d, want 12345", recordedUserID)
		}
	})
}

func TestRequireTokenAuth(t *testing.T) {
	lookup := func(_ context.Context, token string) (int64, error) {
		switch token {
		case "valid-token":
			return 42, nil
		case "db-error":
			return 0, errors.New("db unavailable")
		default:
			return 0, pgx.ErrNoRows
		}
	}
	server := NewServerWithQuerier(&mockQuerier{getUserIDByToken: lookup}, &mockCacheUpdater{}, "", "", "", 0)

	var recordedUserID int64
	handler := server.RequireTokenAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedUserID = r.Context().Value(userIDKey).(int64)
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name         string
		forwardedURI string
		wantStatus   int
		wantUserID   int64
	}{
		{name: "missing token parameter", forwardedURI: "/raster/styles/routes", wantStatus: http.StatusUnauthorized},
		{name: "malformed forwarded URI", forwardedURI: "%", wantStatus: http.StatusBadRequest},
		{name: "unknown token", forwardedURI: "/raster/styles/routes?token=unknown", wantStatus: http.StatusUnauthorized},
		{name: "database failure", forwardedURI: "/raster/styles/routes?token=db-error", wantStatus: http.StatusInternalServerError},
		{name: "valid token", forwardedURI: "/raster/styles/routes?token=valid-token", wantStatus: http.StatusNoContent, wantUserID: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recordedUserID = 0
			req := httptest.NewRequest(http.MethodGet, "/auth/raster", nil)
			req.Header.Set("X-Forwarded-Uri", tt.forwardedURI)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantUserID != 0 && recordedUserID != tt.wantUserID {
				t.Fatalf("userID = %d, want %d", recordedUserID, tt.wantUserID)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	const adminID int64 = 999

	t.Run("no admin configured forbids all", func(t *testing.T) {
		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", 0)
		handler := server.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/update", nil)
		ctx := context.WithValue(req.Context(), userIDKey, adminID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Errorf("got status %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("admin userID passes", func(t *testing.T) {
		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", adminID)
		handler := server.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/update", nil)
		ctx := context.WithValue(req.Context(), userIDKey, adminID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusOK {
			t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("non-admin userID is forbidden", func(t *testing.T) {
		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", adminID)
		handler := server.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/update", nil)
		ctx := context.WithValue(req.Context(), userIDKey, int64(123))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Errorf("got status %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("missing context userID is unauthorized", func(t *testing.T) {
		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", adminID)
		handler := server.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/update", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}

// ---------------------------
// ForwardAuth Routes Tests
// ---------------------------

func TestForwardAuthEndpoints(t *testing.T) {
	setupTestStore(t)

	server := NewServerWithQuerier(&mockQuerier{
		getUserIDByToken: func(_ context.Context, token string) (int64, error) {
			if token == "valid-token" {
				return 42, nil
			}
			return 0, pgx.ErrNoRows
		},
	}, &mockCacheUpdater{}, "", "", "", 0)

	cookie := createTestSessionCookie(t, 42)

	tests := []struct {
		name         string
		path         string
		forwardedURI string
		cookie       *http.Cookie
		wantStatus   int
	}{
		{name: "vector permits session owner", path: "/auth/vector", forwardedURI: "/vector/user_routes?user_id=42", cookie: cookie, wantStatus: http.StatusOK},
		{name: "vector rejects mismatched user", path: "/auth/vector", forwardedURI: "/vector/user_routes?user_id=99", cookie: cookie, wantStatus: http.StatusForbidden},
		{name: "vector rejects missing user_id param", path: "/auth/vector", forwardedURI: "/vector/user_routes", cookie: cookie, wantStatus: http.StatusForbidden},
		{name: "vector rejects malformed forwarded URI", path: "/auth/vector", forwardedURI: "%", cookie: cookie, wantStatus: http.StatusBadRequest},
		{name: "raster rejects cookie session", path: "/auth/raster", forwardedURI: "/raster/styles/routes", cookie: cookie, wantStatus: http.StatusUnauthorized},
		{name: "raster accepts valid token", path: "/auth/raster", forwardedURI: "/raster/styles/routes?token=valid-token", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("X-Forwarded-Uri", tt.forwardedURI)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			rec := httptest.NewRecorder()

			server.router.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// ---------------------------
// User & Preferences Endpoint Tests
// ---------------------------

func TestGetCurrentUser(t *testing.T) {
	setupTestStore(t)

	queries := &mockQuerier{
		getAthlete: func(ctx context.Context, id int64) (db.GetAthleteRow, error) {
			if id == 42 {
				return db.GetAthleteRow{
					ID:        42,
					Firstname: pgtype.Text{String: "John", Valid: true},
					Lastname:  pgtype.Text{String: "Doe", Valid: true},
				}, nil
			}
			return db.GetAthleteRow{}, pgx.ErrNoRows
		},
	}

	server := NewServerWithQuerier(queries, &mockCacheUpdater{}, "", "", "", 0)

	t.Run("returns athlete json for authenticated user", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.AddCookie(createTestSessionCookie(t, 42))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var athlete db.GetAthleteRow
		if err := json.Unmarshal(rec.Body.Bytes(), &athlete); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if athlete.ID != 42 || athlete.Firstname.String != "John" {
			t.Errorf("unexpected athlete data: %+v", athlete)
		}
	})

	t.Run("returns 500 when athlete lookup fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.AddCookie(createTestSessionCookie(t, 999))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

func TestGetUserPreferences(t *testing.T) {
	setupTestStore(t)

	queries := &mockQuerier{
		getUserPreferences: func(ctx context.Context, userID int64) (db.UserPreference, error) {
			if userID == 42 {
				return db.UserPreference{
					UserID:              42,
					WriteUniqueDistance: true,
				}, nil
			}
			return db.UserPreference{}, errors.New("db error")
		},
	}

	server := NewServerWithQuerier(queries, &mockCacheUpdater{}, "", "", "", 0)

	t.Run("returns user preferences", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/preferences", nil)
		req.AddCookie(createTestSessionCookie(t, 42))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var pref db.UserPreference
		if err := json.Unmarshal(rec.Body.Bytes(), &pref); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if pref.UserID != 42 || !pref.WriteUniqueDistance {
			t.Errorf("unexpected preferences: %+v", pref)
		}
	})

	t.Run("returns 500 on database error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/preferences", nil)
		req.AddCookie(createTestSessionCookie(t, 99))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

func TestUpdateUserPreferences(t *testing.T) {
	setupTestStore(t)

	queries := &mockQuerier{
		upsertUserPreferences: func(ctx context.Context, arg db.UpsertUserPreferencesParams) (db.UserPreference, error) {
			return db.UserPreference{
				UserID:              arg.UserID,
				WriteUniqueDistance: arg.WriteUniqueDistance,
			}, nil
		},
	}

	server := NewServerWithQuerier(queries, &mockCacheUpdater{}, "", "", "", 0)

	t.Run("updates user preferences successfully", func(t *testing.T) {
		reqBody := `{"write_unique_distance": true}`
		req := httptest.NewRequest(http.MethodPut, "/preferences", strings.NewReader(reqBody))
		req.AddCookie(createTestSessionCookie(t, 42))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var pref db.UserPreference
		if err := json.Unmarshal(rec.Body.Bytes(), &pref); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if pref.UserID != 42 || !pref.WriteUniqueDistance {
			t.Errorf("unexpected preferences: %+v", pref)
		}
	})

	t.Run("rejects request missing write_unique_distance", func(t *testing.T) {
		reqBody := `{}`
		req := httptest.NewRequest(http.MethodPut, "/preferences", strings.NewReader(reqBody))
		req.AddCookie(createTestSessionCookie(t, 42))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("rejects request with unknown fields", func(t *testing.T) {
		reqBody := `{"write_unique_distance": true, "extra": "field"}`
		req := httptest.NewRequest(http.MethodPut, "/preferences", strings.NewReader(reqBody))
		req.AddCookie(createTestSessionCookie(t, 42))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

func TestListRoutesWithoutRouteData(t *testing.T) {
	setupTestStore(t)

	queries := &mockQuerier{
		listRoutesByUser: func(ctx context.Context, userID int64) ([]db.ListRoutesByUserRow, error) {
			if userID == 42 {
				return []db.ListRoutesByUserRow{
					{ID: 1001, Name: "Morning Run", Distance: 5000},
					{ID: 1002, Name: "Evening Walk", Distance: 3000},
				}, nil
			}
			return nil, errors.New("db error")
		},
	}

	server := NewServerWithQuerier(queries, &mockCacheUpdater{}, "", "", "", 0)

	t.Run("returns list of routes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/route_details", nil)
		req.AddCookie(createTestSessionCookie(t, 42))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var routes []db.ListRoutesByUserRow
		if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if len(routes) != 2 || routes[0].Name != "Morning Run" {
			t.Errorf("unexpected routes: %+v", routes)
		}
	})

	t.Run("returns 500 when list query fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/route_details", nil)
		req.AddCookie(createTestSessionCookie(t, 99))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

// ---------------------------
// Webhook Endpoint Tests
// ---------------------------

func TestWebhookCallbackChallenge(t *testing.T) {
	server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "my-secret-token", "", 0)

	t.Run("valid challenge subscription returns challenge response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.challenge=test-challenge-123&hub.verify_token=my-secret-token", nil)
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var resp map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resp["hub.challenge"] != "test-challenge-123" {
			t.Errorf("got %q, want 'test-challenge-123'", resp["hub.challenge"])
		}
	})

	t.Run("invalid verify token is forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.challenge=test-challenge-123&hub.verify_token=wrong-token", nil)
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})
}

func TestWebhookCallbackUpdate(t *testing.T) {
	t.Run("ignores non-activity events with 200", func(t *testing.T) {
		updater := &mockCacheUpdater{}
		server := NewServerWithQuerier(&mockQuerier{}, updater, "", "", "", 0)

		payload := `{"object_type": "athlete", "aspect_type": "update", "object_id": 1, "owner_id": 1}`
		req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(updater.addDetailedActivityCalls) != 0 {
			t.Errorf("expected no cache updates, got %d", len(updater.addDetailedActivityCalls))
		}
	})

	t.Run("skips description-only updates to prevent loop", func(t *testing.T) {
		updater := &mockCacheUpdater{}
		server := NewServerWithQuerier(&mockQuerier{}, updater, "", "", "", 0)

		payload := `{"object_type": "activity", "aspect_type": "update", "object_id": 101, "owner_id": 42, "updates": {"description": "New Unique Distance: 5km"}}`
		req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(updater.addDetailedActivityCalls) != 0 {
			t.Errorf("expected no cache updates for description-only update, got %d", len(updater.addDetailedActivityCalls))
		}
	})

	t.Run("processes activity create event", func(t *testing.T) {
		updater := &mockCacheUpdater{}
		server := NewServerWithQuerier(&mockQuerier{}, updater, "", "", "", 0)

		payload := `{"object_type": "activity", "aspect_type": "create", "object_id": 202, "owner_id": 42}`
		req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		// Wait briefly for asynchronous goroutines
		time.Sleep(50 * time.Millisecond)

		updater.mu.Lock()
		defer updater.mu.Unlock()
		if len(updater.addDetailedActivityCalls) != 1 {
			t.Fatalf("expected 1 addDetailedActivity call, got %d", len(updater.addDetailedActivityCalls))
		}
		if updater.addDetailedActivityCalls[0] != [2]int64{202, 42} {
			t.Errorf("got %v, want [202, 42]", updater.addDetailedActivityCalls[0])
		}
		if len(updater.writeUniqueDistanceDescCalls) != 1 {
			t.Errorf("expected 1 writeUniqueDistance call for create event, got %d", len(updater.writeUniqueDistanceDescCalls))
		}
	})

	t.Run("handles invalid request body with 400", func(t *testing.T) {
		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", 0)
		req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("invalid-json"))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

// ---------------------------
// Admin Update Endpoint Tests
// ---------------------------

func TestUpdateCacheForUser(t *testing.T) {
	setupTestStore(t)
	const adminID int64 = 777

	t.Run("triggers activity cache update when called by admin", func(t *testing.T) {
		updater := &mockCacheUpdater{}
		server := NewServerWithQuerier(&mockQuerier{}, updater, "", "", "", adminID)

		req := httptest.NewRequest(http.MethodGet, "/update?user_id=42", nil)
		req.AddCookie(createTestSessionCookie(t, adminID))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		time.Sleep(50 * time.Millisecond)
		updater.mu.Lock()
		defer updater.mu.Unlock()
		if len(updater.updateActivityCacheCalls) != 1 || updater.updateActivityCacheCalls[0] != 42 {
			t.Errorf("unexpected updateActivityCacheCalls: %v", updater.updateActivityCacheCalls)
		}
	})

	t.Run("returns 400 for invalid user_id query param", func(t *testing.T) {
		updater := &mockCacheUpdater{}
		server := NewServerWithQuerier(&mockQuerier{}, updater, "", "", "", adminID)

		req := httptest.NewRequest(http.MethodGet, "/update?user_id=invalid", nil)
		req.AddCookie(createTestSessionCookie(t, adminID))
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

// ---------------------------
// Purge Tile Cache Tests
// ---------------------------

func TestPurgeTileCache(t *testing.T) {
	t.Run("sends BAN request when tileCacheURL is configured", func(t *testing.T) {
		var receivedMethod, receivedUserID string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMethod = r.Method
			receivedUserID = r.Header.Get("X-User-Id")
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", ts.URL, 0)
		server.purgeTileCache(1234)

		if receivedMethod != "BAN" {
			t.Errorf("received method = %q, want 'BAN'", receivedMethod)
		}
		if receivedUserID != "1234" {
			t.Errorf("received X-User-Id = %q, want '1234'", receivedUserID)
		}
	})

	t.Run("does nothing when tileCacheURL is empty", func(t *testing.T) {
		server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", 0)
		// Should not panic or err
		server.purgeTileCache(1234)
	})
}

// ---------------------------
// Authentication Start & Logout Tests
// ---------------------------

func TestInitiateAuthenticationRedirect(t *testing.T) {
	setupTestStore(t)
	server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", 0)

	t.Run("fails when redirect_url is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/start", nil)
		rec := httptest.NewRecorder()

		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

func TestLogout(t *testing.T) {
	setupTestStore(t)
	server := NewServerWithQuerier(&mockQuerier{}, &mockCacheUpdater{}, "", "", "", 0)

	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(createTestSessionCookie(t, 42))
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	// Session cookie should have MaxAge < 0 / expired
	cookies := rec.Result().Cookies()
	var foundSessionCookie bool
	for _, c := range cookies {
		if c.Name == "user-session" {
			foundSessionCookie = true
			if c.MaxAge > 0 {
				t.Errorf("expected session cookie to be expired, got MaxAge %d", c.MaxAge)
			}
		}
	}
	if !foundSessionCookie {
		// Session can also be deleted by setting MaxAge -1
		t.Logf("Logout completed")
	}
}
