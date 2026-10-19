//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestUsersDefaultRoleBrowserE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 390, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, f.server.URL+"/admin/users/new")
	assertBrowserUserRole(t, b, "viewer")
	b.screenshot(t, "user-default-viewer-390")
	fillDefaultRoleUser(
		t,
		b,
		"browser-default-viewer@example.test",
		"Default-role-pass-1",
	)
	b.wait(t, `location.pathname === '/admin/users' &&
 document.querySelector('[data-user-id]') && !document.querySelector('#user-form-new')`)
	viewer := userWithRole(
		t,
		f,
		"browser-default-viewer@example.test",
		"viewer",
	)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/admin/users/%d/edit", f.server.URL, viewer.ID),
	)
	assertBrowserUserRole(t, b, "viewer")

	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 1280, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, f.server.URL+"/admin/users/new")
	assertBrowserUserRole(t, b, "viewer")
	b.evaluate(
		t,
		`document.querySelector('select[name=role]').value='admin'; true`,
	)
	fillDefaultRoleUser(t, b, "browser-explicit-admin@example.test", "mismatch")
	b.wait(
		t,
		`document.querySelector('#form-error')?.textContent.includes('Passwords do not match')`,
	)
	assertBrowserUserRole(t, b, "admin")
	b.screenshot(t, "user-explicit-admin-validation")
	fillDefaultRoleUser(
		t,
		b,
		"browser-explicit-admin@example.test",
		"Default-role-pass-1",
	)
	b.wait(t, `location.pathname === '/admin/users' &&
 document.querySelector('[data-user-id]') && !document.querySelector('#user-form-new')`)
	admin := userWithRole(t, f, "browser-explicit-admin@example.test", "admin")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/admin/users/%d/edit", f.server.URL, admin.ID),
	)
	assertBrowserUserRole(t, b, "admin")
	b.screenshot(t, "user-explicit-admin-edit")

	b.navigateBackTest(t, f.server.URL+"/admin/users/new")
	b.evaluate(
		t,
		`document.querySelector('select[name=role]').disabled=true; true`,
	)
	fillDefaultRoleUser(
		t,
		b,
		"browser-missing-role@example.test",
		"Default-role-pass-1",
	)
	b.wait(
		t,
		`document.querySelector('#form-error')?.textContent.includes('Role must be')`,
	)
	assertBrowserUserRole(t, b, "viewer")
	b.screenshot(t, "user-rejected-missing-role")
	for _, user := range f.listedUsers(t) {
		if user.Email == "browser-missing-role@example.test" {
			t.Fatal("missing role persisted a user")
		}
	}
}

func assertBrowserUserRole(t *testing.T, b *packageBrowser, want string) {
	t.Helper()
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const role=document.querySelector('select[name=role]');
 return role?.value === %q && role.selectedOptions.length === 1 &&
 document.body.innerText.includes(%q);
})()`, want, userRoleExplanation))) != "true" {
		b.screenshot(t, "user-role-incorrect")
		t.Fatalf("role selection or explanation incorrect, want %s", want)
	}
}

func fillDefaultRoleUser(
	t *testing.T,
	b *packageBrowser,
	email, confirmation string,
) {
	t.Helper()
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form=document.querySelector('#user-form-new');
 form.elements.email.value=%q;
 form.elements.name.value='Default role browser user';
 form.elements.password.value='Default-role-pass-1';
 form.elements.password_confirmation.value=%q;
 document.querySelector('button[form="user-form-new"]').click();
 return true;
})()`, email, confirmation))
}
