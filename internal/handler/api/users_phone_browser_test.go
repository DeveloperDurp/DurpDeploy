//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestUsersPhoneBrowserE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	for _, width := range []int{390, 768, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
			}, &struct{}{})
			b.navigateBackTest(t, f.server.URL+"/admin/users")
			assertUsersPhoneLayout(t, b, f)
			b.screenshot(t, fmt.Sprintf("users-phone-%d", width))
		})
	}
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 390, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	checkUsersPhoneActions(t, b, f)
}

func assertUsersPhoneLayout(
	t *testing.T,
	b *packageBrowser,
	f usersPhoneFixture,
) {
	t.Helper()
	for _, user := range f.users {
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector('[data-user-id="%d"]');
 const email = row?.querySelector('[data-user-email]');
 const name = row?.querySelector('[data-user-name]');
 const badge = row?.querySelector('.badge');
 const actions = [...(row?.querySelectorAll('a, button') || [])];
 const mobile = innerWidth < 1024;
 const fits = el => el && el.scrollWidth <= el.clientWidth;
 const overlap = (a,b) => a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
 if (!row || !email?.textContent.includes(%q) || !name?.textContent.includes(%q) ||
 badge?.textContent !== %q || actions.length !== 3 || !fits(row)) return false;
 if (!mobile) return getComputedStyle(row).display === 'table-row' &&
 getComputedStyle(row.closest('table').tHead).display === 'table-header-group';
 return getComputedStyle(row).display === 'grid' && fits(email) && fits(name) &&
 getComputedStyle(email).textOverflow !== 'ellipsis' &&
 actions.every((action,i) => {
  const box=action.getBoundingClientRect();
  return box.height >= 44 && box.left >= 0 && box.right <= innerWidth &&
   !overlap(box,badge.getBoundingClientRect()) &&
   !overlap(box,email.getBoundingClientRect()) && !overlap(box,name.getBoundingClientRect()) &&
   actions.slice(i+1).every(other=>!overlap(box,other.getBoundingClientRect()));
 }) && document.documentElement.scrollWidth <= innerWidth;
})()`, user.ID, user.Email, user.Name, user.Role))) != "true" {
			b.screenshot(t, "users-phone-unreadable")
			t.Fatalf("identity or actions unreadable for %s", user.Role)
		}
	}
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const row=document.querySelector('[data-user-id="%d"]');
 return row?.querySelectorAll('a,button').length === 1 && !row.querySelector('[hx-delete], [data-mfa-reset-opener]');
})()`, f.adminID))) != "true" {
		t.Fatal("self row exposes unsafe account actions")
	}
}

func checkUsersPhoneActions(
	t *testing.T,
	b *packageBrowser,
	f usersPhoneFixture,
) {
	t.Helper()
	user := f.users[2]
	row := fmt.Sprintf(`[data-user-id="%d"]`, user.ID)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('%s [data-mfa-reset-opener]').click(); true`,
			row,
		),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('#mfa-reset-dialog-%d').matches(':modal')`,
			user.ID,
		),
	)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const dialog=document.querySelector('#mfa-reset-dialog-%d');
 const box=dialog.querySelector('.modal-box');
 return dialog.textContent.includes(%q) && box.scrollWidth <= box.clientWidth;
})()`, user.ID, user.Email))) != "true" {
		t.Fatal("MFA confirmation lost or clipped its target identity")
	}
	b.screenshot(t, "users-phone-mfa-target")
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('#mfa-reset-dialog-%d form[method="dialog"] button').click(); true`,
			user.ID,
		),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`!document.querySelector('#mfa-reset-dialog-%d').open && document.activeElement?.matches('%s [data-mfa-reset-opener]')`,
			user.ID,
			row,
		),
	)
	b.evaluate(
		t,
		fmt.Sprintf(`document.querySelector('%s a').click(); true`, row),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === '/admin/users/%d/edit' && document.querySelector('input[name="name"]')?.value === %q && document.querySelector('select[name="role"]')?.value === %q`,
			user.ID,
			user.Name,
			user.Role,
		),
	)
	b.navigateBackTest(t, f.server.URL+"/admin/users")
	user = f.users[0]
	b.evaluate(
		t,
		fmt.Sprintf(
			`window.confirm=()=>true; document.querySelector('[data-user-id="%d"] [hx-delete]').click(); true`,
			user.ID,
		),
	)
	b.wait(
		t,
		fmt.Sprintf(`!document.querySelector('[data-user-id="%d"]')`, user.ID),
	)
	for _, current := range f.listedUsers(t) {
		if current.ID == user.ID {
			t.Fatal("phone delete did not persist through the API")
		}
	}
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-user-id="%d"] [data-user-email]').textContent.includes(%q)`,
				f.users[1].ID,
				f.users[1].Email,
			),
		),
	) != "true" {
		t.Fatal("phone delete affected the neighboring identity")
	}
	b.screenshot(t, "users-phone-deleted")
}
