package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/notify"
)

// NotificationSettingsInput contains caller-normalized notification settings.
type NotificationSettingsInput struct {
	SlackWebhookURL        string
	NotifyEmails           string
	GotifyURL              string
	GotifyToken            string
	DiscordWebhookURL      string
	ClearSlackWebhookURL   bool
	ClearGotifyToken       bool
	ClearDiscordWebhookURL bool
}

// PrepareNotificationSettings validates endpoints after applying clear flags.
func PrepareNotificationSettings(
	input NotificationSettingsInput,
) (db.UpdateGlobalNotificationsParams, error) {
	slack := notify.CredentialValue(
		input.SlackWebhookURL, input.ClearSlackWebhookURL,
	)
	discord := notify.CredentialValue(
		input.DiscordWebhookURL, input.ClearDiscordWebhookURL,
	)
	for _, raw := range []string{slack, input.GotifyURL, discord} {
		if err := notify.ValidateEndpointURL(raw); err != nil {
			return db.UpdateGlobalNotificationsParams{}, err
		}
	}
	params := db.UpdateGlobalNotificationsParams{
		SlackWebhookUrl: sql.NullString{String: slack, Valid: slack != ""},
		NotifyEmails: sql.NullString{
			String: input.NotifyEmails, Valid: input.NotifyEmails != "",
		},
		GotifyUrl: sql.NullString{
			String: input.GotifyURL, Valid: input.GotifyURL != "",
		},
		GotifyToken: sql.NullString{
			String: input.GotifyToken, Valid: input.GotifyToken != "",
		},
		DiscordWebhookUrl: sql.NullString{
			String: discord, Valid: discord != "",
		},
	}
	if input.ClearSlackWebhookURL {
		params.ClearSlackWebhookUrl = 1
	}
	if input.ClearGotifyToken {
		params.ClearGotifyToken = 1
	}
	if input.ClearDiscordWebhookURL {
		params.ClearDiscordWebhookUrl = 1
	}
	return params, nil
}

func notificationClearFlag(r *http.Request, name string) int64 {
	if r.FormValue(name) == "on" {
		return 1
	}
	return 0
}

func notificationFormInput(r *http.Request) NotificationSettingsInput {
	return NotificationSettingsInput{
		SlackWebhookURL: r.FormValue("slack_webhook_url"),
		NotifyEmails: strings.TrimSpace(
			r.FormValue("notify_emails"),
		),
		GotifyURL: strings.TrimSpace(
			r.FormValue("gotify_url"),
		),
		GotifyToken: strings.TrimSpace(
			r.FormValue("gotify_token"),
		),
		DiscordWebhookURL: r.FormValue("discord_webhook_url"),
		ClearSlackWebhookURL: notificationClearFlag(
			r,
			"clear_slack_webhook_url",
		) == 1,
		ClearGotifyToken: notificationClearFlag(
			r,
			"clear_gotify_token",
		) == 1,
		ClearDiscordWebhookURL: notificationClearFlag(
			r,
			"clear_discord_webhook_url",
		) == 1,
	}
}
