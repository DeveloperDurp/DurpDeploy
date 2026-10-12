//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestPackageRepositoryBackBrowserE2E(t *testing.T) {
	// Given: both a writer and a viewer can reach the same project.
	f := newArtifactE2E(t)
	browser := startPackageBrowser(t)
	viewer := seedAPIUser(t, f.h.repo, "back-viewer@example.com", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(), db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: viewer.ID, Role: "deployer",
		},
	); err != nil {
		t.Fatal(err)
	}
	viewerSession, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: viewerSession, UserID: viewer.ID, CsrfToken: csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("%s/projects/%d", f.baseURL, f.project.ID)
	repository := project + "/package-repository"
	for _, configured := range []bool{false, true} {
		if configured {
			f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
				"url_template": f.upstream + "/{version}.zip",
				"auth_type":    "bearer", "credential": "artifact-secret",
			}, 200)
		}
		f.api(t, "GET", f.base()+"/package-repository", nil, 200)
		for _, user := range []struct{ role, session string }{
			{"admin", f.session}, {"viewer", viewerSession},
		} {
			browser.setBackTestSession(t, f.baseURL, user.session)
			for _, width := range []int{375, 768, 1280} {
				t.Run(fmt.Sprintf("%s/%t/%d", user.role, configured, width),
					func(t *testing.T) {
						browser.call(t, "Emulation.setDeviceMetricsOverride",
							map[string]any{
								"width": width, "height": 900,
								"deviceScaleFactor": 1, "mobile": false,
							}, &struct{}{},
						)
						browser.navigateBackTest(t, project)
						// When: opening Packages through its actual project link.
						browser.evaluate(t, fmt.Sprintf(
							`document.querySelector('a[href="/projects/%d/package-repository"]').click(); true`,
							f.project.ID,
						))
						browser.waitBackTestURL(t, repository)
						// Then: Back fits and remains keyboard accessible for everyone.
						if string(browser.evaluate(
							t,
							`(() => { window.scrollTo(0, 0); const back = document.querySelector('a[x-data=backNavigation]'); const r = back.getBoundingClientRect(); back.focus({preventScroll: true}); return r.width > 0 && r.left >= 0 && r.right <= innerWidth && r.top >= 0 && document.activeElement === back && document.documentElement.scrollWidth <= innerWidth; })()`,
						)) != "true" {
							t.Fatal("Back is missing, clipped, or inaccessible")
						}
						browser.screenshot(t, fmt.Sprintf(
							"back-package-%s-%t-%d",
							user.role,
							configured,
							width,
						))
						browser.call(
							t,
							"Input.dispatchKeyEvent",
							map[string]any{
								"type": "keyDown", "key": "Enter", "code": "Enter",
								"windowsVirtualKeyCode": 13,
							},
							&struct{}{},
						)
						browser.call(
							t,
							"Input.dispatchKeyEvent",
							map[string]any{
								"type": "keyUp", "key": "Enter", "code": "Enter",
								"windowsVirtualKeyCode": 13,
							},
							&struct{}{},
						)
						browser.waitBackTestURL(t, project)
					})
			}
		}
	}

	// Viewer form guard and protocol rejection return to the real prior page.
	browser.navigateBackTest(t, project)
	browser.navigateBackTest(t, repository+"/edit")
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, project)
	browser.wire.events = nil
	browser.evaluate(
		t,
		`(() => { const form = document.createElement('form'); form.method = 'post'; form.action = '/projects'; document.body.append(form); setTimeout(() => form.requestSubmit(), 0); return true; })()`,
	)
	if err := browser.wire.waitEvent(
		"Page.frameStoppedLoading", browser.session,
	); err != nil {
		t.Fatal(err)
	}
	browser.wait(
		t,
		"document.title === 'Forbidden' && document.readyState === 'complete' && window.Alpine",
	)
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, project)
}
