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
	}

	// Validate required fields
	requiredFields := map[string]string{
		"STRAVA_CLIENT_ID":     cfg.StravaClientID,
		"STRAVA_CLIENT_SECRET": cfg.StravaClientSecret,
		"REDIRECT_URI":         cfg.RedirectURI,
		"WEBHOOK_URI":          cfg.WebhookURI,
		"DATABASE_PATH":        cfg.DatabasePath,
		"SERVER_PORT":          cfg.ServerPort,
	}

	for name, value := range requiredFields {
		if err := validateRequired(name, value); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}
