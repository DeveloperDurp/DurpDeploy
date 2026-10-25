//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/repository"
)

func TestSecretExportBrowserE2E(t *testing.T) {
	for _, scope := range []string{"project", "lifecycle"} {
		for _, method := range []string{"password", "oidc"} {
			t.Run(scope+"/"+method, func(t *testing.T) {
				testSecretExportBrowser(t, scope, method)
			})
		}
	}
}

func testSecretExportBrowser(t *testing.T, scope, method string) {
	t.Helper()
	f := newExportFixture(t, method == "oidc")
	if method == "oidc" {
		f.issuer.EmulateAuthentikReauth()
	}
	browser := startPackageBrowser(t, method == "oidc")
	if method == "oidc" {
		browser.call(
			t,
			"Security.setIgnoreCertificateErrors",
			map[string]bool{"ignore": true},
			&struct{}{},
		)
	}
	browser.setBackTestSession(t, f.server.URL, f.session)
	browser.call(
		t,
		"Page.setDownloadBehavior",
		map[string]string{
			"behavior":     "allow",
			"downloadPath": "/tmp/secret-export-test-downloads",
		},
		&struct{}{},
	)
	path := fmt.Sprintf("/projects/%d/variables", f.project.ID)
	if scope == "lifecycle" {
		path = fmt.Sprintf(
			"/lifecycles/%d/variables",
			f.lifecycle.ID,
		)
	}
	browser.navigateBackTest(t, f.server.URL+path)
	browser.wait(
		t,
		`document.querySelector('form[action="/admin/secret-exports"] button')`,
	)
	browser.evaluate(
		t,
		`document.querySelector('form[action="/admin/secret-exports"] button').click(); true`,
	)
	browser.wait(
		t,
		`location.pathname.startsWith('/admin/secret-exports/') && document.querySelector('form[action$="/authorize"]')`,
	)
	var current string
	if err := json.Unmarshal(browser.evaluate(t, `location.pathname`), &current); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(current, "/admin/secret-exports/")
	captureSecretExportBrowser(t, browser, scope, method)
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/authorize"] button').click(); true`,
	)
	browser.wait(
		t,
		`location.pathname === '/settings/security/reauth' && document.querySelector('[name=export_id]')`,
	)
	if method == "password" {
		browser.evaluate(
			t,
			`document.querySelector('[name=password]').value='incorrect'; document.querySelector('form[action="/settings/security/reauth"] button').click(); true`,
		)
		browser.wait(
			t,
			`document.querySelector('[role=alert]') && document.querySelector('[name=export_id]')`,
		)
		browser.evaluate(
			t,
			`document.querySelector('[name=password]').value='testpass'; document.querySelector('form[action="/settings/security/reauth"] button').click(); true`,
		)
	} else {
		browser.evaluate(t, `document.querySelector('a[href^="/settings/security/reauth/oidc"]').click(); true`)
	}
	browser.wait(
		t,
		`location.pathname.startsWith('/admin/secret-exports/') && document.querySelector('form[action$="/download"]')`,
	)
	if method == "oidc" &&
		(f.issuer.Capture().Authorization.Prompt != "login" || f.issuer.Capture().Authorization.MaxAge != "1" || f.issuer.Counters().Reauthenticate != 1) {
		t.Fatal("browser OIDC reused existing login")
	}
	browser.screenshot(t, "secret-export-"+scope+"-"+method+"-approved")
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/download"] button').click(); true`,
	)
	f.waitForBrowserExportConsumption(t, id)
	checkSecretExportBrowserCancellation(t, f, browser, path)
}

func captureSecretExportBrowser(
	t *testing.T,
	browser *packageBrowser,
	scope, method string,
) {
	t.Helper()
	for _, width := range []int{375, 1280} {
		browser.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            900,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		browser.wait(
			t,
			`document.documentElement.scrollWidth <= innerWidth`,
		)
		browser.screenshot(
			t,
			fmt.Sprintf(
				"secret-export-%s-%s-%d",
				scope,
				method,
				width,
			),
		)
	}
}

func (f *exportFixture) waitForBrowserExportConsumption(
	t *testing.T,
	id string,
) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		_, err := f.h.repo.SecretExport(t.Context(), id, f.user.ID)
		if errors.Is(err, repository.ErrSecretExportInvalid) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("native download did not consume approval")
}

func checkSecretExportBrowserCancellation(
	t *testing.T,
	f *exportFixture,
	browser *packageBrowser,
	path string,
) {
	t.Helper()
	browser.navigateBackTest(t, f.server.URL+path)
	browser.wait(
		t,
		`document.querySelector('form[action="/admin/secret-exports"] button')`,
	)
	browser.evaluate(
		t,
		`document.querySelector('form[action="/admin/secret-exports"] button').click(); true`,
	)
	browser.wait(
		t,
		`document.querySelector('form[action$="/cancel"]')`,
	)
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/cancel"] button').click(); true`,
	)
	browser.wait(t, `location.pathname === '/projects'`)
	if _, err := f.h.repo.DB.Exec("UPDATE users SET role = 'maintainer' WHERE id = ?", f.user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.Exec("INSERT INTO project_members(project_id,user_id,role) VALUES (?,?,'admin')", f.project.ID, f.user.ID); err != nil {
		t.Fatal(err)
	}
	browser.navigateBackTest(
		t,
		f.server.URL+fmt.Sprintf(
			"/projects/%d/variables",
			f.project.ID,
		),
	)
	browser.wait(
		t,
		`document.querySelector('#variables-content') && !document.querySelector('form[action="/admin/secret-exports"]')`,
	)
}
