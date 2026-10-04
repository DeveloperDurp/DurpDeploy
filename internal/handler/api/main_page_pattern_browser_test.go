//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestMainPagePatternBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var template, lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/templates",
		map[string]string{
			"name": "consistent-template", "script_body": "echo hi",
			"interpreter": "bash", "container_image": "alpine:3.20",
			"execution_target": "local",
		}, 201), &template)
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "consistent-lifecycle"}, 201), &lifecycle)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	paths := []string{
		"/projects", "/environments", "/lifecycles", "/templates",
		"/deployments", "/admin/agents",
		fmt.Sprintf("/environments/%d/edit", f.environment.ID),
		fmt.Sprintf("/lifecycles/%d", lifecycle.ID),
		fmt.Sprintf("/templates/%d/edit", template.ID),
	}
	for _, suffix := range []string{
		"", "/edit", "/steps-page", "/variables", "/releases", "/schedules",
		"/runbooks", "/package-repository",
	} {
		paths = append(
			paths,
			fmt.Sprintf("/projects/%d%s", f.project.ID, suffix),
		)
	}
	for i, path := range paths {
		b.navigateBackTest(t, f.baseURL+path)
		b.wait(t, `document.querySelector('main .page-header h1') !== null`)
		b.captureNavigation(t, fmt.Sprintf("main-page-%d", i), func() {
			if string(b.evaluate(t, `(() => {
 const header = document.querySelector('main .page-header');
 const title = header.querySelector('h1');
 const controls = [...header.querySelectorAll('.btn')];
 return title.scrollWidth <= title.clientWidth &&
 controls.every(el => el.getBoundingClientRect().right <= innerWidth &&
 (innerWidth >= 768 || el.getBoundingClientRect().height >= 44));
})()`)) != "true" {
				t.Fatalf("header clips text or phone controls on %s", path)
			}
		})
	}
	// Header Save controls submit the real edit form and persist through the API.
	for _, edit := range []struct {
		path, api, name string
	}{
		{fmt.Sprintf("/projects/%d/edit", f.project.ID),
			fmt.Sprintf("/api/v1/projects/%d", f.project.ID), "saved-project"},
		{fmt.Sprintf("/templates/%d/edit", template.ID),
			fmt.Sprintf("/api/v1/templates/%d", template.ID), "saved-template"},
	} {
		b.navigateBackTest(t, f.baseURL+edit.path)
		b.evaluate(
			t,
			fmt.Sprintf(
				`window.savedForm = document.querySelector('main input[name="name"]').form; document.querySelector('main input[name="name"]').value = %q; document.querySelector('.page-header button[type="submit"]').click(); true`,
				edit.name,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`!window.savedForm?.isConnected && (document.querySelector('main input[name="name"]')?.value === %q || document.querySelector('main').textContent.includes(%q))`,
				edit.name,
				edit.name,
			),
		)
		var saved struct{ Name string }
		decodeStepLogTest(t, f.api(t, "GET", edit.api, nil, 200), &saved)
		if saved.Name != edit.name {
			t.Fatalf("header Save did not persist %s", edit.api)
		}
	}
}
