//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
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
		checkNotificationCredentialForm(t, f, b, scope.api, scope.web)
	}
}

func checkNotificationCredentialForm(
	t *testing.T, f *artifactE2E, b *packageBrowser, apiPath, webPath string,
) {
	t.Helper()
	f.api(
		t,
		"PUT",
		apiPath,
		map[string]string{
			"slack_webhook_url":   "https://example.com/first-secret",
			"gotify_token":        "first-secret",
			"discord_webhook_url": "https://example.com/first-secret",
		},
		200,
	)
	b.navigateBackTest(t, f.baseURL+webPath)
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
		f.api(t, "GET", apiPath, nil, 200),
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
		f.api(t, "GET", apiPath, nil, 200),
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

func TestNotificationCardsViewerBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	for _, endpoint := range []string{f.base() + "/notifications", "/api/v1/admin/notifications/settings"} {
		f.api(t, "PUT", endpoint, map[string]string{
			"slack_webhook_url": "https://example.com/viewer-card-secret",
			"gotify_url":        "https://example.com/gotify", "gotify_token": "viewer-card-secret",
			"discord_webhook_url": "https://example.com/viewer-card-secret",
			"notify_emails":       "ops@example.com",
		}, 200)
	}
	user := seedAPIUser(
		t,
		f.h.repo,
		"notification-viewer@example.test",
		"viewer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: user.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	viewer := *f
	_, viewer.token = seedAPIToken(t, f.h.repo, user.ID)
	viewer.session = memberRoleSession(t, f.h, user.ID)
	viewer.csrf = "member-role-csrf"
	settings := viewer.api(t, "GET", f.base()+"/notifications", nil, 200)
	if strings.Contains(string(settings), "viewer-card-secret") {
		t.Fatal("viewer API exposed notification credentials")
	}
	viewer.api(t, "PUT", f.base()+"/notifications", map[string]string{
		"notify_emails": "forged@example.com",
	}, 403)
	viewer.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/notifications", f.project.ID),
		url.Values{
			"notify_emails": {"forged@example.com"},
		},
		403,
	)
	viewer.api(t, "GET", "/api/v1/admin/notifications/settings", nil, 403)
	viewer.web(t, "GET", "/admin/notifications/settings", nil, 403)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, viewer.session)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/notifications", f.baseURL, f.project.ID),
	)
	if string(b.evaluate(t, `(() => {
const main = document.querySelector('main');
return !main.querySelector('form, input, button[type=submit]') &&
['Slack','Email','Gotify','Discord'].every(name => [...main.querySelectorAll('h3')].some(h => h.textContent === name)) &&
main.textContent.includes('ops@example.com') && !main.innerHTML.includes('viewer-card-secret');
})()`)) != "true" {
		t.Fatal(
			"viewer notification cards lost read-only display or exposed controls",
		)
	}
	b.screenshot(t, "notification-viewer-cards")
	if string(
		b.evaluate(
			t,
			`(async () => (await fetch('/admin/notifications/settings')).status === 403)()`,
		),
	) != "true" {
		t.Fatal("viewer reached global notification settings")
	}
}
