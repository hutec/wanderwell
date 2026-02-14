package strava

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
	swagger "wanderwell/backend/client"
	"wanderwell/backend/config"
	"wanderwell/backend/models"

	"github.com/antihax/optional"
)

// API wrapper that handles authentication and requests to Strava's API.
type StravaAPI struct {
	db        *sql.DB
	dbMutex   sync.Mutex
	cfg       *config.Config
	apiClient *swagger.APIClient
	RateLimit *RateLimit
}

func NewStravaAPI(db *sql.DB, cfg *config.Config) *StravaAPI {
	apiConfig := swagger.NewConfiguration()
	apiClient := swagger.NewAPIClient(apiConfig)
	rateLimit := NewRateLimit()

	return &StravaAPI{
		db:        db,
		cfg:       cfg,
		apiClient: apiClient,
		RateLimit: rateLimit,
	}
}

// GetAthleteAccessToken retrieves the access token for a given athlete.
// If the token is expired it is automatically refreshed.
func (api *StravaAPI) GetAthleteAccessToken(athleteID int64) (string, error) {
	var user models.User
	err := api.db.QueryRow("SELECT id, expires_at, refresh_token, access_token FROM user WHERE id = ?", athleteID).
		Scan(&user.ID, &user.ExpiresAt, &user.RefreshToken, &user.AccessToken)
	if err != nil {
		return "", err
	}
	// If token is expired or about to expire, refresh it
	if user.ExpiresAt < time.Now().Unix() {
		slog.Info("Refreshing token for user", "userID", athleteID)
		tokenResp, err := refreshToken(user.RefreshToken, api.cfg.StravaClientID, api.cfg.StravaClientSecret)
		if err != nil {
			slog.Error("Failed to refresh token", "error", err)
			return "", err
		}
		user.AccessToken = tokenResp.AccessToken
		user.ExpiresAt = tokenResp.ExpiresAt

		// Update user in database
		api.dbMutex.Lock()
		_, err = api.db.Exec("UPDATE user SET access_token = ?, expires_at = ? WHERE id = ?", user.AccessToken, user.ExpiresAt, user.ID)
		api.dbMutex.Unlock()
		if err != nil {
			slog.Error("Failed to update user", "error", err)
			return "", err
		}
	}
	return user.AccessToken, nil
}

// GetAthleteSummaryActivities fetches all summary activities for a given athlete.
// maxPages limits the number of pages to fetch; if 0, fetch all pages
func (api *StravaAPI) GetAthleteSummaryActivities(athleteID int64, maxPages int) ([]swagger.SummaryActivity, error) {
	slog.Info("Getting all activities for user", "athleteID", athleteID)

	var allActivities []swagger.SummaryActivity
	page := int32(1)
	for {
		activities, err := api.GetAthleteSummaryActivitiesByPage(athleteID, page)
		if err != nil {
			return nil, err
		}
		if len(activities) == 0 {
			break
		}
		allActivities = append(allActivities, activities...)
		page++

		if maxPages > 0 && page > int32(maxPages) {
			break // Reached the maximum number of pages to fetch
		}
	}
	return allActivities, nil
}

func (api *StravaAPI) GetAthleteSummaryActivitiesByPage(athleteID int64, page int32) ([]swagger.SummaryActivity, error) {
	slog.Info("Getting activities for athlete by page", "athleteID", athleteID, "page", page)

	accessToken, err := api.GetAthleteAccessToken(athleteID)
	if err != nil {
		return nil, err
	}

	limitType := api.RateLimit.IsRateLimitExceeded()
	if limitType != RateLimitNone {
		slog.Info("Rate limit exceeded", "limitType", limitType)
		api.RateLimit.WaitForRateLimitReset(limitType)
	}

	var allActivities []swagger.SummaryActivity
	opts := &swagger.ActivitiesApiGetLoggedInAthleteActivitiesOpts{
		PerPage: optional.NewInt32(200), // Strava API maximum is 200
		Page:    optional.NewInt32(page),
	}
	ctx := context.WithValue(context.Background(), swagger.ContextAccessToken, accessToken)
	activities, resp, err := api.apiClient.ActivitiesApi.GetLoggedInAthleteActivities(ctx, opts)
	api.RateLimit.UpdateRateLimit(resp)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			// This happens if the rate limit was exceeded during the request or just initialized
			slog.Info("Rate limit exceeded on activity fetch", "athleteID", athleteID, "page", page)
			// call recursively, since the rate limit has been updated, it will wait
			return api.GetAthleteSummaryActivitiesByPage(athleteID, page)
		}
		slog.Error("Failed to get activities", "error", err)
		return nil, fmt.Errorf("failed to get activities for page %d: %w", page, err)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("Failed to get activities", "error", err)
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	allActivities = append(allActivities, activities...)
	return allActivities, nil
}

// GetDetailedActivityByID fetches detailed information for a specific activity by its ID.
func (api *StravaAPI) GetDetailedActivityByID(activityID int64, athleteID int64) (*swagger.DetailedActivity, error) {
	slog.Info("Fetching detailed activity info", "activityID", activityID)
	accessToken, err := api.GetAthleteAccessToken(athleteID)
	if err != nil {
		return nil, err
	}

	limitType := api.RateLimit.IsRateLimitExceeded()
	if limitType != RateLimitNone {
		slog.Info("Rate limit exceeded", "limitType", limitType)
		api.RateLimit.WaitForRateLimitReset(limitType)
	}

	ctx := context.WithValue(context.Background(), swagger.ContextAccessToken, accessToken)
	detailedActivity, resp, err := api.apiClient.ActivitiesApi.GetActivityById(ctx, activityID, nil)
	api.RateLimit.UpdateRateLimit(resp)
	if err != nil {
		if resp.StatusCode == http.StatusTooManyRequests {
			// TODO: Add recursion limit to avoid infinite loops
			slog.Info("Rate limit exceeded on detailed activity fetch", "activityID", activityID)
			// call recursively, since the rate limit has been updated, it will wait
			return api.GetDetailedActivityByID(activityID, athleteID)

		}
		slog.Error("Failed to get detailed activity", "activityID", activityID, "error", err)
		return nil, fmt.Errorf("failed to get detailed activity for ID %d: %w", activityID, err)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("Failed to get detailed activity", "error", err)
		return nil, fmt.Errorf("API request for detailed activity failed with status: %d", resp.StatusCode)
	}

	return &detailedActivity, nil
}
