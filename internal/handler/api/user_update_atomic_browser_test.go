//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/auth"
)

func TestUserUpdateAtomicBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var target struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", "/api/v1/admin/users", map[string]string{
			"email": "atomic-browser@example.test", "name": "Original account", "role": "deployer", "password": "old-password-96",
		}, 201),
		&target,
	)
	before, err := f.h.repo.Queries.GetUserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/admin/users/%d/edit", target.ID)
	b.navigateBackTest(t, f.baseURL+path)
	b.wait(
		t,
		`document.querySelector('form[hx-put] [name=password_confirmation]') && window.htmx`,
	)
	b.evaluate(t, `(() => {
 document.body.addEventListener('htmx:afterRequest', e => { window.accountUpdateStatus=e.detail.xhr.status; });
 const form=document.querySelector('form[hx-put]');
 for (const [name,value] of Object.entries({name:'Updated account',role:'viewer',password:'new-password-96',password_confirmation:'new-password-96'})) {
  form.querySelector('[name='+name+']').value=value;
 }
 return true;
})()`)
	if _, err := f.h.repo.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_account_password BEFORE UPDATE OF password_hash ON users BEGIN SELECT RAISE(ABORT,'injected password failure'); END`); err != nil {
		t.Fatal(err)
	}
	b.evaluate(t, `document.querySelector('form[hx-put]').requestSubmit();true`)
	b.wait(
		t,
		`window.accountUpdateStatus===500 && !document.querySelector('.htmx-request')`,
	)
	actual, err := f.h.repo.Queries.GetUserByID(t.Context(), target.ID)
	if err != nil || actual != before {
		t.Fatalf("failed browser request changed account: %+v: %v", actual, err)
	}
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`location.pathname===%q && document.querySelector('form[hx-put] [name=name]').value==='Updated account'`,
				path,
			),
		),
	) != "true" {
		t.Fatal("failed update left the edit page or lost the draft")
	}
	b.captureNavigation(t, "account-update-rollback")
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "DROP TRIGGER reject_account_password"); err != nil {
		t.Fatal(err)
	}
	b.evaluate(t, `document.querySelector('form[hx-put]').requestSubmit();true`)
	b.waitBackTestURL(t, f.baseURL+"/admin/users")
	actual, err = f.h.repo.Queries.GetUserByID(t.Context(), target.ID)
	if err != nil || actual.Name != "Updated account" ||
		actual.Role != "viewer" ||
		!auth.VerifyPassword(actual.PasswordHash, "new-password-96") {
		t.Fatalf("combined browser update did not commit: %+v: %v", actual, err)
	}
	var response struct {
		ID         int64
		Name, Role string
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/admin/users/%d", target.ID),
			nil,
			200,
		),
		&response,
	)
	if response.ID != target.ID || response.Name != actual.Name ||
		response.Role != actual.Role {
		t.Fatalf("public API differs from browser update: %+v", response)
	}
	// The role-only and password-only paths use the same rendered form.
	for _, password := range []string{"", "final-password-96"} {
		b.navigateBackTest(t, f.baseURL+path)
		b.wait(
			t,
			`document.querySelector('form[hx-put] [name=password_confirmation]')`,
		)
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => {const f=document.querySelector('form[hx-put]');f.querySelector('[name=role]').value='deployer';f.querySelector('[name=password]').value=%q;f.querySelector('[name=password_confirmation]').value=%q;f.requestSubmit();return true;})()`,
				password,
				password,
			),
		)
		b.waitBackTestURL(t, f.baseURL+"/admin/users")
	}
	actual, err = f.h.repo.Queries.GetUserByID(t.Context(), target.ID)
	if err != nil || actual.Role != "deployer" ||
		!auth.VerifyPassword(actual.PasswordHash, "final-password-96") {
		t.Fatalf("individual updates failed: %+v: %v", actual, err)
	}
}
