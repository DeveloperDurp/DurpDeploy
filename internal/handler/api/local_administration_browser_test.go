//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/auth"
)

func TestLastAdministratorBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	var owner struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1/users/me", nil, 200), &owner)
	before, err := f.h.repo.Queries.GetUserByID(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/admin/users/%d/edit", owner.ID)
	b.navigateBackTest(t, f.baseURL+path)
	b.wait(
		t,
		`document.querySelector('form[hx-put] [name=role]') && window.htmx`,
	)
	b.evaluate(t, `(() => {
 document.body.addEventListener('htmx:afterRequest', e => {window.localAdminStatus=e.detail.xhr.status;});
 const form=document.querySelector('form[hx-put]');
 for (const [name,value] of Object.entries({name:'Rejected administrator',role:'viewer',password:'rejected-password-105',password_confirmation:'rejected-password-105'})) {
  form.querySelector('[name='+name+']').value=value;
 }
 form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`window.localAdminStatus===422 && document.querySelector('#form-container [data-form-error]')?.textContent.includes('at least one administrator must remain')`,
	)
	if string(b.evaluate(t, `(() => {
 const form=document.querySelector('form[hx-put]');
 return document.querySelector('#form-container h1')?.textContent==='Edit user' &&
  !!document.querySelector('#form-container [data-user-form-body]') &&
  form.querySelector('[name=name]').value==='Rejected administrator' &&
  form.querySelector('[name=role]').value==='viewer' &&
  form.querySelector('[name=password]').value==='' &&
  form.querySelector('[name=password_confirmation]').value==='';
})()`)) != "true" {
		t.Fatal("rejected demotion lost its draft or retained password input")
	}
	actual, err := f.h.repo.Queries.GetUserByID(t.Context(), owner.ID)
	if err != nil || actual != before {
		t.Fatal("rejected browser demotion changed the account")
	}
	f.api(t, "GET", "/api/v1/admin/users", nil, 200)
	b.captureNavigation(t, "last-administrator-rejected")
	var replacement struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(t, "POST", "/api/v1/admin/users", map[string]string{
			"email": "replacement-105@example.test", "name": "Replacement administrator",
			"role": "admin", "password": "replacement-password-105",
		}, 201),
		&replacement,
	)
	b.evaluate(
		t,
		`document.querySelector('form[hx-put]').requestSubmit(); true`,
	)
	b.waitBackTestURL(t, f.baseURL+"/login?next=%2Fadmin%2Fusers")
	var current struct{ Role string }
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/users/me", nil, 200),
		&current,
	)
	if current.Role != "viewer" {
		t.Fatal("replacement handoff did not demote the original account")
	}
	f.api(t, "GET", "/api/v1/admin/users", nil, 403)
	_, f.token = seedAPIToken(t, f.h.repo, replacement.ID)
	f.api(t, "GET", "/api/v1/admin/users", nil, 200)
	user, err := f.h.repo.Queries.GetUserByID(t.Context(), replacement.ID)
	if err != nil ||
		!auth.VerifyPassword(user.PasswordHash, "replacement-password-105") {
		t.Fatal("replacement administrator has no usable password")
	}
}
