package main

import (
	"context"
	_ "embed"
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

//go:embed db/schema.sql
var schemaSQL string

func ensureSchema(pool *pgxpool.Pool) error {
	_, err := pool.Exec(context.Background(), schemaSQL)
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

	if err := api.NewServer(db, cacheUpdater, cfg.FrontendURL, cfg.VerifyToken, cfg.TileCacheURL, cfg.AdminUserID).Start(cfg.ServerPort); err != nil {
		slog.Error("Error starting server", "err", err)
	}
}
