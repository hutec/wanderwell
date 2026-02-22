package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
	"wanderwell/backend/models"
	"wanderwell/backend/strava"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/httplog/v3"
	"github.com/markbates/goth/gothic"
	"github.com/twpayne/go-polyline"
)

type Server struct {
	db           *sql.DB
	cacheUpdater *strava.CacheUpdater
	router       chi.Router
	frontendURL  string
}

func NewServer(db *sql.DB, cacheUpdater *strava.CacheUpdater, frontendURL string) *Server {
	s := &Server{
		db:           db,
		cacheUpdater: cacheUpdater,
		router:       chi.NewRouter(),
		frontendURL:  frontendURL,
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
	s.router.Get("/users", s.listUsers)

	// Protected routes - require authentication
	s.router.Group(func(r chi.Router) {
		r.Use(s.RequireAuth)
		r.Get("/me", s.getCurrentUser)
		r.Get("/routes", s.listRoutesWithRouteData)
		r.Get("/route_details", s.listRoutesWithoutRouteData)
		r.Get("/geojson", s.serveGeojsonForUser)
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

	// Fetch user from database
	var user models.User
	err := s.db.QueryRow(
		"SELECT id, firstname, lastname FROM athlete WHERE id = $1",
		userID,
	).Scan(&user.ID, &user.Firstname, &user.Lastname)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		slog.Error("Failed to fetch user", "error", err)
		http.Error(w, "Failed to fetch user", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	listUsers(s.db)(w, r)
}

func (s *Server) listRoutesWithRouteData(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(int64)
	if !ok {
		http.Error(w, "user_id not found in context", http.StatusBadRequest)
	}
	listRoutesByUser(s.db, userID, false)(w, r) // excludeRoute = false
}

func (s *Server) listRoutesWithoutRouteData(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(int64)
	if !ok {
		http.Error(w, "user_id not found in context", http.StatusBadRequest)
	}
	listRoutesByUser(s.db, userID, true)(w, r) // excludeRoute = true
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
		}
	}()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) serveGeojsonForUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(int64)
	if !ok {
		http.Error(w, "user_id not found in context", http.StatusBadRequest)
	}
	serveGeojsonForUser(s.db, userID)(w, r)
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
	}
	slog.Info("User authenticated", "user", user)
	userID, err := strconv.ParseInt(user.UserID, 10, 64)
	if err != nil {
		slog.Error("Failed to parse userID", "error", err)
	}

	// Insert or update user in database
	_, err = s.db.Exec(`
		INSERT INTO athlete (id, firstname, lastname, access_token, refresh_token, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(id) DO UPDATE SET
			firstname = excluded.firstname,
			lastname = excluded.lastname,
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			expires_at = excluded.expires_at
	`, userID, user.FirstName, user.LastName, user.AccessToken, user.RefreshToken, user.ExpiresAt.Unix())

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

	expectedVerifyToken := "STRAVA" // Replace with your actual verify token
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
		w.WriteHeader(http.StatusOK)
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

func listUsers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query("SELECT id, firstname, lastname, expires_at, refresh_token, access_token FROM athlete")
		if err != nil {
			http.Error(w, "Failed to query users", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var users []models.User
		for rows.Next() {
			var user models.User
			if err := rows.Scan(&user.ID, &user.Firstname, &user.Lastname, &user.ExpiresAt, &user.RefreshToken, &user.AccessToken); err != nil {
				http.Error(w, "Failed to scan user", http.StatusInternalServerError)
				return
			}
			users = append(users, user)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "Error iterating over users", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(users)
	}
}

func listRoutesByUser(db *sql.DB, userID int64, excludeRoute bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query("SELECT id, user_id, start_date, name, elapsed_time, moving_time, distance, average_speed, route, elevation, bounds, sport_type  FROM route WHERE user_id = $1 ORDER BY start_date DESC", userID)
		if err != nil {
			http.Error(w, "Failed to query routes", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var routes []models.Route
		for rows.Next() {
			var route models.Route
			if err := rows.Scan(&route.ID, &route.UserID, &route.StartDate, &route.Name, &route.ElapsedTime, &route.MovingTime, &route.Distance, &route.AverageSpeed, &route.Route, &route.Elevation, &route.Bounds, &route.SportType); err != nil {
				http.Error(w, fmt.Sprintf("Failed to scan route: %v", err), http.StatusInternalServerError)
				return
			}

			if excludeRoute {
				route.Route = ""
			}

			routes = append(routes, route)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "Error iterating over routes", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(routes)
	}
}

func serveGeojsonForUser(db *sql.DB, userID int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Create a temporary file to write the GeoJSON data
		tmpFile, err := os.CreateTemp("", fmt.Sprintf("geojson_user_%d_*.json", userID))
		if err != nil {
			http.Error(w, "Failed to create temporary file", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpFile.Name()) // Clean up the temp file
		defer tmpFile.Close()

		// Start writing the GeoJSON structure to the file
		encoder := json.NewEncoder(tmpFile)

		// Write the opening of the FeatureCollection
		if _, err := tmpFile.WriteString(`{"type":"FeatureCollection","features":[`); err != nil {
			http.Error(w, "Failed to write to temporary file", http.StatusInternalServerError)
			return
		}

		// Query routes with all necessary fields, ordered by start_date descending
		rows, err := db.Query("SELECT id, start_date, route FROM route WHERE user_id = $1 ORDER BY start_date DESC", userID)
		if err != nil {
			http.Error(w, "Failed to query routes", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		firstFeature := true
		for rows.Next() {
			var routeID string
			var startDate time.Time
			var routePolyline string

			if err := rows.Scan(&routeID, &startDate, &routePolyline); err != nil {
				http.Error(w, "Failed to scan route", http.StatusInternalServerError)
				return
			}

			// Decode the polyline to get coordinates
			coords, _, err := polyline.DecodeCoords([]byte(routePolyline))
			if err != nil {
				slog.Error("Failed to decode polyline", "routeID", routeID, "err", err)
				continue // Skip this route but continue with others
			}

			// Convert coordinates to GeoJSON format [lng, lat] and swap lat/lng like Python version
			var geoJSONCoords [][]float32
			for _, coord := range coords {
				// Python version does: [x[1], x[0]] which swaps lat/lng to lng/lat
				geoJSONCoords = append(geoJSONCoords, []float32{float32(coord[1]), float32(coord[0])})
			}

			// Create GeoJSON feature similar to Python version
			feature := map[string]interface{}{
				"type": "Feature",
				"geometry": map[string]interface{}{
					"type":        "LineString",
					"coordinates": geoJSONCoords,
				},
				"properties": map[string]interface{}{
					"id":         routeID,
					"start_date": startDate.Unix(), // Convert to seconds (Python divides ms by 1000)
				},
			}

			// Add comma separator for subsequent features
			if !firstFeature {
				if _, err := tmpFile.WriteString(","); err != nil {
					http.Error(w, "Failed to write to temporary file", http.StatusInternalServerError)
					return
				}
			}
			firstFeature = false

			// Write the feature to the file
			if err := encoder.Encode(feature); err != nil {
				http.Error(w, "Failed to encode feature to temporary file", http.StatusInternalServerError)
				return
			}
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "Error iterating over routes", http.StatusInternalServerError)
			return
		}

		// Close the FeatureCollection
		if _, err := tmpFile.WriteString("]}"); err != nil {
			http.Error(w, "Failed to write to temporary file", http.StatusInternalServerError)
			return
		}

		// Close the file to ensure all data is flushed
		if err := tmpFile.Close(); err != nil {
			http.Error(w, "Failed to close temporary file", http.StatusInternalServerError)
			return
		}

		// Reopen the file for reading
		file, err := os.Open(tmpFile.Name())
		if err != nil {
			http.Error(w, "Failed to open temporary file for reading", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Set headers and serve the file content
		w.Header().Set("Content-Type", "application/json")

		// Copy the file content to the response writer
		if _, err := io.Copy(w, file); err != nil {
			slog.Error("Failed to serve GeoJSON file", "err", err)
		}
	}
}
