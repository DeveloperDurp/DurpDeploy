//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestNotificationCredentialFormsBrowserE2E(t *testing.T) {
	// Given: both notification scopes contain existing credentials.
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, scope := range []struct{ api, web string }{
		{f.base() + "/notifications", fmt.Sprintf("/projects/%d/notifications", f.project.ID)},
		{"/api/v1/admin/notifications/settings", "/admin/notifications/settings"},
	} {
		f.api(
			t,
			"PUT",
			scope.api,
			map[string]string{
				"slack_webhook_url":   "https://example.com/first-secret",
				"gotify_token":        "first-secret",
				"discord_webhook_url": "https://example.com/first-secret",
			},
			200,
		)
		b.navigateBackTest(t, f.baseURL+scope.web)
		// When: an administrator opens the form and saves an unrelated setting.
		if string(
			b.evaluate(
				t,
				`[...document.querySelectorAll('[name=slack_webhook_url],[name=gotify_token],[name=discord_webhook_url]')].every(e=>e.type==='password' && e.value==='') && !document.documentElement.outerHTML.includes('first-secret')`,
			),
		) != "true" {
			t.Fatal("credential leaked into form")
		}
		b.evaluate(
			t,
			`document.querySelector('[name=notify_emails]').value='edited@example.com'; document.querySelector('[name=notify_emails]').form.querySelector('button[type=submit]').click(); true`,
		)
		b.wait(
			t,
			`document.readyState==='complete' && document.querySelector('[name=notify_emails]').getAttribute('value')==='edited@example.com'`,
		)
		// Then: blank password fields preserve configured credentials.
		if string(
			b.evaluate(
				t,
				`document.querySelector('[name=slack_webhook_url]').closest('.form-control').textContent.includes('configured')`,
			),
		) != "true" {
			t.Fatal("credential state disappeared")
		}
		var settings map[string]any
		if err := json.Unmarshal(
			f.api(t, "GET", scope.api, nil, 200),
			&settings,
		); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"slack_webhook_url", "gotify_token", "discord_webhook_url"} {
			if settings[name] != "" || settings[name+"_configured"] != true {
				t.Fatalf(
					"blank form did not preserve masked credential %s",
					name,
				)
			}
		}
		// A replacement and an explicit clear stay available through normal form submission.
		b.evaluate(
			t,
			`document.querySelector('[name=slack_webhook_url]').value='https://example.com/second-secret'; document.querySelector('[name=gotify_token]').value='second-secret'; document.querySelector('[name=discord_webhook_url]').value='https://example.com/second-secret'; document.querySelector('[name=notify_emails]').form.querySelector('button[type=submit]').click(); true`,
		)
		b.wait(
			t,
			`document.readyState==='complete' && document.querySelector('[name=slack_webhook_url]').value===''`,
		)
		if string(
			b.evaluate(
				t,
				`!document.documentElement.outerHTML.includes('second-secret')`,
			),
		) != "true" {
			t.Fatal("replacement credential leaked")
		}
		b.evaluate(
			t,
			`document.querySelector('[name=slack_webhook_url]').value='ftp://example.com/ignored'; document.querySelector('[name=discord_webhook_url]').value='missing-scheme'; for(const e of document.querySelectorAll('[name^=clear_]'))e.checked=true; document.querySelector('[name=notify_emails]').form.querySelector('button[type=submit]').click(); true`,
		)
		b.wait(
			t,
			`document.readyState==='complete' && [...document.querySelectorAll('[name=slack_webhook_url],[name=gotify_token],[name=discord_webhook_url]')].every(e=>e.closest('.form-control').textContent.includes('not configured'))`,
		)
		b.screenshot(t, "notification-credentials-cleared")
		if err := json.Unmarshal(
			f.api(t, "GET", scope.api, nil, 200),
			&settings,
		); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"slack_webhook_url", "gotify_token", "discord_webhook_url"} {
			if settings[name] != "" || settings[name+"_configured"] != false {
				t.Fatalf("clear form did not remove credential %s", name)
			}
		}
	}
}
