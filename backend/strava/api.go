package strava

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
	swagger "wanderwell/backend/client"
	"wanderwell/backend/config"
	"wanderwell/backend/db"

	"github.com/antihax/optional"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// API wrapper that handles authentication and requests to Strava's API.
type StravaAPI struct {
	queries   *db.Queries
	tokenMu   sync.Mutex // Serializes token refreshes, since Strava rotates refresh tokens.
	cfg       *config.Config
	apiClient *swagger.APIClient
	RateLimit *RateLimit
}

func NewStravaAPI(pool *pgxpool.Pool, cfg *config.Config) *StravaAPI {
	return &StravaAPI{
		queries:   db.New(pool),
		cfg:       cfg,
		apiClient: swagger.NewAPIClient(swagger.NewConfiguration()),
		RateLimit: NewRateLimit(),
	}
}

// GetAthleteAccessToken retrieves the access token for a given athlete.
// If the token is expired or about to expire it is automatically refreshed.
func (api *StravaAPI) GetAthleteAccessToken(athleteID int64) (string, error) {
	api.tokenMu.Lock()
	defer api.tokenMu.Unlock()

	row, err := api.queries.GetAthleteTokens(context.Background(), athleteID)
	if err != nil {
		return "", err
	}
	if row.ExpiresAt.Int64 > time.Now().Add(time.Minute).Unix() {
		return row.AccessToken.String, nil
	}

	slog.Info("Refreshing token for user", "userID", athleteID)
	tokenResp, err := refreshToken(row.RefreshToken.String, api.cfg.StravaClientID, api.cfg.StravaClientSecret)
	if err != nil {
		slog.Error("Failed to refresh token", "error", err)
		return "", err
	}
	// Strava may rotate the refresh token; keep the old one if none was returned.
	newRefreshToken := row.RefreshToken
	if tokenResp.RefreshToken != "" {
		newRefreshToken = pgtype.Text{String: tokenResp.RefreshToken, Valid: true}
	}
	err = api.queries.UpdateAthleteTokens(context.Background(), db.UpdateAthleteTokensParams{
		ID:           athleteID,
		AccessToken:  pgtype.Text{String: tokenResp.AccessToken, Valid: true},
		ExpiresAt:    pgtype.Int8{Int64: tokenResp.ExpiresAt, Valid: true},
		RefreshToken: newRefreshToken,
	})
	if err != nil {
		slog.Error("Failed to update user", "error", err)
		return "", err
	}
	return tokenResp.AccessToken, nil
}

const maxRequestAttempts = 3

// doWithRateLimit sends a request once it fits in the rate limits, and retries
// up to maxRequestAttempts times on HTTP 429 (Update starts a cooldown first).
// Any non-200 response is returned as an error.
func doWithRateLimit(rl *RateLimit, isRead bool, request func() (*http.Response, error)) error {
	for attempt := 1; ; attempt++ {
		ticket := rl.Acquire(isRead)
		resp, err := request()
		rl.Update(ticket, resp)
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests && attempt < maxRequestAttempts {
			slog.Info("Strava rate limit exceeded, retrying", "attempt", attempt)
			continue
		}
		if err != nil {
			return err
		}
		if resp == nil {
			return errors.New("no response from Strava")
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("strava returned status %d", resp.StatusCode)
		}
		return nil
	}
}

// do runs a rate-limited request on behalf of an athlete. The access token is
// fetched after waiting for the rate limit, so it cannot expire during the wait.
func (api *StravaAPI) do(athleteID int64, isRead bool, request func(ctx context.Context) (*http.Response, error)) error {
	return doWithRateLimit(api.RateLimit, isRead, func() (*http.Response, error) {
		accessToken, err := api.GetAthleteAccessToken(athleteID)
		if err != nil {
			return nil, err
		}
		return request(context.WithValue(context.Background(), swagger.ContextAccessToken, accessToken))
	})
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

	opts := &swagger.ActivitiesApiGetLoggedInAthleteActivitiesOpts{
		PerPage: optional.NewInt32(200), // Strava API maximum is 200
		Page:    optional.NewInt32(page),
	}
	var activities []swagger.SummaryActivity
	err := api.do(athleteID, true, func(ctx context.Context) (resp *http.Response, err error) {
		activities, resp, err = api.apiClient.ActivitiesApi.GetLoggedInAthleteActivities(ctx, opts)
		return resp, err
	})
	if err != nil {
		slog.Error("Failed to get activities", "error", err)
		return nil, fmt.Errorf("failed to get activities for page %d: %w", page, err)
	}
	return activities, nil
}

// UpdateActivityDescription writes a new description to a Strava activity.
func (api *StravaAPI) UpdateActivityDescription(activityID int64, athleteID int64, description string) error {
	opts := &swagger.ActivitiesApiUpdateActivityByIdOpts{
		Body: optional.NewInterface(swagger.UpdatableActivity{Description: description}),
	}
	err := api.do(athleteID, false, func(ctx context.Context) (*http.Response, error) {
		_, resp, err := api.apiClient.ActivitiesApi.UpdateActivityById(ctx, activityID, opts)
		return resp, err
	})
	if err != nil {
		return fmt.Errorf("failed to update activity description for ID %d: %w", activityID, err)
	}
	slog.Info("Updated activity description on Strava", "activityID", activityID, "description", description)
	return nil
}

// GetDetailedActivityByID fetches detailed information for a specific activity by its ID.
func (api *StravaAPI) GetDetailedActivityByID(activityID int64, athleteID int64) (*swagger.DetailedActivity, error) {
	slog.Info("Fetching detailed activity info", "activityID", activityID)

	var detailedActivity swagger.DetailedActivity
	err := api.do(athleteID, true, func(ctx context.Context) (resp *http.Response, err error) {
		detailedActivity, resp, err = api.apiClient.ActivitiesApi.GetActivityById(ctx, activityID, nil)
		return resp, err
	})
	if err != nil {
		slog.Error("Failed to get detailed activity", "activityID", activityID, "error", err)
		return nil, fmt.Errorf("failed to get detailed activity for ID %d: %w", activityID, err)
	}
	return &detailedActivity, nil
}
