//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestScheduleTransitionsBrowserE2E(t *testing.T) {
	for _, transition := range []string{"skip", "park", "disable"} {
		t.Run(transition, func(t *testing.T) {
			f := newScheduleTransitionFixture(t, transition)
			finish := f.pause(t)
			if transition == "disable" {
				// Repair the legacy target through the public API before using
				// the inline editor, which edits only cron and enabled state.
				f.api(t, "PUT", f.path(), map[string]any{
					"release_id":     f.edited.ID,
					"environment_id": f.artifactE2E.environment.ID,
					"cron":           "* * * * *", "enabled": true, "note": "observed",
				}, 200)
			}
			b := startPackageBrowser(t)
			b.setBackTestSession(t, f.baseURL, f.session)
			b.navigateBackTest(t, fmt.Sprintf(
				"%s/projects/%d/schedules",
				f.baseURL,
				f.project.ID,
			))
			b.wait(
				t,
				`!!document.querySelector('tr [data-schedule-action="edit"]') && !document.querySelector('.htmx-settling')`,
			)
			b.evaluate(
				t,
				`document.querySelector('tr [data-schedule-action="edit"]').click(); true`,
			)
			b.wait(
				t,
				`document.querySelector('[data-desktop-schedule-edit] form').getClientRects().length > 0 && !document.querySelector('.htmx-settling')`,
			)
			b.evaluate(t, `(() => {
 const form = document.querySelector('[data-desktop-schedule-edit] form');
 form.querySelector('[name="cron"]').value = '0 0 1 1 *';
 form.querySelector('[name="enabled"]').checked = false;
 form.requestSubmit();
 return true;
})()`)
			b.wait(
				t,
				`location.pathname.endsWith('/schedules') && document.querySelector('tbody').textContent.includes('0 0 1 1 *') && document.querySelector('tr [data-schedule-action="toggle"]').textContent.trim() === 'Enable' && !document.querySelector('.htmx-settling')`,
			)
			var edited db.ScheduledDeployment
			decodeStepLogTest(t, f.api(t, "GET", f.path(), nil, 200), &edited)
			if edited.Enabled != 0 || edited.Cron != "0 0 1 1 *" {
				t.Fatalf("web edit did not commit: %+v", edited)
			}
			finish()
			f.assertPreserved(t, edited)
			b.navigateBackTest(
				t,
				fmt.Sprintf(
					"%s/projects/%d/schedules",
					f.baseURL,
					f.project.ID,
				),
			)
			b.wait(
				t,
				`document.querySelector('tbody').textContent.includes('0 0 1 1 *') && document.querySelector('tr [data-schedule-action="toggle"]').textContent.trim() === 'Enable'`,
			)
			b.screenshot(t, "schedule-concurrent-"+transition)
		})
	}
}
