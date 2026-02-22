package strava

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"wanderwell/backend/config"
	"wanderwell/backend/models"

	swagger "wanderwell/backend/client"

	"github.com/twpayne/go-polyline"
)

type CacheUpdater struct {
	db        *sql.DB
	dbMutex   sync.Mutex
	cfg       *config.Config
	stravaAPI *StravaAPI
}

func NewCacheUpdater(db *sql.DB, cfg *config.Config, api *StravaAPI) *CacheUpdater {
	return &CacheUpdater{
		db:        db,
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
	rows, err := cu.db.Query("SELECT id FROM athlete")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}

	return userIDs, rows.Err()
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
		var currentName string
		err := cu.db.QueryRow("SELECT name FROM route WHERE id = $1 and user_id = $2", activity.Id, userID).Scan(&currentName)
		if err != nil {
			if err == sql.ErrNoRows {
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
			_, err = cu.db.Exec("UPDATE route SET name = $1 WHERE id = $2 and user_id = $3", activity.Name, activity.Id, userID)
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

	routePolyline := detailedActivity.Map_.Polyline
	route := &models.Route{
		ID:           detailedActivity.Id,
		UserID:       detailedActivity.Athlete.Id,
		StartDate:    detailedActivity.StartDate,
		Name:         detailedActivity.Name,
		ElapsedTime:  detailedActivity.ElapsedTime,
		MovingTime:   detailedActivity.MovingTime,
		Distance:     float32(detailedActivity.Distance) / 1000.0, // Convert to kilometers
		Route:        &routePolyline,
		AverageSpeed: float32(detailedActivity.AverageSpeed) * 3.6, // Convert to km/h
		Elevation:    float32(detailedActivity.TotalElevationGain),
		Bounds:       bounds,
	}

	cu.dbMutex.Lock()
	defer cu.dbMutex.Unlock()
	exists, err := route.Exists(cu.db)
	if err != nil {
		return err
	}

	if exists {
		err = route.Update(cu.db)
		slog.Info("Updated activity in cache", "activityID", activityID, "userID", athleteID)
	} else {
		err = route.Add(cu.db)
		slog.Info("Added new activity to cache", "activityID", activityID, "userID", athleteID)
	}
	if err != nil {
		return err
	}
	return nil
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
