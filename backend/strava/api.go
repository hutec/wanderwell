package strava

import (
	"context"
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

// tokenQueries contains the database operations used for Strava authentication.
type tokenQueries interface {
	GetAthleteTokens(ctx context.Context, id int64) (db.GetAthleteTokensRow, error)
	UpdateAthleteTokens(ctx context.Context, arg db.UpdateAthleteTokensParams) error
}

var _ tokenQueries = (*db.Queries)(nil)

// StravaAPI handles authentication and schedules individual activity requests.
// Share one instance across athletes to share application-wide quotas. It must
// be constructed with NewStravaAPI, shut down before closing the DB, and not copied.
type StravaAPI struct {
	queries   tokenQueries
	tokenMu   sync.Mutex // Protects the entire token lookup/refresh/update sequence.
	cfg       *config.Config
	apiClient *swagger.APIClient
	scheduler *RequestScheduler
	gate      *RateLimitGate
}

// NewStravaAPI owns four workers and room for 64 pending requests. Quotas start
// at Strava's default overall 200/2000 and read 100/1000 per quarter-hour/day,
// then reconcile with response headers. Zero initial usage is only a local
// startup estimate: restarting does not reset server quotas; headers and 429s
// reconcile usage and impose cooldowns.
func NewStravaAPI(pool *pgxpool.Pool, cfg *config.Config) (*StravaAPI, error) {
	gate, err := NewRateLimitGate(RateLimitGateConfig{
		Overall: RateLimitGateQuota{QuarterHourLimit: 200, DailyLimit: 2000},
		Read:    RateLimitGateQuota{QuarterHourLimit: 100, DailyLimit: 1000},
	})
	if err != nil {
		return nil, err
	}
	scheduler, err := NewRequestScheduler(4, 64)
	if err != nil {
		return nil, err
	}
	return &StravaAPI{
		queries:   db.New(pool),
		cfg:       cfg,
		apiClient: swagger.NewAPIClient(swagger.NewConfiguration()),
		scheduler: scheduler,
		gate:      gate,
	}, nil
}

// Shutdown cancels queued/active activity requests and waits for the workers.
// It is safe to call repeatedly. The database remains owned by the caller.
func (api *StravaAPI) Shutdown() {
	api.scheduler.Shutdown()
}

// GetAthleteAccessToken retrieves the access token for a given athlete.
// If the token is expired it is automatically refreshed. Activity requests call
// this only after gate admission, on every attempt. OAuth refresh is separate
// authentication traffic and must not be nested into the activity worker pool.
func (api *StravaAPI) GetAthleteAccessToken(ctx context.Context, athleteID int64) (string, error) {
	// Serialize refreshes so a concurrent request reads the newly persisted
	// credentials rather than refreshing the same rotating token twice.
	api.tokenMu.Lock()
	defer api.tokenMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	row, err := api.queries.GetAthleteTokens(ctx, athleteID)
	if err != nil {
		return "", err
	}
	accessToken := row.AccessToken.String
	if row.ExpiresAt.Int64 <= time.Now().Unix() {
		slog.Info("Refreshing token for user", "userID", athleteID)
		tokenResp, err := refreshToken(ctx, row.RefreshToken.String, api.cfg.StravaClientID, api.cfg.StravaClientSecret)
		if err != nil {
			slog.Error("Failed to refresh token", "error", err)
			return "", err
		}
		accessToken = tokenResp.AccessToken
		err = api.queries.UpdateAthleteTokens(ctx, db.UpdateAthleteTokensParams{
			ID:           athleteID,
			AccessToken:  pgtype.Text{String: accessToken, Valid: true},
			RefreshToken: pgtype.Text{String: tokenResp.RefreshToken, Valid: true},
			ExpiresAt:    pgtype.Int8{Int64: tokenResp.ExpiresAt, Valid: true},
		})
		if err != nil {
			slog.Error("Failed to update user", "error", err)
			return "", err
		}
	}
	return accessToken, nil
}

// GetAthleteSummaryActivities fetches all summary activities for a given athlete.
// maxPages limits the number of pages to fetch; if 0, fetch all pages.
// Each page is scheduled separately, never the entire import.
func (api *StravaAPI) GetAthleteSummaryActivities(ctx context.Context, athleteID int64, maxPages int) ([]swagger.SummaryActivity, error) {
	slog.Info("Getting all activities for user", "athleteID", athleteID)

	var allActivities []swagger.SummaryActivity
	page := int32(1)
	for {
		activities, err := api.GetAthleteSummaryActivitiesByPage(ctx, athleteID, page)
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

func (api *StravaAPI) GetAthleteSummaryActivitiesByPage(ctx context.Context, athleteID int64, page int32) ([]swagger.SummaryActivity, error) {
	slog.Info("Getting activities for athlete by page", "athleteID", athleteID, "page", page)
	return ExecuteStravaRequest(ctx, api.scheduler, api.gate, RequestRead, func(ctx context.Context) ([]swagger.SummaryActivity, *http.Response, error) {
		accessToken, err := api.GetAthleteAccessToken(ctx, athleteID)
		if err != nil {
			return nil, nil, err
		}
		opts := &swagger.ActivitiesApiGetLoggedInAthleteActivitiesOpts{
			PerPage: optional.NewInt32(200), // Strava API maximum is 200
			Page:    optional.NewInt32(page),
		}
		ctx = context.WithValue(ctx, swagger.ContextAccessToken, accessToken)
		activities, resp, err := api.apiClient.ActivitiesApi.GetLoggedInAthleteActivities(ctx, opts)
		if err != nil {
			return nil, resp, fmt.Errorf("failed to get activities for page %d: %w", page, err)
		}
		if resp == nil {
			return nil, resp, fmt.Errorf("no response from Strava for activities page %d", page)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, resp, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
		}
		return activities, resp, nil
	})
}

// UpdateActivityDescription writes a new description to a Strava activity.
func (api *StravaAPI) UpdateActivityDescription(ctx context.Context, activityID int64, athleteID int64, description string) error {
	_, err := ExecuteStravaRequest(ctx, api.scheduler, api.gate, RequestWrite, func(ctx context.Context) (swagger.DetailedActivity, *http.Response, error) {
		accessToken, err := api.GetAthleteAccessToken(ctx, athleteID)
		if err != nil {
			return swagger.DetailedActivity{}, nil, err
		}
		ctx = context.WithValue(ctx, swagger.ContextAccessToken, accessToken)
		opts := &swagger.ActivitiesApiUpdateActivityByIdOpts{
			Body: optional.NewInterface(swagger.UpdatableActivity{Description: description}),
		}
		activity, resp, err := api.apiClient.ActivitiesApi.UpdateActivityById(ctx, activityID, opts)
		if err != nil {
			return activity, resp, fmt.Errorf("failed to update activity description for ID %d: %w", activityID, err)
		}
		if resp == nil {
			return activity, resp, fmt.Errorf("no response from Strava for activity ID %d", activityID)
		}
		if resp.StatusCode != http.StatusOK {
			return activity, resp, fmt.Errorf("update activity description returned status %d for ID %d", resp.StatusCode, activityID)
		}
		return activity, resp, nil
	})
	if err != nil {
		return err
	}
	slog.Info("Updated activity description on Strava", "activityID", activityID, "description", description)
	return nil
}

// GetDetailedActivityByID fetches detailed information for a specific activity by its ID.
func (api *StravaAPI) GetDetailedActivityByID(ctx context.Context, activityID int64, athleteID int64) (*swagger.DetailedActivity, error) {
	slog.Info("Fetching detailed activity info", "activityID", activityID)
	return ExecuteStravaRequest(ctx, api.scheduler, api.gate, RequestRead, func(ctx context.Context) (*swagger.DetailedActivity, *http.Response, error) {
		accessToken, err := api.GetAthleteAccessToken(ctx, athleteID)
		if err != nil {
			return nil, nil, err
		}
		ctx = context.WithValue(ctx, swagger.ContextAccessToken, accessToken)
		activity, resp, err := api.apiClient.ActivitiesApi.GetActivityById(ctx, activityID, nil)
		if err != nil {
			return nil, resp, fmt.Errorf("failed to get detailed activity for ID %d: %w", activityID, err)
		}
		if resp == nil {
			return nil, resp, fmt.Errorf("no response from Strava for detailed activity ID %d", activityID)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, resp, fmt.Errorf("API request for detailed activity failed with status: %d", resp.StatusCode)
		}
		return &activity, resp, nil
	})
}
