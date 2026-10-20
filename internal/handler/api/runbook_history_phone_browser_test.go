//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestRunbookHistoryPhoneBrowserE2E(t *testing.T) {
	f, rows := runbookHistoryPhoneFixture(t)
	viewer, userID := newDeletionMember(t, f, "viewer")
	f.api(t, "POST", f.base()+"/members", map[string]any{
		"user_id": userID, "role": "viewer",
	}, 201)
	b := startPackageBrowser(t)
	path := fmt.Sprintf("/projects/%d/runbooks", f.project.ID)
	for index, actor := range []*artifactE2E{f, &viewer} {
		b.setBackTestSession(t, f.baseURL, actor.session)
		for _, width := range []int{375, 390, 768, 1280} {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
			}, &struct{}{})
			b.navigateBackTest(
				t,
				fmt.Sprintf("%s%s?actor=%d", f.baseURL, path, index),
			)
			b.wait(
				t,
				fmt.Sprintf(
					`document.querySelectorAll('[data-runbook-execution-id]').length === 3 && !![...document.querySelectorAll('a')].find(a => a.textContent === 'New runbook') === %t`,
					index == 0,
				),
			)
			assertRunbookHistoryPhone(t, b, rows)
			b.screenshot(
				t,
				fmt.Sprintf("runbook-history-actor%d-%d", index, width),
			)
		}
		b.evaluate(
			t,
			`document.querySelector('[data-runbook-execution-id] a').focus(); true`,
		)
		for _, kind := range []string{"keyDown", "keyUp"} {
			text := ""
			if kind == "keyDown" {
				text = "\r"
			}
			b.call(t, "Input.dispatchKeyEvent", map[string]any{
				"type": kind, "key": "Enter", "code": "Enter",
				"windowsVirtualKeyCode": 13, "text": text,
			}, &struct{}{})
		}
		b.wait(
			t,
			fmt.Sprintf(
				`location.pathname === %q && document.querySelector('[x-data^="deploymentStream"]')`,
				fmt.Sprintf(
					"/projects/%d/runbooks/executions/%d",
					f.project.ID,
					rows[0].ID,
				),
			),
		)
	}
}

func assertRunbookHistoryPhone(
	t *testing.T,
	b *packageBrowser,
	rows []db.ListRunbookExecutionsRow,
) {
	t.Helper()
	for _, row := range rows {
		check := fmt.Sprintf(
			`(() => {
 const row = document.querySelector('[data-runbook-execution-id="%d"]');
 const link = row?.querySelector('a');
 const fields = ['version','environment','status','created'].map(name => row?.querySelector('[data-execution-'+name+']'));
 const expected = [%q, %q, %q, %q];
 if (!row || link?.textContent !== %q || link.getAttribute('href') !== %q ||
 fields.some((field,i) => !field || field.textContent !== expected[i] || !field.getClientRects().length)) return false;
 const mobile = innerWidth < 640;
 if (getComputedStyle(row).display !== (mobile ? 'grid' : 'table-row')) return false;
 if (!mobile) return true;
 const cells = [...row.children];
 const overlap = (a,b) => a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
 return link.getBoundingClientRect().height >= 44 && cells.every((cell,i) => {
  const box = cell.getBoundingClientRect();
  return cell.scrollWidth <= cell.clientWidth && box.left >= 0 && box.right <= innerWidth &&
   cells.slice(i+1).every(other => !overlap(box,other.getBoundingClientRect()));
 }) && document.documentElement.scrollWidth <= innerWidth && !document.querySelector('img[src=x]');
})()`,
			row.ID,
			fmt.Sprint(row.Version),
			row.EnvironmentName,
			row.Status,
			time.Unix(row.CreatedAt, 0).Format("2006-01-02 15:04:05"),
			fmt.Sprintf("#%d — %s", row.ID, row.RunbookName),
			fmt.Sprintf(
				"/projects/%d/runbooks/executions/%d",
				row.ProjectID,
				row.ID,
			),
		)
		if string(b.evaluate(t, check)) != "true" {
			b.screenshot(t, "runbook-history-unreadable")
			t.Fatalf(
				"execution %d identity is missing, clipped, or overlaps",
				row.ID,
			)
		}
	}
}
