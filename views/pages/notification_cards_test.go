package pages

import (
	"bytes"
	"database/sql"
	"html"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"github.com/a-h/templ"
)

func TestNotificationCardsProtectCredentials(t *testing.T) {
	for _, role := range []string{"admin", "viewer"} {
		for _, configured := range []bool{false, true} {
			label := role + "/empty"
			if configured {
				label = role + "/configured"
			}
			t.Run(label, func(t *testing.T) {
				checkNotificationCards(t, role, configured)
			})
		}
	}
}

func checkNotificationCards(t *testing.T, role string, configured bool) {
	t.Helper()
	var credential, emails, serverURL string
	if configured {
		credential = "secret-card-value"
		emails = "ops+<team>@example.com"
		serverURL = "https://gotify.example.test/?x=<tag>"
	}
	value := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
	project := db.Project{
		SlackWebhookUrl: value(credential), NotifyEmails: value(emails),
		GotifyUrl: value(serverURL), GotifyToken: value(credential),
		DiscordWebhookUrl: value(credential),
	}
	global := db.GlobalNotification{
		SlackWebhookUrl: project.SlackWebhookUrl, NotifyEmails: project.NotifyEmails,
		GotifyUrl: project.GotifyUrl, GotifyToken: project.GotifyToken,
		DiscordWebhookUrl: project.DiscordWebhookUrl,
	}
	request := auth.SetUser(
		httptest.NewRequest("GET", "/", nil),
		&db.User{Role: role},
	)
	wantUnconfigured := 0
	if !configured {
		wantUnconfigured = 3
		if role == "viewer" {
			wantUnconfigured = 4
		}
	}
	for _, cards := range []templ.Component{NotificationSettingsSection(project), GlobalNotificationCards(global)} {
		var output bytes.Buffer
		if err := cards.Render(request.Context(), &output); err != nil {
			t.Fatal(err)
		}
		body := output.String()
		if strings.Count(body, "not configured") != wantUnconfigured {
			t.Fatal("cards lost configured status display")
		}
		if strings.Contains(body, "secret-card-value") ||
			strings.Contains(body, "<tag>") ||
			strings.Contains(body, "<team>") {
			t.Fatal("cards exposed a credential or unescaped display value")
		}
		checkNotificationCardControls(
			t,
			body,
			role,
			configured,
			emails,
			serverURL,
		)
	}
}

func checkNotificationCardControls(
	t *testing.T, body, role string, configured bool, emails, serverURL string,
) {
	t.Helper()
	if role == "viewer" {
		if strings.Contains(body, "<input") || strings.Contains(body, "<form") {
			t.Fatal("viewer cards exposed write controls")
		}
		if configured && !strings.Contains(body, html.EscapeString(emails)) {
			t.Fatal("viewer cards lost email display")
		}
		return
	}
	if strings.Count(body, `type="password"`) != 3 ||
		strings.Count(body, `name="clear_`) != 3 {
		t.Fatal("writer cards lost blank credential or explicit clear controls")
	}
	for _, field := range []string{"notify_emails", "gotify_url"} {
		if strings.Count(body, `name="`+field+`"`) != 1 {
			t.Fatalf("field %s missing or duplicated", field)
		}
	}
	if !strings.Contains(body, `value="`+html.EscapeString(emails)+`"`) ||
		!strings.Contains(body, `value="`+html.EscapeString(serverURL)+`"`) {
		t.Fatal("writer cards lost escaped display values")
	}
}
