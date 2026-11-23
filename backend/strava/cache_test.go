package strava

import (
	"context"
	"reflect"
	"testing"
	"time"
	swagger "wanderwell/backend/client"
	"wanderwell/backend/db"

	"github.com/jackc/pgx/v5/pgtype"
)

type preferencesQueriesStub struct {
	// Other queries are deliberately unsupported: calling one will fail the test.
	cacheQueries
	getUserPreferences func(context.Context, int64) (db.UserPreference, error)
}

func (q *preferencesQueriesStub) GetUserPreferences(ctx context.Context, userID int64) (db.UserPreference, error) {
	return q.getUserPreferences(ctx, userID)
}

type routeQueriesStub struct {
	cacheQueries
	upsertRoute func(context.Context, db.UpsertRouteParams) error
}

func (q *routeQueriesStub) UpsertRoute(ctx context.Context, arg db.UpsertRouteParams) error {
	return q.upsertRoute(ctx, arg)
}

type activityAPIStub struct {
	activityAPI
	getDetailedActivity func(int64, int64) (*swagger.DetailedActivity, error)
}

func (api *activityAPIStub) GetDetailedActivityByID(activityID, athleteID int64) (*swagger.DetailedActivity, error) {
	return api.getDetailedActivity(activityID, athleteID)
}

func TestAddDetailedActivity(t *testing.T) {
	const activityID, athleteID int64 = 123, 42
	startDate := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	activity := &swagger.DetailedActivity{
		Id:                 activityID,
		Athlete:            &swagger.MetaAthlete{Id: athleteID},
		Name:               "Morning ride",
		StartDate:          startDate,
		ElapsedTime:        3000,
		MovingTime:         2500,
		Distance:           12500,
		AverageSpeed:       5,
		TotalElevationGain: 120,
		Map_:               &swagger.PolylineMap{Polyline: "_p~iF~ps|U_ulLnnqC_mqNvxq`@"},
	}

	var got db.UpsertRouteParams
	fetches, upserts := 0, 0
	updater := &CacheUpdater{
		stravaAPI: &activityAPIStub{
			getDetailedActivity: func(id, userID int64) (*swagger.DetailedActivity, error) {
				fetches++
				if id != activityID || userID != athleteID {
					t.Fatalf("GetDetailedActivityByID(%d, %d), want (%d, %d)", id, userID, activityID, athleteID)
				}
				return activity, nil
			},
		},
		queries: &routeQueriesStub{
			upsertRoute: func(_ context.Context, arg db.UpsertRouteParams) error {
				upserts++
				got = arg
				return nil
			},
		},
	}

	if err := updater.AddDetailedActivity(activityID, athleteID); err != nil {
		t.Fatal(err)
	}
	if fetches != 1 || upserts != 1 {
		t.Fatalf("got %d fetches and %d upserts, want one each", fetches, upserts)
	}

	wkt := "LINESTRING(-120.200000 38.500000, -120.950000 40.700000, -126.453000 43.252000)"
	want := db.UpsertRouteParams{
		ID:             activityID,
		UserID:         athleteID,
		StartDate:      pgtype.Timestamptz{Time: startDate, Valid: true},
		Name:           "Morning ride",
		ElapsedTime:    3000,
		MovingTime:     2500,
		Distance:       12.5,
		AverageSpeed:   18,
		Elevation:      120,
		Bounds:         "38.500000,-126.453000,43.252000,-120.200000",
		StGeomfromtext: &wkt,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UpsertRoute params = %+v, want %+v", got, want)
	}
}

func TestWriteUniqueDistanceDescriptionDisabled(t *testing.T) {
	const athleteID int64 = 42
	called := false
	updater := &CacheUpdater{
		queries: &preferencesQueriesStub{
			getUserPreferences: func(_ context.Context, userID int64) (db.UserPreference, error) {
				called = true
				if userID != athleteID {
					t.Fatalf("GetUserPreferences userID = %d, want %d", userID, athleteID)
				}
				return db.UserPreference{WriteUniqueDistance: false}, nil
			},
		},
		// No Strava API is needed when description updates are disabled.
	}

	updater.WriteUniqueDistanceDescription(123, athleteID)

	if !called {
		t.Fatal("expected user preferences to be checked")
	}
}
