package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type notificationCredentialUpdate struct{ name, body, want, wantGotify string }

func TestNotificationCredentialsAreWriteOnly(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		for _, update := range []notificationCredentialUpdate{
			{"omitted destination", `{"notify_emails":"new@example.com"}`, "old", ""},
			{"blank destination", `{"slack_webhook_url":" ","gotify_token":"","discord_webhook_url":""}`, "old", ""},
			{"unchanged destination", `{"gotify_url":"https://gotify.example.test/old"}`, "old", "old"},
			{"trimmed destination", `{"gotify_url":" https://gotify.example.test/old "}`, "old", "old"},
			{"changed destination", `{"gotify_url":"https://gotify.example.test/new"}`, "old", ""},
			{"replace", `{"slack_webhook_url":"https://example.com/new","gotify_token":"new","discord_webhook_url":"https://example.com/new"}`, "new", "new"},
			{"clear", `{"clear_slack_webhook_url":true,"clear_gotify_token":true,"clear_discord_webhook_url":true}`, "", ""},
			{"clear takes precedence", `{"slack_webhook_url":"https://example.com/new","gotify_token":"new","discord_webhook_url":"https://example.com/new","clear_slack_webhook_url":true,"clear_gotify_token":true,"clear_discord_webhook_url":true}`, "", ""},
			{"clear ignores invalid replacement", `{"slack_webhook_url":"ftp://example.com/new","gotify_token":"new","discord_webhook_url":"missing-scheme","clear_slack_webhook_url":true,"clear_gotify_token":true,"clear_discord_webhook_url":true}`, "", ""},
		} {
			t.Run(scope+"/"+update.name, func(t *testing.T) {
				checkNotificationCredentialUpdate(t, scope, update)
			})
		}
	}
}

func checkNotificationCredentialUpdate(
	t *testing.T, scope string, update notificationCredentialUpdate,
) {
	t.Helper()
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
		`{"slack_webhook_url":"https://example.com/old","gotify_url":"https://gotify.example.test/old","gotify_token":"old","discord_webhook_url":"https://example.com/old"}`,
	)
	h.assertStatus(t, initial, http.StatusOK)
	// When: the API receives an omitted, blank, replacement or clear update.
	rec := h.request(t, http.MethodPut, path, token, update.body)
	h.assertStatus(t, rec, http.StatusOK)
	// Then: responses mask credentials, and storage follows the retention rule.
	assertNotificationCredentialResponse(t, rec.Body.Bytes(), update)
	assertStoredNotificationCredentials(t, h, scope, p.ID, update)
	get := h.request(t, http.MethodGet, path, token, "")
	h.assertStatus(t, get, http.StatusOK)
	assertNotificationCredentialResponse(t, get.Body.Bytes(), update)
}

func assertNotificationCredentialResponse(
	t *testing.T, encoded []byte, update notificationCredentialUpdate,
) {
	t.Helper()
	if strings.Contains(string(encoded), "example.com/old") ||
		strings.Contains(string(encoded), "example.com/new") {
		t.Fatal(string(encoded))
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"slack_webhook_url", "gotify_token", "discord_webhook_url"} {
		if body[key] != "" {
			t.Fatalf("secret field %s=%v", key, body[key])
		}
		want := update.want
		if key == "gotify_token" {
			want = update.wantGotify
		}
		if body[key+"_configured"] != (want != "") {
			t.Fatalf("configured %s=%v", key, body)
		}
	}
}

func assertStoredNotificationCredentials(
	t *testing.T, h *testHarness, scope string, projectID int64,
	update notificationCredentialUpdate,
) {
	t.Helper()
	var slack, gotify, discord string
	if scope == "project" {
		stored, err := h.repo.Queries.GetProject(t.Context(), projectID)
		if err != nil {
			t.Fatal(err)
		}
		slack, gotify, discord = stored.SlackWebhookUrl.String, stored.GotifyToken.String, stored.DiscordWebhookUrl.String
	} else {
		stored, err := h.repo.Queries.GetGlobalNotifications(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		slack, gotify, discord = stored.SlackWebhookUrl.String, stored.GotifyToken.String, stored.DiscordWebhookUrl.String
	}
	wantURL := ""
	if update.want != "" {
		wantURL = "https://example.com/" + update.want
	}
	if slack != wantURL || discord != wantURL || gotify != update.wantGotify {
		t.Fatal("stored credentials changed incorrectly")
	}
}
