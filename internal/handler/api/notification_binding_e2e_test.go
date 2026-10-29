//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/notify"
)

type gotifyBindingDelivery struct{ path, token string }

func TestGotifyDestinationBindingAPIWebE2E(t *testing.T) {
	for _, surface := range []string{
		"project-api", "project-web", "scoped-maintainer", "global-api", "global-web",
	} {
		t.Run(surface, func(t *testing.T) {
			checkGotifyDestinationBindingE2E(t, surface)
		})
	}
	t.Run("permissions", checkGotifyBindingPermissions)
}

func checkGotifyDestinationBindingE2E(t *testing.T, surface string) {
	t.Helper()
	f := newArtifactE2E(t, fakePodman(t))
	writer := *f
	apiPath := f.base() + "/notifications"
	webPath := fmt.Sprintf("/projects/%d/notifications", f.project.ID)
	global := strings.HasPrefix(surface, "global")
	if global {
		apiPath = "/api/v1/admin/notifications/settings"
		webPath = "/admin/notifications/settings"
	}
	if surface == "scoped-maintainer" {
		writer.token = mintProjectToken(t, f, "maintainer", f.project.ID).ID
	}
	if surface == "project-web" {
		user := seedAPIUser(
			t,
			f.h.repo,
			"gotify-maintainer@example.test",
			"maintainer",
		)
		if err := f.h.repo.Queries.AddProjectMember(
			t.Context(),
			db.AddProjectMemberParams{
				ProjectID: f.project.ID, UserID: user.ID, Role: "maintainer",
			},
		); err != nil {
			t.Fatal(err)
		}
		writer.session = memberRoleSession(t, f.h, user.ID)
		writer.csrf = "member-role-csrf"
	}
	update := func(values map[string]any, invalid bool) {
		t.Helper()
		updateGotifyBinding(
			t,
			&writer,
			apiPath,
			webPath,
			surface,
			values,
			invalid,
		)
	}
	deliveries := make(chan gotifyBindingDelivery, 32)
	client := notificationFixtureClient(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deliveries <- gotifyBindingDelivery{r.URL.Path, r.URL.Query().Get("token")}
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	bus := events.NewBus(f.h.repo)
	bus.Register(notify.NewGotifyNotifierWithClient(client))
	bus.Register(artifactNotifier{f.done})
	f.h.runner.SetEventBus(bus)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "binding check", "script_body": "echo binding-ok",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "gotify-binding"}, 201), &release)
	checkDelivery := func(path, token string) {
		t.Helper()
		assertGotifyBindingMask(t, f.api(t, "GET", apiPath, nil, 200), token)
		count := publishGotifyBindingEvent(t, f, bus, release, global)
		assertGotifyBindingDeliveries(t, deliveries, path, token, count)
	}
	const first = "http://notification.example.test/a"
	const second = "http://notification.example.test/b"
	update(
		map[string]any{"gotify_url": first, "gotify_token": "binding-token-a"},
		false,
	)
	checkDelivery("/a", "binding-token-a")
	update(map[string]any{"gotify_url": first}, false)
	checkDelivery("/a", "binding-token-a")
	// Clearing a token does not bypass validation or change saved settings on failure.
	update(
		map[string]any{
			"gotify_url":         "http://127.0.0.1/forbidden",
			"clear_gotify_token": true,
		},
		true,
	)
	checkDelivery("/a", "binding-token-a")
	update(map[string]any{"gotify_url": second}, false)
	checkDelivery("/b", "")
	update(
		map[string]any{"gotify_url": second, "gotify_token": "binding-token-b"},
		false,
	)
	checkDelivery("/b", "binding-token-b")
	update(map[string]any{"gotify_url": ""}, false)
	checkDelivery("", "")
	update(map[string]any{"gotify_url": second}, false)
	checkDelivery("/b", "")
	update(
		map[string]any{
			"gotify_url":         second,
			"gotify_token":       "binding-token-a",
			"clear_gotify_token": true,
		},
		false,
	)
	checkDelivery("/b", "")
	update(map[string]any{"gotify_url": second, "gotify_token": " "}, false)
	want := ""
	if surface == "global-api" {
		want = " " // The global API preserves raw token values.
	}
	checkDelivery("/b", want)
	var history []db.ListNotificationEventsRow
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/admin/notifications", nil, 200),
		&history,
	)
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "binding-token-") ||
		strings.Contains(
			f.web(t, "GET", "/admin/notifications", nil, 200),
			"binding-token-",
		) {
		t.Fatal("notification history exposed a token")
	}
}

func updateGotifyBinding(
	t *testing.T, writer *artifactE2E, apiPath, webPath, surface string,
	values map[string]any, invalid bool,
) {
	t.Helper()
	if strings.HasSuffix(surface, "web") {
		form := url.Values{}
		for key, value := range values {
			if flag, ok := value.(bool); ok {
				if flag {
					form.Set(key, "on")
				}
				continue
			}
			form.Set(key, fmt.Sprint(value))
		}
		status := http.StatusSeeOther
		if invalid {
			status = http.StatusUnprocessableEntity
		}
		writer.web(t, "POST", webPath, form, status)
		return
	}
	status := http.StatusOK
	if invalid {
		status = http.StatusBadRequest
	}
	writer.api(t, "PUT", apiPath, values, status)
}

func assertGotifyBindingMask(t *testing.T, body []byte, token string) {
	t.Helper()
	var settings map[string]any
	decodeStepLogTest(t, body, &settings)
	if settings["gotify_token"] != "" ||
		settings["gotify_token_configured"] != (token != "") ||
		strings.Contains(string(body), "binding-token-") {
		t.Fatal(
			"Gotify response lost masking or reported incorrect credential state",
		)
	}
}

func publishGotifyBindingEvent(
	t *testing.T,
	f *artifactE2E,
	bus *events.Bus,
	release db.Release,
	global bool,
) int {
	t.Helper()
	if global {
		bus.Publish(t.Context(), events.Event{
			Type:    events.BackupUnhealthy,
			Message: "gotify-binding-system-event",
		})
		return 1
	}
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{"release_id": release.ID,
			"environment_id": f.environment.ID}, 201), &deployment)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	return 2
}

func assertGotifyBindingDeliveries(
	t *testing.T, deliveries <-chan gotifyBindingDelivery,
	path, token string, count int,
) {
	t.Helper()
	if token == "" {
		count = 0
	}
	if len(deliveries) != count {
		t.Fatalf("Gotify delivery count=%d, want=%d", len(deliveries), count)
	}
	for range count {
		got := <-deliveries
		if got.path != path+"/message" || got.token != token {
			t.Fatal("Gotify sent a credential to the wrong destination")
		}
	}
}

func checkGotifyBindingPermissions(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	path := f.base() + "/notifications"
	f.api(t, "PUT", path, map[string]string{
		"gotify_url":   "https://notification.example.test/a",
		"gotify_token": "binding-token-a",
	}, 200)
	other := seedProject(t, f.h.repo)
	for _, role := range []string{"viewer", "deployer", "maintainer"} {
		token := mintProjectToken(t, f, role, f.project.ID)
		writer := *f
		writer.token = token.ID
		body := map[string]string{
			"gotify_url": "https://notification.example.test/b",
		}
		if role != "maintainer" {
			writer.api(t, "PUT", path, body, 403)
		}
		writer.api(
			t,
			"PUT",
			fmt.Sprintf("/api/v1/projects/%d/notifications", other.ID),
			body,
			http.StatusForbidden,
		)
		writer.api(t, "PUT", "/api/v1/admin/notifications/settings", body, 403)
	}
	var saved map[string]any
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &saved)
	if saved["gotify_url"] != "https://notification.example.test/a" ||
		saved["gotify_token_configured"] != true {
		t.Fatal("forbidden notification update changed saved settings")
	}
}
