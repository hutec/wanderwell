package strava

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"wanderwell/backend/config"
	"wanderwell/backend/db"
	"wanderwell/backend/models"

	swagger "wanderwell/backend/client"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twpayne/go-polyline"
)

type CacheUpdater struct {
	db        *pgxpool.Pool
	queries   *db.Queries
	dbMutex   sync.Mutex
	cfg       *config.Config
	stravaAPI *StravaAPI
}

func NewCacheUpdater(pool *pgxpool.Pool, cfg *config.Config, api *StravaAPI) *CacheUpdater {
	return &CacheUpdater{
		db:        pool,
		queries:   db.New(pool),
		cfg:       cfg,
		stravaAPI: api,
	}
}

// getAllUserActivities retrieves all activities for a user from the Strava API by handling pagination
// maxPages limits the number of pages to fetch; if 0, fetch all pages
func (cu *CacheUpdater) GetAllUserActivities(userID int64, maxPages int) ([]swagger.SummaryActivity, error) {
	return cu.stravaAPI.GetAthleteSummaryActivities(userID, maxPages)
}

func (cu *CacheUpdater) GetUserIDs() ([]int64, error) {
	return cu.queries.ListAthleteIDs(context.Background())
}

// UpdateActivityCache fetches all activities for a user and updates the local cache (database)
// by checking for new activities. This is meant to do the initial population of the cache.
// Afterwards, a webhook should be used to get real-time updates.
func (cu *CacheUpdater) UpdateActivityCache(userID int64) error {
	slog.Info("Updating activity cache for user", "userID", userID)

	activities, err := cu.GetAllUserActivities(userID, 0)
	if err != nil {
		return err
	}

	for _, activity := range activities {

		// Skip activities with empty polyline
		if activity.Map_ == nil || activity.Map_.SummaryPolyline == "" {
			slog.Info("Skipping activity with empty polyline", "activityID", activity.Id, "sportType", string(*activity.SportType))
			continue
		}

		// Check if activity already exists in the database and add it if not
		currentName, err := cu.queries.GetRouteName(context.Background(), db.GetRouteNameParams{
			ID:     activity.Id,
			UserID: userID,
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				// Can be a go-routine once rate limiting in concurrent calls is handled
				cu.AddDetailedActivity(activity.Id, userID)
				continue
			}
			slog.Error("Failed to check activity existence", "error", err, "activityID", activity.Id)
			return err
		}
		// Update activity name if it has changed
		if currentName != activity.Name {
			slog.Info("Activity name changed, updating", "activityID", activity.Id, "oldName", currentName, "newName", activity.Name)
			cu.dbMutex.Lock()
			err = cu.queries.UpdateRouteName(context.Background(), db.UpdateRouteNameParams{
				Name:   activity.Name,
				ID:     activity.Id,
				UserID: userID,
			})
			cu.dbMutex.Unlock()
			if err != nil {
				slog.Error("Failed to update activity name", "error", err)
				return err
			}
		}
	}

	return nil
}

// AddDetailedActivity fetches detailed activity information for a given activity ID and athlete ID,
// and adds it to the database.
func (cu *CacheUpdater) AddDetailedActivity(activityID int64, athleteID int64) error {
	detailedActivity, err := cu.stravaAPI.GetDetailedActivityByID(activityID, athleteID)
	if err != nil {
		return err
	}
	bounds, err := computeBounds([]byte(detailedActivity.Map_.Polyline))
	if err != nil {
		return err
	}

	// Skip activities with empty polyline
	if detailedActivity.Map_ == nil || detailedActivity.Map_.Polyline == "" {
		slog.Info("Skipping activity with empty polyline", "activityID", activityID, "sportType", detailedActivity.SportType)
		return nil
	}

	// Decode polyline to WKT geometry
	var wkt *string
	coords, _, decodeErr := polyline.DecodeCoords([]byte(detailedActivity.Map_.Polyline))
	if decodeErr != nil {
		slog.Error("Error decoding polyline for route", "route_id", activityID, "err", decodeErr)
	} else {
		wktValue := models.CoordsToWKT(coords)
		wkt = &wktValue
	}

	cu.dbMutex.Lock()
	defer cu.dbMutex.Unlock()

	err = cu.queries.UpsertRoute(context.Background(), db.UpsertRouteParams{
		ID:             activityID,
		UserID:         detailedActivity.Athlete.Id,
		StartDate:      pgtype.Timestamptz{Time: detailedActivity.StartDate, Valid: true},
		Name:           detailedActivity.Name,
		ElapsedTime:    detailedActivity.ElapsedTime,
		MovingTime:     detailedActivity.MovingTime,
		Distance:       float64(detailedActivity.Distance) / 1000.0,
		AverageSpeed:   float64(detailedActivity.AverageSpeed) * 3.6,
		Elevation:      float64(detailedActivity.TotalElevationGain),
		Bounds:         bounds,
		StGeomfromtext: wkt,
	})
	slog.Info("Upserted activity in cache", "activityID", activityID, "userID", athleteID)

	return err
}

func computeBounds(buf []byte) (string, error) {
	coords, _, _ := polyline.DecodeCoords(buf)
	if len(coords) == 0 {
		return "", errors.New("no coordinates found in polyline")
	}

	minLat, minLng := coords[0][0], coords[0][1]
	maxLat, maxLng := coords[0][0], coords[0][1]
	for _, coord := range coords {
		lat, lng := coord[0], coord[1]
		minLat = min(minLat, lat)
		maxLat = max(maxLat, lat)
		minLng = min(minLng, lng)
		maxLng = max(maxLng, lng)
	}
	return fmt.Sprintf("%f,%f,%f,%f", minLat, minLng, maxLat, maxLng), nil
}
