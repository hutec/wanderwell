package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"wanderwell/backend/api"
	"wanderwell/backend/config"
	"wanderwell/backend/strava"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/markbates/goth"
	gothstrava "github.com/markbates/goth/providers/strava"

	"github.com/joho/godotenv"
)

func initDB(databasePath string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), databasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

func ensureSchema(pool *pgxpool.Pool) error {
	schema := `
		CREATE EXTENSION IF NOT EXISTS postgis;

		CREATE TABLE IF NOT EXISTS athlete (
			id BIGINT PRIMARY KEY,
			firstname TEXT,
			lastname TEXT,
			expires_at INTEGER,
			refresh_token TEXT,
			access_token TEXT
		);
		CREATE TABLE IF NOT EXISTS route (
    		id            BIGINT PRIMARY KEY,
    		user_id       BIGINT NOT NULL,
    		start_date    TIMESTAMPTZ NOT NULL,
    		name          VARCHAR NOT NULL,
    		elapsed_time  INTEGER NOT NULL,
    		moving_time   INTEGER NOT NULL,
    		distance      FLOAT NOT NULL,
    		average_speed FLOAT NOT NULL,
    		elevation     FLOAT NOT NULL,
    		bounds        TEXT NOT NULL,
    		sport_type    VARCHAR,
    		geom          geometry(LineString, 4326),
    		FOREIGN KEY (user_id) REFERENCES athlete(id)
		);

		-- Create spatial index
        CREATE INDEX IF NOT EXISTS route_geom_idx ON route USING GIST (geom);

        -- Create MVT function for user routes
        CREATE OR REPLACE FUNCTION user_routes(z int, x int, y int, query_params json)
		RETURNS bytea AS $$
		DECLARE
		  mvt bytea;
		  uid bigint;
		BEGIN
		  uid := (query_params->>'user_id')::bigint;

		  SELECT INTO mvt ST_AsMVT(tile, 'user_routes', 4096, 'geom')
		  FROM (
		    SELECT
		      id,
		      name,
		      sport_type,
		      distance,
		      start_date,
		      ST_AsMVTGeom(
		        ST_Transform(geom, 3857),
		        ST_TileEnvelope(z, x, y),
		        4096, 64, true
		      ) AS geom
		    FROM route
		    WHERE user_id = uid AND geom && ST_Transform(ST_TileEnvelope(z, x, y), 4326)
		  ) tile;

		  RETURN mvt;
		END;
		$$ LANGUAGE plpgsql STABLE PARALLEL SAFE;
		`

	_, err := pool.Exec(context.Background(), schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	slog.Info("DB schema ensured")
	return nil
}

func main() {

	file, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}

	mw := io.MultiWriter(os.Stdout, file)
	handler := slog.NewTextHandler(mw, nil)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	err = godotenv.Load()
	if err != nil {
		slog.Error("No .env file found or error loading .env file, proceeding with environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Error loading config from .env file", "err", err)
	}

	db, err := initDB(cfg.DatabasePath)
	if err != nil {
		slog.Error("Error initializing database", "err", err)
	}
	defer db.Close()

	if err := ensureSchema(db); err != nil {
		slog.Error("Error ensuring database schema", "err", err)
	}

	stravaApi := strava.NewStravaAPI(db, cfg)
	cacheUpdater := strava.NewCacheUpdater(db, cfg, stravaApi)

	scope := "read,activity:read_all,profile:read_all"
	goth.UseProviders(
		gothstrava.New(cfg.StravaClientID, cfg.StravaClientSecret, cfg.RedirectURI, scope),
	)

	if err := api.NewServer(db, cacheUpdater, cfg.FrontendURL, cfg.VerifyToken, cfg.TileCacheURL).Start(cfg.ServerPort); err != nil {
		slog.Error("Error starting server", "err", err)
	}
}
