package main

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"wanderwell/backend/api"
	"wanderwell/backend/config"
	"wanderwell/backend/strava"

	"github.com/markbates/goth"
	gothstrava "github.com/markbates/goth/providers/strava"

	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"
)

func initDB(databasePath string) (*sql.DB, error) {
	var err error
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		slog.Error("Failed to open database", "err", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

func ensureSchema(db *sql.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS user (
			id INTEGER PRIMARY KEY,
			firstname TEXT,
			lastname TEXT,
			expires_at INTEGER,
			refresh_token TEXT,
			access_token TEXT
		);
		CREATE TABLE IF NOT EXISTS route (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			start_date DATETIME,
			name VARCHAR,
			elapsed_time INTEGER,
			moving_time INTEGER,
			distance FLOAT,
			average_speed FLOAT,
			route VARCHAR,
			elevation FLOAT,
			bounds TEXT,
			FOREIGN KEY(user_id) REFERENCES user(id)
		);
		`

	_, err := db.Exec(schema)
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

	if err := api.NewServer(db, cacheUpdater).Start(cfg.ServerPort); err != nil {
		slog.Error("Error starting server", "err", err)
	}
}
