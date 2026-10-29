//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestSchedulePagesBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "pagination"}, 201), &release)
	const note = "retain this schedule note"
	for range 51 {
		f.api(t, "POST", f.base()+"/schedules", map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID,
			"cron": "0 0 1 1 *", "enabled": false, "note": note,
		}, 201)
	}
	var page struct {
		Items []db.ScheduledDeployment `json:"items"`
		Total int64                    `json:"total"`
	}
	decodeStepLogTest(t, f.api(t, "GET",
		f.base()+"/schedules?limit=50&offset=50", nil, 200), &page)
	if len(page.Items) != 1 || page.Total != 51 {
		t.Fatalf("API page count=%d total=%d", len(page.Items), page.Total)
	}
	f.api(t, "GET", f.base()+"/schedules?offset=-1", nil, 400)
	lastID := page.Items[0].ID
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/projects/%d/schedules", f.project.ID)
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+path)
		b.wait(
			t,
			`document.querySelectorAll('[data-mobile-schedule]').length === 50 && !document.querySelector('.htmx-settling')`,
		)
		b.evaluate(
			t,
			`document.querySelector('nav[aria-label="Schedule pages"] a[href$="offset=50"]').click(); true`,
		)
		b.wait(
			t,
			`location.search === '?offset=50' && document.querySelectorAll('[data-mobile-schedule]').length === 1 && !document.querySelector('.htmx-settling')`,
		)
		if string(
			b.evaluate(t, `document.documentElement.scrollWidth <= innerWidth`),
		) != "true" {
			t.Fatal("schedule page overflows viewport")
		}
		b.screenshot(t, fmt.Sprintf("schedule-page-two-%d", width))
	}
	// New, Cancel, and Create must also retain the selected page.
	b.evaluate(
		t,
		`document.querySelector('main a[href*="/schedules/new"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname.endsWith('/schedules/new') && location.search === '?offset=50' && !document.querySelector('.htmx-settling')`,
	)
	b.evaluate(
		t,
		`[...document.querySelectorAll('main a')].find(a => a.textContent === 'Cancel').click(); true`,
	)
	b.wait(
		t,
		`location.search === '?offset=50' && !!document.querySelector('#schedules-content')`,
	)
	b.wait(t, `!document.querySelector('.htmx-settling')`)
	b.evaluate(
		t,
		`document.querySelector('main a[href*="/schedules/new"]').click(); true`,
	)
	b.wait(
		t,
		`!!document.querySelector('form select[name="release_id"]') && !document.querySelector('.htmx-settling')`,
	)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('form select[name="release_id"]').form;
 form.querySelector('[name="release_id"]').value = %q;
 form.querySelector('[name="environment_id"]').value = %q;
 form.querySelector('[name="cron"]').value = '0 0 1 1 *';
 form.querySelector('[name="note"]').value = %q;
 form.querySelector('[name="enabled"]').checked = false;
 form.requestSubmit();
 return true;
})()`, fmt.Sprint(release.ID), fmt.Sprint(f.environment.ID), note))
	b.wait(
		t,
		`location.search === '?offset=50' && document.querySelectorAll('[data-mobile-schedule]').length === 2`,
	)
	var newest struct {
		Items []db.ScheduledDeployment `json:"items"`
	}
	decodeStepLogTest(
		t,
		f.api(t, "GET", f.base()+"/schedules?limit=1", nil, 200),
		&newest,
	)
	if len(newest.Items) != 1 {
		t.Fatal("new schedule is missing from the public API")
	}
	created := newest.Items[0]
	if created.Cron != "0 0 1 1 *" || created.Enabled != 0 ||
		created.Note.String != note || !created.Note.Valid ||
		created.ReleaseID != release.ID ||
		created.EnvironmentID != f.environment.ID {
		t.Fatalf(
			"created schedule is inconsistent with the web form: %+v",
			created,
		)
	}
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/schedules/%d", f.base(), newest.Items[0].ID),
		nil,
		204,
	)
	b.navigateBackTest(t, f.baseURL+path+"?offset=50")
	b.wait(
		t,
		`document.querySelectorAll('[data-mobile-schedule]').length === 1 && !document.querySelector('.htmx-settling')`,
	)
	// Toggle and quick edit must retain the second page.
	b.evaluate(
		t,
		`document.querySelector('tr [data-schedule-action="toggle"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#schedules-content').dataset.scheduleOffset === '50' && document.querySelector('tr [data-schedule-action="toggle"]').textContent.trim() === 'Disable' && !document.querySelector('.htmx-settling')`,
	)
	for i, width := range []int{1280, 375} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		row, editor := "tbody tr", "[data-desktop-schedule-edit]"
		if width == 375 {
			row, editor = "[data-mobile-schedule]", "[data-mobile-schedule-edit]"
		}
		b.evaluate(t, fmt.Sprintf(
			`document.querySelector('%s [data-schedule-action="edit"]').click(); true`,
			row,
		))
		b.wait(t, fmt.Sprintf(
			`document.querySelector('%s').getClientRects().length > 0 && !document.querySelector('.htmx-settling')`,
			editor,
		))
		b.screenshot(t, fmt.Sprintf("schedule-quick-edit-%d", width))
		cron := fmt.Sprintf("0 %d 1 1 *", i+1)
		b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('%s form');
 form.querySelector('[name="cron"]').value = %q;
 form.querySelector('[name="enabled"]').checked = false;
 form.requestSubmit();
 return true;
})()`, editor, cron))
		b.wait(t, fmt.Sprintf(
			`document.querySelector('#schedules-content').dataset.scheduleOffset === '50' && document.querySelector('%s [data-schedule-action="toggle"]').textContent.trim() === 'Enable' && document.querySelector('%s').textContent.includes(%q) && !document.querySelector('.htmx-settling')`,
			row,
			row,
			cron,
		))
		var updated db.ScheduledDeployment
		decodeStepLogTest(t, f.api(
			t,
			"GET",
			fmt.Sprintf(
				"%s/schedules/%d",
				f.base(),
				lastID,
			),
			nil,
			200,
		), &updated)
		if updated.Cron != cron || updated.Enabled != 0 ||
			updated.Note.String != note || !updated.Note.Valid ||
			updated.ReleaseID != release.ID ||
			updated.EnvironmentID != f.environment.ID {
			t.Fatalf(
				"web update at width %d is inconsistent with the public API: %+v",
				width,
				updated,
			)
		}
		b.screenshot(t, fmt.Sprintf("schedule-quick-edit-saved-%d", width))
	}
	b.evaluate(
		t,
		`window.confirm = () => true; document.querySelector('tr [data-schedule-action="delete"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#schedules-content').dataset.scheduleOffset === '0' && document.querySelectorAll('[data-mobile-schedule]').length === 50`,
	)
	f.api(t, "GET", fmt.Sprintf("%s/schedules/%d", f.base(), lastID), nil, 404)
}
