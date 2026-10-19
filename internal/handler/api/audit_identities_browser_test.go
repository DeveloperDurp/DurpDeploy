//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestAuditIdentitiesBrowserE2E(t *testing.T) {
	f := newAuditIdentityFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	for _, width := range []int{390, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.server.URL+"/admin/audit")
		checkAuditBrowserIdentity(t, b, f)
		b.evaluate(t, fmt.Sprintf(`(() => {
 const input = document.querySelector('#audit-user');
 input.value = %q;
 input.form.requestSubmit(); return true;
})()`, strings.ToUpper(f.actor.Email)))
		b.wait(
			t,
			`new URL(location.href).searchParams.get('user') === 'OPERATOR@AUDIT.TEST'`,
		)
		checkAuditBrowserIdentity(t, b, f)
		b.screenshot(t, fmt.Sprintf("audit-identities-%d", width))
	}
	// A name containing HTML and SQL wildcard characters remains literal.
	b.navigateBackTest(
		t,
		f.server.URL+"/admin/audit?user="+url.QueryEscape(f.actor.Name),
	)
	checkAuditBrowserIdentity(t, b, f)
	b.evaluate(
		t,
		`document.querySelector('a[data-audit-target]').click(); true`,
	)
	b.waitBackTestURL(
		t,
		fmt.Sprintf("%s/admin/users/%d/edit", f.server.URL, f.target.ID),
	)
	b.wait(
		t,
		`document.querySelector('input[name=name]')?.value === 'Audit Target'`,
	)
	b.screenshot(t, "audit-target-user")
	f.request(t, "DELETE", fmt.Sprintf("/api/v1/admin/users/%d", f.target.ID),
		"", f.token, "", false, 204)
	b.navigateBackTest(t, f.server.URL+"/admin/audit?action=update_user")
	b.wait(t, `document.querySelectorAll('[data-audit-target]').length === 0`)
	b.screenshot(t, "audit-deleted-target")
	b.navigateBackTest(t, f.server.URL+"/admin/audit?user=%25")
	b.wait(
		t,
		`document.body.textContent.includes('No audit entries match the filter.') && document.querySelector('#audit-user').value === '%'`,
	)
	b.evaluate(
		t,
		`document.querySelector('a[href="/admin/audit"]').click(); true`,
	)
	b.waitBackTestURL(t, f.server.URL+"/admin/audit")
	b.wait(
		t,
		`document.querySelector('#audit-user')?.value === '' && !!document.querySelector('[data-audit-actor]')`,
	)
}

func checkAuditBrowserIdentity(
	t *testing.T,
	b *packageBrowser,
	f auditIdentityFixture,
) {
	t.Helper()
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const visible = element => element.getClientRects().length > 0;
 const actor = [...document.querySelectorAll('[data-audit-actor]')].find(visible);
 const target = [...document.querySelectorAll('[data-audit-target]')].find(visible);
 const input = document.querySelector('#audit-user');
 const table = document.querySelector('[data-audit-table]');
 const cards = document.querySelector('[data-mobile-audit-list]');
 const correctLayout = innerWidth < 1024 ?
  !visible(table) && visible(cards) : visible(table) && !visible(cards);
 return actor?.textContent.includes(%q) && actor.textContent.includes(%q) &&
 actor.textContent.includes('User ID %d') &&
 target?.getAttribute('href') === '/admin/users/%d/edit' &&
 target.textContent.includes('user %d') &&
 input.labels[0]?.textContent.includes('User name or email') &&
 !document.querySelector('[data-audit-actor] owner') &&
 correctLayout && document.documentElement.scrollWidth <= innerWidth;
})()`, f.actor.Name, f.actor.Email, f.actor.ID, f.target.ID, f.target.ID))) != "true" {
		b.screenshot(t, "audit-identity-failure")
		t.Fatal(
			"audit identity, accessible filter, target, stable IDs, or layout failed",
		)
	}
}
