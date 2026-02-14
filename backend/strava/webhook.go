package strava

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"wanderwell/backend/config"
)

// RegisterWebhook sets up the webhook with Strava API
func RegisterWebhook(cfg *config.Config) error {
	// Retrieve existing webhook subscriptions
	values := url.Values{}
	values.Set("client_id", cfg.StravaClientID)
	values.Set("client_secret", cfg.StravaClientSecret)
	resp, err := http.Get("https://www.strava.com/api/v3/push_subscriptions" + "?" + values.Encode())
	if err != nil {
		return fmt.Errorf("failed to get existing subscriptions: %w", err)
	}
	defer resp.Body.Close()
	slog.Info("Existing webhook subscriptions retrieved", "status", resp.Status, "statusCode", resp.StatusCode)

	// TODO: Check if webhook is already registered

	// Register new webhook subscription
	values = url.Values{}
	values.Set("client_id", cfg.StravaClientID)
	values.Set("client_secret", cfg.StravaClientSecret)
	values.Set("callback_url", cfg.WebhookURI)
	values.Set("verify_token", "STRAVA")

	resp, err = http.PostForm("https://www.strava.com/api/v3/push_subscriptions", values)
	if err != nil || resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to register webhook: %w", err)
	}
	defer resp.Body.Close()
	slog.Info("Webhook registered successfully", "status", resp.Status, "statusCode", resp.StatusCode)
	return nil
}
