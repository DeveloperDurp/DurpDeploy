//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentMobileBrowserE2E(t *testing.T) {
	// Given: real API snapshots containing long names and scripts.
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	empty := seedRelease(t, f.h.repo, f.project.ID)
	name := "Prepare the application configuration and database connections"
	script := "echo " + strings.Repeat("deployment-output", 12)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": name, "script_body": script,
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{
			"version": strings.Repeat("mobile-release-", 4),
		}, 201), &release)
	for _, state := range []string{
		"running", "pending_approval", "failed", "succeeded",
	} {
		t.Run(state, func(t *testing.T) {
			deployment := seedDeployment(t, f.h.repo,
				release.ID, f.environment.ID, state)
			path := fmt.Sprintf("/deployments/%d", deployment.ID)
			var read db.Deployment
			decodeStepLogTest(t, f.api(t, "GET",
				"/api/v1"+path, nil, 200), &read)
			if read.Status != state || read.ReleaseID != release.ID {
				t.Fatal("deployment API lost status or release")
			}
			// When: the operator opens the detail at each width and theme.
			b.navigateBackTest(t, f.baseURL+path)
			b.wait(t, `document.querySelector('[hx-get$="/verification"]')`+
				`?.textContent.includes('Verification disabled')`)
			// Then: logs lead, metadata fits, and controls remain usable.
			b.captureNavigation(t, "mobile-deployment-"+state, func() {
				b.evaluate(t, `window.scrollTo(0, 0); true`)
				assertDeploymentMobileLayout(t, b)
				if string(b.evaluate(t, fmt.Sprintf(
					`document.querySelector('[data-step-index="0"] summary').textContent.includes(%q)`,
					name,
				))) != "true" {
					t.Fatal("deployment log panel lost its step name")
				}
				if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const button = suffix => document.querySelector('button[hx-post$="/' + suffix + '"]');
 return !!button('approve') === %t && !!button('cancel') === %t &&
 !!button('redeploy') === %t;
})()`, state == "pending_approval",
					state == "running" || state == "pending_approval",
					state == "failed" || state == "succeeded"))) != "true" {
					t.Fatal("deployment actions do not match status")
				}
			})
			if state != "failed" {
				return
			}
			b.captureNavigation(t, "mobile-deployment-menu", func() {
				b.evaluate(t, `(() => {
 window.scrollTo(0, 0);
 const menu = document.querySelector('main details[data-focus-menu]');
 menu.open = true; menu.querySelector('summary').focus(); return true;
})()`)
				if string(b.evaluate(t, `(() => {
 const menu = document.querySelector('main details[data-focus-menu] ul');
 const box = menu.getBoundingClientRect();
 return box.left >= 0 && box.right <= innerWidth &&
 menu.querySelector('a[href$="/rollback"]') &&
 !menu.querySelector('a[href$="/logs.txt"]').hasAttribute('hx-boost');
})()`)) != "true" {
					t.Fatal("secondary actions overflow or boost downloads")
				}
			})
			b.call(t, "Input.dispatchKeyEvent", map[string]any{
				"type": "keyDown", "key": "Escape", "code": "Escape",
				"windowsVirtualKeyCode": 27,
			}, &struct{}{})
			b.wait(
				t,
				`!document.querySelector('main details[data-focus-menu]').open`,
			)
		})
	}
	deployment := seedDeployment(t, f.h.repo,
		empty.ID, f.environment.ID, "succeeded")
	b.navigateBackTest(t, fmt.Sprintf(
		"%s/deployments/%d", f.baseURL, deployment.ID))
	b.wait(t, `document.querySelector('[hx-get$="/verification"]')`+
		`?.textContent.includes('Verification disabled')`)
	b.captureNavigation(t, "mobile-deployment-empty", func() {
		b.evaluate(t, `window.scrollTo(0, 0); true`)
		assertDeploymentMobileLayout(t, b)
	})
}

func assertDeploymentMobileLayout(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const logs = document.querySelector('[aria-label="Step logs"]');
 const steps = document.querySelector('[aria-label="Step definitions"]');
 const verification = document.querySelector('[hx-get$="/verification"]');
 const visible = el => el.getBoundingClientRect().height > 0;
 const header = document.querySelector('[x-data^="deploymentStepLogs"] > div');
 const controls = [...header.querySelectorAll('button, a, summary')];
 return steps === null && visible(logs) &&
 logs.compareDocumentPosition(verification) & Node.DOCUMENT_POSITION_FOLLOWING &&
 verification.getBoundingClientRect().height < 48 &&
 [...header.querySelectorAll('dd')].every(el => el.scrollWidth <= el.clientWidth) &&
 (innerWidth >= 640 || logs.getBoundingClientRect().top < innerHeight &&
 controls.filter(visible).every(el => el.getBoundingClientRect().height >= 44));
})()`)) != "true" {
		t.Fatal(
			"deployment layout shows definitions, hides logs, or clips controls",
		)
	}
}
