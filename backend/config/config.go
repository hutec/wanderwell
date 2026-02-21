package config

import (
	"fmt"
	"os"
)

type Config struct {
	StravaClientID     string
	StravaClientSecret string
	RedirectURI        string
	WebhookURI         string
	DatabasePath       string
	ServerPort         string // e.g., ":3000"
	FrontendURL        string // for CORS
	// for goth sessions
	SESSION_SECRET string
	SESSION_KEY    string
}

func validateRequired(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func Load() (*Config, error) {
	cfg := &Config{
		StravaClientID:     os.Getenv("STRAVA_CLIENT_ID"),
		StravaClientSecret: os.Getenv("STRAVA_CLIENT_SECRET"),
		RedirectURI:        os.Getenv("REDIRECT_URI"),
		WebhookURI:         os.Getenv("WEBHOOK_URI"),
		DatabasePath:       os.Getenv("DATABASE_PATH"),
		ServerPort:         os.Getenv("SERVER_PORT"),
		FrontendURL:        os.Getenv("FRONTEND_URL"),
		SESSION_SECRET:     os.Getenv("SESSION_SECRET"),
		SESSION_KEY:        os.Getenv("SESSION_KEY"),
	}

	// Validate required fields
	requiredFields := map[string]string{
		"STRAVA_CLIENT_ID":     cfg.StravaClientID,
		"STRAVA_CLIENT_SECRET": cfg.StravaClientSecret,
		"REDIRECT_URI":         cfg.RedirectURI,
		"WEBHOOK_URI":          cfg.WebhookURI,
		"DATABASE_PATH":        cfg.DatabasePath,
		"SERVER_PORT":          cfg.ServerPort,
		"FRONTEND_URL":         cfg.FrontendURL,
		"SESSION_SECRET":       cfg.SESSION_SECRET,
		"SESSION_KEY":          cfg.SESSION_KEY,
	}

	for name, value := range requiredFields {
		if err := validateRequired(name, value); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}
