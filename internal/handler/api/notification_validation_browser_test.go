//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestNotificationValidationPreservesFormBrowserE2E(t *testing.T) {
	// Given: both scopes have saved settings and masked credentials.
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, scope := range []struct{ name, api, web string }{
		{"project", f.base() + "/notifications",
			fmt.Sprintf("/projects/%d/notifications", f.project.ID)},
		{"global", "/api/v1/admin/notifications/settings",
			"/admin/notifications/settings"},
	} {
		t.Run(scope.name, func(t *testing.T) {
			f.api(t, "PUT", scope.api, map[string]string{
				"notify_emails": "saved@example.com",
				"gotify_url":    "https://example.com/gotify",
				"gotify_token":  "saved-token",
			}, 200)
			var saved map[string]any
			if err := json.Unmarshal(
				f.api(t, "GET", scope.api, nil, 200), &saved,
			); err != nil {
				t.Fatal(err)
			}
			b.navigateBackTest(t, f.baseURL+scope.web)
			for _, invalid := range []struct{ name, field, value string }{
				{"missing scheme", "gotify_url", "example.com/gotify"},
				{"unsupported scheme", "gotify_url", "ftp://example.com/gotify"},
				{"Slack", "slack_webhook_url", "ftp://example.com/slack"},
				{"Discord", "discord_webhook_url", "ftp://example.com/discord"},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					b.evaluate(t, fmt.Sprintf(`(() => {
						window.notificationForm = document.querySelector('[name=notify_emails]').form;
						window.notificationStatus = 0;
						window.notificationForm.addEventListener('htmx:afterRequest', e => window.notificationStatus = e.detail.xhr.status, {once: true});
						for (const name of ['slack_webhook_url','discord_webhook_url']) document.querySelector('[name='+name+']').value = '';
						document.querySelector('[name=gotify_url]').value = 'https://example.com/gotify';
						document.querySelector('[name=notify_emails]').value = 'draft@example.com';
						document.querySelector('[name=gotify_token]').value = 'draft-token';
						document.querySelector('[name=%s]').value = %q;
						return true;
					})()`, invalid.field, invalid.value))
					// When: the administrator clicks Save with an invalid endpoint.
					clickNotificationSave(t, b)
					b.wait(t, `window.notificationStatus === 422`)
					// Then: the error toast leaves the same form and draft intact.
					b.wait(
						t,
						`Array.from(document.querySelectorAll('[x-data="toast"]')).some(e => getComputedStyle(e).display !== 'none' && e.querySelector('.alert-error') && e.innerText.trim().length > 0 && !e.querySelector('.alert-success'))`,
					)
					if string(b.evaluate(t, fmt.Sprintf(`
						window.notificationForm === document.querySelector('[name=notify_emails]').form &&
						document.querySelector('[name=notify_emails]').value === 'draft@example.com' &&
						document.querySelector('[name=gotify_token]').value === 'draft-token' &&
						document.querySelector('[name=%s]').value === %q &&
						location.pathname === %q`, invalid.field, invalid.value, scope.web))) != "true" {
						t.Fatal(
							"invalid notification submission replaced the form or draft",
						)
					}
					var current map[string]any
					if err := json.Unmarshal(
						f.api(t, "GET", scope.api, nil, 200), &current,
					); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(current, saved) {
						t.Fatal(
							"invalid notification submission changed saved settings",
						)
					}
				})
				if t.Failed() {
					return
				}
			}
			// When: the administrator corrects the endpoints and saves the draft.
			b.evaluate(
				t,
				`document.querySelector('[name=discord_webhook_url]').value = ''; document.querySelector('[name=gotify_url]').value = 'https://example.com/corrected'; true`,
			)
			clickNotificationSave(t, b)
			b.wait(
				t,
				`document.querySelector('[name=notify_emails]')?.getAttribute('value') === 'draft@example.com' && window.notificationForm !== document.querySelector('[name=notify_emails]').form`,
			)
			// Then: the refreshed form clears credentials and the API has the save.
			b.wait(
				t,
				`Array.from(document.querySelectorAll('[x-data="toast"]')).some(e => getComputedStyle(e).display !== 'none' && e.querySelector('.alert-success') && e.innerText.includes('Notification settings saved'))`,
			)
			if string(
				b.evaluate(
					t,
					`['slack_webhook_url','gotify_token','discord_webhook_url'].every(name => document.querySelector('[name='+name+']').value === '')`,
				),
			) != "true" {
				t.Fatal(
					"successful notification save did not clear secret inputs",
				)
			}
			var current map[string]any
			if err := json.Unmarshal(
				f.api(t, "GET", scope.api, nil, 200), &current,
			); err != nil {
				t.Fatal(err)
			}
			if current["gotify_url"] != "https://example.com/corrected" ||
				current["notify_emails"] != "draft@example.com" ||
				current["gotify_token"] != "" ||
				current["gotify_token_configured"] != true {
				t.Fatal(
					"corrected notification submission was not saved and masked",
				)
			}
		})
	}
}

func clickNotificationSave(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.evaluate(
		t,
		`document.querySelector('[name=notify_emails]').form.querySelector('button[type=submit]').scrollIntoView({block: 'center', behavior: 'instant'}); true`,
	)
	b.wait(t, `(() => {
		const button = document.querySelector('[name=notify_emails]').form.querySelector('button[type=submit]');
		const rect = button.getBoundingClientRect();
		return !button.disabled && !document.querySelector('.htmx-request,.htmx-settling') &&
			button.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2));
	})()`)
	var point struct{ X, Y float64 }
	if err := json.Unmarshal(b.evaluate(t, `(() => {
		const button = document.querySelector('[name=notify_emails]').form.querySelector('button[type=submit]');
		const rect = button.getBoundingClientRect();
		return {X: rect.x + rect.width / 2, Y: rect.y + rect.height / 2};
	})()`), &point); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"mousePressed", "mouseReleased"} {
		b.call(t, "Input.dispatchMouseEvent", map[string]any{
			"type": event, "x": point.X, "y": point.Y,
			"button": "left", "clickCount": 1,
		}, &struct{}{})
	}
}
