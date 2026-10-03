//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBackNavigationBrowserE2E(t *testing.T) {
	// Given: a deployment reachable from two different pages.
	f := newArtifactE2E(t)
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(
		t, f.h.repo, release.ID, f.environment.ID, "succeeded",
	)
	browser := startPackageBrowser(t)
	browser.setBackTestSession(t, f.baseURL, f.session)
	f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d", deployment.ID,
	), nil, 200)
	detail := fmt.Sprintf("/deployments/%d", deployment.ID)
	for _, previous := range []string{
		"/deployments?status=succeeded&limit=20&offset=0",
		fmt.Sprintf("/projects/%d/releases/%d", f.project.ID, release.ID),
	} {
		t.Run(previous, func(t *testing.T) {
			browser.navigateBackTest(t, f.baseURL+previous)
			// When: navigating to the deployment, then using Back.
			if strings.HasPrefix(previous, "/deployments") {
				browser.evaluate(t, fmt.Sprintf(
					`document.querySelector('a[href=%q]').click(); true`,
					detail,
				))
				browser.waitBackTestURL(t, f.baseURL+detail)
			} else {
				browser.navigateBackTest(t, f.baseURL+detail)
			}
			browser.clickBackTest(t)
			// Then: Back restores the exact entry URL, including filters.
			browser.waitBackTestURL(t, f.baseURL+previous)
		})
	}
}

func (b *packageBrowser) setBackTestSession(
	t *testing.T, address, session string,
) {
	t.Helper()
	var cookie struct {
		Success bool `json:"success"`
	}
	b.call(t, "Network.setCookie", map[string]any{
		"name": "session", "value": session,
		"url": address, "httpOnly": true,
	}, &cookie)
	if !cookie.Success {
		t.Fatal("test browser session was not established")
	}
}

func (b *packageBrowser) clickBackTest(t *testing.T) {
	t.Helper()
	b.evaluate(
		t,
		`[...document.querySelectorAll('a')].find(a => ['Back', 'Go back'].includes(a.textContent.trim())).click(); true`,
	)
}

func (b *packageBrowser) navigateBackTest(t *testing.T, address string) {
	t.Helper()
	b.call(t, "Page.navigate", map[string]string{
		"url": address,
	}, &struct{}{})
	b.waitBackTestURL(t, address)
}

func (b *packageBrowser) waitBackTestURL(t *testing.T, address string) {
	t.Helper()
	encoded, err := json.Marshal(address)
	if err != nil {
		t.Fatal(err)
	}
	b.wait(t, fmt.Sprintf(
		`location.href === %s && document.readyState === 'complete' && window.Alpine && document.querySelector('h1')`,
		encoded,
	))
}
