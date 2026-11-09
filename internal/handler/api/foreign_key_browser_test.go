//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestForeignKeyFeedbackBrowserE2E(t *testing.T) {
	f := foreignKeyFixture(t)
	environment, _ := foreignKeyReferences(t, f)
	b := startLivePackageBrowser(t, f.baseURL)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/environments/%d/edit", f.baseURL, environment.ID),
	)
	b.wait(t, `window.Alpine && document.querySelector('button[hx-delete]')`)
	b.wire.events = nil
	b.evaluate(
		t,
		`setTimeout(() => document.querySelector('button[hx-delete]').click(), 0); true`,
	)
	if err := b.wire.waitEvent(
		"Page.javascriptDialogOpening",
		b.session,
	); err != nil {
		t.Fatal(err)
	}
	b.call(
		t,
		"Page.handleJavaScriptDialog",
		map[string]bool{"accept": true},
		&struct{}{},
	)
	b.wait(
		t,
		`document.querySelector('[x-data="toast"]')?.innerText.includes('1 lifecycle stages') && !!document.querySelector('button[hx-delete]')`,
	)
	b.screenshot(t, "foreign-key-environment-conflict")
	var project struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/projects",
			map[string]string{"name": "FK browser " + uuid.NewString()},
			201,
		),
		&project,
	)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/variables", f.baseURL, project.ID),
	)
	b.wait(
		t,
		`window.Alpine && document.querySelector('form[hx-post] select[name=environment_id]')`,
	)
	// Simulate a stale selection after another administrator deletes an environment.
	b.evaluate(t, `(() => {
 const select = document.querySelector('form[hx-post] select[name=environment_id]');
 select.add(new Option('Removed environment', '2147483647'));
 select.value = '2147483647';
 select.form.elements.name.value = 'STALE';
 select.form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`document.querySelector('[x-data="toast"]')?.innerText.includes('Environment does not exist') && document.querySelector('form[hx-post] [name=name]')?.value === 'STALE'`,
	)
	b.screenshot(t, "foreign-key-variable-validation")
	var release struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/projects/%d/releases", project.ID),
			map[string]string{"version": "v1"},
			201,
		),
		&release,
	)
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/projects/%d/deploy?release_id=%d",
			f.baseURL,
			project.ID,
			release.ID,
		),
	)
	b.wait(
		t,
		`window.Alpine && document.querySelector('select[name=environment_id]')`,
	)
	b.evaluate(t, `(() => {
 const select = document.querySelector('select[name=environment_id]');
 select.add(new Option('Removed environment', '2147483647'));
 select.value = '2147483647';
 select.dispatchEvent(new Event('change', {bubbles:true}));
 select.form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`document.querySelector('[x-data="toast"]')?.innerText.includes('Environment not found') && !!document.querySelector('select[name=environment_id]')`,
	)
	b.screenshot(t, "foreign-key-deployment-validation")
	t.Logf(
		"Retained browser validation project: %s/projects/%d/variables",
		f.baseURL,
		project.ID,
	)
}
