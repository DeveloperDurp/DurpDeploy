package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNotificationCredentialsAreWriteOnly(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		for _, update := range []struct{ name, body, want string }{
			{"omitted", `{"notify_emails":"new@example.com"}`, "old"},
			{"blank", `{"slack_webhook_url":" ","gotify_token":"","discord_webhook_url":""}`, "old"},
			{"replace", `{"slack_webhook_url":"https://example.com/new","gotify_token":"new","discord_webhook_url":"https://example.com/new"}`, "new"},
			{"clear", `{"clear_slack_webhook_url":true,"clear_gotify_token":true,"clear_discord_webhook_url":true}`, ""},
			{"clear takes precedence", `{"slack_webhook_url":"https://example.com/new","gotify_token":"new","discord_webhook_url":"https://example.com/new","clear_slack_webhook_url":true,"clear_gotify_token":true,"clear_discord_webhook_url":true}`, ""},
			{"clear ignores invalid replacement", `{"slack_webhook_url":"ftp://example.com/new","gotify_token":"new","discord_webhook_url":"missing-scheme","clear_slack_webhook_url":true,"clear_gotify_token":true,"clear_discord_webhook_url":true}`, ""},
		} {
			t.Run(scope+"/"+update.name, func(t *testing.T) {
				// Given: configured credentials and an administrator who can replace them.
				h := newHarness(t)
				admin := h.seedUser(t, "secrets@example.com", "admin")
				token := h.seedToken(t, admin)
				p := h.seedProject(t, admin)
				path := "/api/v1/projects/" + itoa(p.ID) + "/notifications"
				if scope == "global" {
					path = "/api/v1/admin/notifications/settings"
				}
				initial := h.request(
					t,
					http.MethodPut,
					path,
					token,
					`{"slack_webhook_url":"https://example.com/old","gotify_token":"old","discord_webhook_url":"https://example.com/old"}`,
				)
				h.assertStatus(t, initial, http.StatusOK)
				// When: the API receives an omitted, blank, replacement or clear update.
				rec := h.request(t, http.MethodPut, path, token, update.body)
				h.assertStatus(t, rec, http.StatusOK)
				// Then: writes never return plaintext, and configured status reflects storage.
				if strings.Contains(rec.Body.String(), "example.com/old") ||
					strings.Contains(rec.Body.String(), "example.com/new") {
					t.Fatal(rec.Body.String())
				}
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"slack_webhook_url", "gotify_token", "discord_webhook_url"} {
					if body[key] != "" {
						t.Fatalf("secret field %s=%v", key, body[key])
					}
					if body[key+"_configured"] != (update.want != "") {
						t.Fatalf("configured %s=%v", key, body)
					}
				}
				var slack, gotify, discord string
				if scope == "project" {
					stored, err := h.repo.Queries.GetProject(t.Context(), p.ID)
					if err != nil {
						t.Fatal(err)
					}
					slack, gotify, discord = stored.SlackWebhookUrl.String, stored.GotifyToken.String, stored.DiscordWebhookUrl.String
				} else {
					stored, err := h.repo.Queries.GetGlobalNotifications(
						t.Context(),
					)
					if err != nil {
						t.Fatal(err)
					}
					slack, gotify, discord = stored.SlackWebhookUrl.String, stored.GotifyToken.String, stored.DiscordWebhookUrl.String
				}
				wantURL := ""
				if update.want != "" {
					wantURL = "https://example.com/" + update.want
				}
				if slack != wantURL || discord != wantURL ||
					gotify != update.want {
					t.Fatalf("stored credentials changed incorrectly")
				}
				get := h.request(t, http.MethodGet, path, token, "")
				h.assertStatus(t, get, http.StatusOK)
				if strings.Contains(get.Body.String(), "example.com/old") ||
					strings.Contains(get.Body.String(), "example.com/new") {
					t.Fatal(get.Body.String())
				}
			})
		}
	}
}
