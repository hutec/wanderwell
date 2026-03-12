package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"wanderwell/backend/db"
	"wanderwell/backend/strava"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/httplog/v3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/markbates/goth/gothic"
)

type Server struct {
	queries      *db.Queries
	cacheUpdater *strava.CacheUpdater
	router       chi.Router
	frontendURL  string
	verifyToken  string
	tileCacheURL string
}

func NewServer(pool *pgxpool.Pool, cacheUpdater *strava.CacheUpdater, frontendURL string, verifyToken string, tileCacheURL string) *Server {
	s := &Server{
		queries:      db.New(pool),
		cacheUpdater: cacheUpdater,
		router:       chi.NewRouter(),
		frontendURL:  frontendURL,
		verifyToken:  verifyToken,
		tileCacheURL: tileCacheURL,
	}
	s.setupRoutes()
	return s
}

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const userIDKey contextKey = "userID"

// RequireAuth is a middleware that checks for a valid user session
func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gothic.Store.Get(r, "user-session")
		if err != nil {
			slog.Error("Failed to get session", "error", err)
			http.Error(w, "Invalid session", http.StatusUnauthorized)
			return
		}

		userIDRaw, ok := session.Values["user_id"]
		if !ok {
			slog.Error("user_id not found in session")
			http.Error(w, "user_id not found in session", http.StatusUnauthorized)
			return
		}

		userID, ok := userIDRaw.(int64)
		if !ok {
			slog.Error("user_id is not int64", "type", fmt.Sprintf("%T", userIDRaw))
			http.Error(w, "Invalid user_id type", http.StatusInternalServerError)
			return
		}

		// Add userID to request context
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) setupRoutes() {
	// CORS configuration
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{s.frontendURL},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))

	s.router.Use(httplog.RequestLogger(slog.Default(), nil))

	// Protected routes - require authentication
	s.router.Group(func(r chi.Router) {
		r.Use(s.RequireAuth)
		r.Get("/me", s.getCurrentUser)
		r.Get("/route_details", s.listRoutesWithoutRouteData)
		r.Get("/update", s.updateCacheForUser)
	})

	// Public routes
	s.router.Get("/start", s.initiateAuthentication)
	s.router.Get("/user_token_exchange", s.tokenExchange)
	s.router.Get("/webhook", s.webhookCallbackChallenge)
	s.router.Post("/webhook", s.webhookCallbackUpdate)
	s.router.Get("/logout", s.logout)
}

func (s *Server) Start(addr string) error {
	slog.Info("Starting server", "addr", addr)
	return http.ListenAndServe(addr, s.router)
}

// purgeTileCache sends a BAN request to Vinyl Cache to invalidate all cached tiles for a user.
// It is a no-op when no tile cache URL is configured.
func (s *Server) purgeTileCache(userID int64) {
	if s.tileCacheURL == "" {
		return
	}
	req, err := http.NewRequest("BAN", s.tileCacheURL, nil)
	if err != nil {
		slog.Error("Failed to create tile cache BAN request", "userID", userID, "error", err)
		return
	}
	req.Header.Set("X-User-Id", strconv.FormatInt(userID, 10))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Failed to send tile cache BAN request", "userID", userID, "error", err)
		return
	}
	defer resp.Body.Close()
	slog.Info("Tile cache ban sent", "userID", userID, "status", resp.Status)
}

func parseUserID(r *http.Request) (int64, error) {
	userIDParam := chi.URLParam(r, "userID")
	var userID int64
	_, err := fmt.Sscan(userIDParam, &userID)
	if err != nil {
		return 0, fmt.Errorf("invalid user ID: %w", err)
	}
	return userID, nil
}

func (s *Server) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(int64)
	if !ok {
		http.Error(w, "user_id not found in context", http.StatusUnauthorized)
		return
	}

	athlete, err := s.queries.GetAthlete(r.Context(), userID)
	if err != nil {
		slog.Error("Failed to fetch user", "error", err)
		http.Error(w, "Failed to fetch user", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(athlete)
}

func (s *Server) listRoutesWithoutRouteData(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(int64)
	if !ok {
		http.Error(w, "user_id not found in context", http.StatusBadRequest)
		return
	}
	listRoutesByUser(s.queries, userID)(w, r)
}

func (s *Server) updateCacheForUser(w http.ResponseWriter, r *http.Request) {

	userID, err := parseUserID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code != "UPDATE" {
		http.Error(w, "Invalid code parameter", http.StatusBadRequest)
		return
	}

	// start background task to fetch and cache user activities
	go func() {
		if err := s.cacheUpdater.UpdateActivityCache(userID); err != nil {
			slog.Error("Failed to fetch initial activities for user", "userID", userID, "error", err)
			return
		}
		s.purgeTileCache(userID)
	}()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) initiateAuthentication(w http.ResponseWriter, r *http.Request) {
	redirectURL := r.URL.Query().Get("redirect_url")
	if redirectURL == "" {
		http.Error(w, "Missing redirect_url parameter", http.StatusBadRequest)
		return
	}

	session, _ := gothic.Store.New(r, "user-session")
	session.Values["redirect_url"] = redirectURL
	session.Save(r, w)

	r = r.WithContext(context.WithValue(r.Context(), "provider", "strava"))
	gothic.BeginAuthHandler(w, r)
}

func (s *Server) tokenExchange(w http.ResponseWriter, r *http.Request) {
	user, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		slog.Error("Failed to complete user authentication", "error", err)
		http.Error(w, "Failed to complete user authentication", http.StatusInternalServerError)
		return
	}
	slog.Info("User authenticated", "user", user.UserID)
	userID, err := strconv.ParseInt(user.UserID, 10, 64)
	if err != nil {
		slog.Error("Failed to parse userID", "error", err)
	}

	// Insert or update user in database
	err = s.queries.UpsertAthlete(r.Context(), db.UpsertAthleteParams{
		ID:           userID,
		Firstname:    pgtype.Text{String: user.FirstName, Valid: true},
		Lastname:     pgtype.Text{String: user.LastName, Valid: true},
		AccessToken:  pgtype.Text{String: user.AccessToken, Valid: true},
		RefreshToken: pgtype.Text{String: user.RefreshToken, Valid: true},
		ExpiresAt:    pgtype.Int8{Int64: user.ExpiresAt.Unix(), Valid: true},
	})

	if err != nil {
		http.Error(w, "Failed to save user", http.StatusInternalServerError)
		return
	}

	// Set a session cookie
	session, _ := gothic.Store.New(r, "user-session")
	session.Values["user_id"] = userID

	// Get the redirect URL from session
	redirectURL, ok := session.Values["redirect_url"].(string)
	if !ok || redirectURL == "" {
		http.Error(w, "No redirect URL found", http.StatusInternalServerError)
		return
	}

	// Clean up redirect_url from session
	delete(session.Values, "redirect_url")

	session.Save(r, w)

	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)

	// start background task to fetch and cache user activities
	// go func() {
	// 	if err = s.cacheUpdater.UpdateActivityCache(userID); err != nil {
	// 		slog.Error("Failed to fetch initial activities for user", "userID", userID, "error", err)
	// 	}
	// }()
}

// webhookCallback subscribes to Strava webhook events
// For  more details see https://developers.strava.com/docs/webhooks/
// Handle webhook verification challenge
func (s *Server) webhookCallbackChallenge(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	challenge := r.URL.Query().Get("hub.challenge")
	verifyToken := r.URL.Query().Get("hub.verify_token")

	expectedVerifyToken := s.verifyToken
	if mode == "subscribe" && verifyToken == expectedVerifyToken {
		slog.Info("Webhook verified successfully")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"hub.challenge": challenge,
		})
	} else {
		http.Error(w, "Verification failed", http.StatusForbidden)
	}
}

func (s *Server) webhookCallbackUpdate(w http.ResponseWriter, r *http.Request) {
	// Define the structure of the Strava webhook event, discaring unneeded field `updates`
	var stravaEvent struct {
		ObjectType     string `json:"object_type"`
		ObjectID       int64  `json:"object_id"`
		AspectType     string `json:"aspect_type"`
		OwnerID        int64  `json:"owner_id"`
		SubscriptionID int64  `json:"subscription_id"`
		EventTime      int64  `json:"event_time"`
	}

	// Read the request body
	if err := json.NewDecoder(r.Body).Decode(&stravaEvent); err != nil {
		slog.Error("Failed to decode webhook event", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	slog.Info("Received Strava webhook event", "object_type", stravaEvent.ObjectType, "aspect_type", stravaEvent.AspectType, "owner_id", stravaEvent.OwnerID, "object_id", stravaEvent.ObjectID)
	if stravaEvent.ObjectType != "activity" {
		// We only care about activity events for now
		w.WriteHeader(http.StatusOK)
		return
	}

	if stravaEvent.AspectType == "update" || stravaEvent.AspectType == "create" {
		if err := s.cacheUpdater.AddDetailedActivity(stravaEvent.ObjectID, stravaEvent.OwnerID); err != nil {
			slog.Error("Failed to process activity update/create", "activity_id", stravaEvent.ObjectID, "owner_id", stravaEvent.OwnerID, "error", err)
			http.Error(w, "Failed to process activity", http.StatusInternalServerError)
			return
		}
		// Respond to Strava first to avoid webhook retries, then purge cache asynchronously.
		w.WriteHeader(http.StatusOK)
		go s.purgeTileCache(stravaEvent.OwnerID)
		return
	} else {
		slog.Info("Unhandled aspect type in webhook event", "aspect_type", stravaEvent.AspectType)
		w.WriteHeader(http.StatusOK)
	}
	// TODO: Handle delete events if needed
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, err := gothic.Store.Get(r, "user-session")
	if err != nil {
		fmt.Fprintf(w, "Error getting session: %v\n", err)
	}

	session.Options.MaxAge = -1
	// Save the session (this sends the delete instruction to the browser)
	err = session.Save(r, w)
	if err != nil {
		fmt.Fprintf(w, "Error deleting session: %v", err)
		return
	}
	gothic.Logout(w, r)

}

func listRoutesByUser(q *db.Queries, userID int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		routes, err := q.ListRoutesByUser(r.Context(), userID)
		if err != nil {
			http.Error(w, "Failed to query routes", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(routes)
	}
}
