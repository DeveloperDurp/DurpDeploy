//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestNotificationSettingsPreparationAPIWebE2E(t *testing.T) {
	for _, surface := range []string{
		"project-api", "project-web", "global-api", "global-web",
	} {
		t.Run(surface, func(t *testing.T) {
			checkNotificationSettingsPreparation(t, surface)
		})
	}
}

func checkNotificationSettingsPreparation(t *testing.T, surface string) {
	t.Helper()
	f := newArtifactE2E(t)
	apiPath := f.base() + "/notifications"
	webPath := fmt.Sprintf("/projects/%d/notifications", f.project.ID)
	table := "projects"
	if strings.HasPrefix(surface, "global") {
		apiPath = "/api/v1/admin/notifications/settings"
		webPath = "/admin/notifications/settings"
		table = "global_notifications"
	}
	update := func(values map[string]string, status int) {
		t.Helper()
		if strings.HasSuffix(surface, "web") {
			form := url.Values{}
			for key, value := range values {
				form.Set(key, value)
			}
			if status == 200 {
				status = 303
			}
			f.web(t, "POST", webPath, form, status)
			return
		}
		f.api(t, "PUT", apiPath, values, status)
	}
	read := func() map[string]any {
		t.Helper()
		var settings map[string]any
		if err := json.Unmarshal(
			f.api(t, "GET", apiPath, nil, 200), &settings,
		); err != nil {
			t.Fatal(err)
		}
		return settings
	}
	// Given: each transport receives whitespace around replacement values.
	values := map[string]string{
		"slack_webhook_url":   " https://example.com/slack ",
		"notify_emails":       " operator@example.test ",
		"gotify_url":          " https://example.com/gotify ",
		"gotify_token":        " synthetic-token ",
		"discord_webhook_url": " https://example.com/discord ",
	}
	update(values, 200)
	// Then: only the global API preserves raw emails and Gotify tokens.
	wantEmails := assertNotificationReplacement(t, f, surface, table, values)
	saved := read()
	if saved["notify_emails"] != wantEmails ||
		saved["gotify_url"] != "https://example.com/gotify" ||
		saved["gotify_token"] != "" ||
		saved["gotify_token_configured"] != true {
		t.Fatal(
			"notification response did not preserve masking or normalization",
		)
	}
	// When: validation rejects a new destination, storage remains unchanged.
	values["gotify_url"] = "http://127.0.0.1/blocked"
	invalidStatus := 400
	if strings.HasSuffix(surface, "web") {
		invalidStatus = 422
	}
	update(values, invalidStatus)
	if !reflect.DeepEqual(read(), saved) {
		t.Fatal("invalid notification update changed saved settings")
	}
	// When: persistence fails, all four handlers report failure without mutation.
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"CREATE TRIGGER reject_notification_update BEFORE UPDATE ON "+table+
			" BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END",
	); err != nil {
		t.Fatal(err)
	}
	values["gotify_url"] = "https://example.com/replacement"
	update(values, 500)
	if !reflect.DeepEqual(read(), saved) {
		t.Fatal("failed notification update changed saved settings")
	}
}

func assertNotificationReplacement(
	t *testing.T, f *artifactE2E, surface, table string,
	values map[string]string,
) string {
	t.Helper()
	wantEmails, wantToken := "operator@example.test", "synthetic-token"
	if surface == "global-api" {
		wantEmails, wantToken = values["notify_emails"], values["gotify_token"]
	}
	var emails, token, slack, discord string
	query := "SELECT notify_emails, gotify_token, slack_webhook_url, " +
		"discord_webhook_url FROM " + table + " WHERE id = ?"
	id := f.project.ID
	if table == "global_notifications" {
		id = 1
	}
	if err := f.h.repo.DB.QueryRowContext(t.Context(), query, id).
		Scan(&emails, &token, &slack, &discord); err != nil {
		t.Fatal(err)
	}
	if emails != wantEmails || token != wantToken ||
		slack != "https://example.com/slack" ||
		discord != "https://example.com/discord" {
		t.Fatal("notification replacement normalization changed")
	}
	return wantEmails
}
