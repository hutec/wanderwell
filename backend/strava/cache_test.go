package strava

import (
	"context"
	"testing"
	"wanderwell/backend/db"
)

type preferencesQueriesStub struct {
	// Other queries are deliberately unsupported: calling one will fail the test.
	cacheQueries
	getUserPreferences func(context.Context, int64) (db.UserPreference, error)
}

func (q *preferencesQueriesStub) GetUserPreferences(ctx context.Context, userID int64) (db.UserPreference, error) {
	return q.getUserPreferences(ctx, userID)
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
