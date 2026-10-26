//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestRoleChangeRevocationBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var target struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", "/api/v1/admin/users", map[string]string{
			"email": "role-browser@example.test", "name": "Role browser", "role": "viewer", "password": "role-browser-password",
		}, 201),
		&target,
	)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, memberRoleSession(t, f.h, target.ID))
	b.navigateBackTest(t, f.baseURL+"/projects")
	path := fmt.Sprintf("/api/v1/admin/users/%d", target.ID)
	f.api(
		t,
		"PUT",
		path,
		map[string]string{"name": "Renamed viewer", "role": "viewer"},
		200,
	)
	b.navigateBackTest(t, f.baseURL+"/projects")
	for _, role := range []string{"admin", "viewer"} {
		f.api(
			t,
			"PUT",
			path,
			map[string]string{"name": "Renamed viewer", "role": role},
			200,
		)
		b.call(
			t,
			"Page.navigate",
			map[string]string{"url": f.baseURL + "/projects"},
			&struct{}{},
		)
		b.waitBackTestURL(t, f.baseURL+"/login?next=%2Fprojects")
		b.wait(
			t,
			`document.querySelector('#login-email') && document.querySelector('#login-password')`,
		)
		b.captureNavigation(t, "role-change-login-"+role)
		b.evaluate(t, `(() => {
 document.querySelector('#login-email').value='role-browser@example.test';
 document.querySelector('#login-password').value='role-browser-password';
 document.querySelector('#login-email').form.requestSubmit();
 return true;
})()`)
		b.waitBackTestURL(t, f.baseURL+"/projects")
		if role == "admin" {
			b.navigateBackTest(t, f.baseURL+"/admin/users")
		} else {
			b.navigateBackTest(t, f.baseURL+"/projects")
			b.wait(t, `!document.querySelector('a[href="/admin/users"]')`)
		}
	}
}
