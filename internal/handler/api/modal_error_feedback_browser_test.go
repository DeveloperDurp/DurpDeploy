//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func assertSingleModalError(
	t *testing.T,
	b *packageBrowser,
	selector, message string,
) {
	t.Helper()
	predicate := fmt.Sprintf(`(() => {
 const dialog = document.querySelector(%q);
 const errors = [...dialog.querySelectorAll('[role=alert]')]
  .filter(e => e.getClientRects().length && getComputedStyle(e).display !== 'none');
 const toast = Alpine.$data(document.querySelector('[x-data=toast]'));
 return dialog.open && errors.length === 1 && !toast.visible &&
  errors[0].textContent.includes(%q) && document.activeElement === errors[0];
})()`, selector, message)
	b.wait(t, predicate)
	b.evaluate(t, `new Promise(resolve => Alpine.nextTick(() =>
 requestAnimationFrame(() => requestAnimationFrame(() => resolve(true)))))`)
	if string(b.evaluate(t, predicate)) != "true" {
		t.Fatal("modal feedback changed after reactive updates settled")
	}
}

func TestUserModalPermissionFeedbackBrowserE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	userCount := len(f.listedUsers(t))
	before, err := f.h.repo.Queries.GetUserByID(t.Context(), f.users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, adminToken := seedAPIToken(t, f.h.repo, f.users[3].ID)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	checkToastWrapperBrowser(t, b, f.server.URL)
	for _, isNew := range []bool{true, false} {
		t.Run(fmt.Sprintf("new=%t", isNew), func(t *testing.T) {
			b.navigateBackTest(t, f.server.URL+"/admin/users")
			path := "/admin/users/new"
			if !isNew {
				path = fmt.Sprintf("/admin/users/%d/edit", f.users[0].ID)
			}
			b.evaluate(t, fmt.Sprintf(
				`document.querySelector('a[hx-get=%q]').click(); true`, path))
			b.wait(t, `document.querySelector('#project-edit-dialog')
 ?.matches(':modal') && document.querySelector('#user-name')`)
			// Keep an earlier server error visible before permission changes.
			b.evaluate(t, `(() => {
 const form = document.querySelector('[data-user-form] form');
 if (form.elements.email) form.elements.email.value = 'modal-denied@example.test';
 form.elements.name.value = 'Retained modal draft';
 form.elements.password.value = 'Modal-feedback-pass-1';
 form.elements.password_confirmation.value = 'mismatch';
 form.requestSubmit();
 return true;
})()`)
			assertSingleModalError(
				t,
				b,
				"#project-edit-dialog",
				"Passwords do not match",
			)
			f.request(
				t,
				"PUT",
				fmt.Sprintf("/api/v1/admin/users/%d", f.adminID),
				`{"name":"Downgraded admin","role":"viewer"}`,
				adminToken,
				"",
				false,
				200,
			)
			b.evaluate(t, `(() => {
 const form = document.querySelector('[data-user-form] form');
 form.elements.password.value = 'Modal-feedback-pass-1';
 form.elements.password_confirmation.value = 'Modal-feedback-pass-1';
 form.requestSubmit();
 return true;
})()`)
			b.waitBackTestURL(t, f.server.URL+"/login?next=%2Fadmin%2Fusers")
			f.request(t, "GET", "/api/v1/admin/users", "",
				f.token, "", false, 403)
			b.screenshot(t, fmt.Sprintf("user-modal-login-new-%t", isNew))
			f.request(
				t,
				"PUT",
				fmt.Sprintf("/api/v1/admin/users/%d", f.adminID),
				`{"name":"Restored admin","role":"admin"}`,
				adminToken,
				"",
				false,
				200,
			)
			f.session = memberRoleSession(t, f.h, f.adminID)
			b.setBackTestSession(t, f.server.URL, f.session)
			if len(f.listedUsers(t)) != userCount {
				t.Fatal("rejected modal created a user")
			}
			if got := userWithRole(t, f, f.users[0].Email, "viewer"); got.Name != f.users[0].Name {
				t.Fatal("rejected modal changed the existing user")
			}
			actual, err := f.h.repo.Queries.GetUserByID(
				t.Context(),
				f.users[0].ID,
			)
			if err != nil || actual != before {
				t.Fatal("revoked session changed the target account")
			}
		})
	}
}

func checkToastWrapperBrowser(t *testing.T, b *packageBrowser, origin string) {
	t.Helper()
	b.navigateBackTest(t, origin+"/admin/users")
	for _, args := range []string{"", ", undefined", ", null", ", 'warning'"} {
		result := b.evaluate(t, fmt.Sprintf(`(() => {
 let notification;
 window.addEventListener('show-toast', e => { notification = e.detail; },
  {once:true});
 window.showToast('Wrapper feedback'%s);
 const toast = Alpine.$data(document.querySelector('[x-data=toast]'));
 return notification.message === 'Wrapper feedback' &&
  toast.visible && toast.message === notification.message &&
  notification.type === %s && toast.type === notification.type;
})()`, args, map[string]string{
			"":            "'success'",
			", undefined": "'success'",
			", null":      "null",
			", 'warning'": "'warning'",
		}[args]))
		if string(result) != "true" {
			t.Fatalf("toast wrapper changed arguments %q", args)
		}
	}
	b.evaluate(t, `document.querySelector('a[hx-get="/admin/users/new"]')
 .click(); true`)
	b.wait(t, `document.querySelector('#project-edit-dialog')
 ?.matches(':modal') && document.querySelector('#user-name')`)
	b.evaluate(t, `window.showToast('Wrapper modal error', 'error',
 document.querySelector('#user-name')); true`)
	assertSingleModalError(t, b, "#project-edit-dialog", "Wrapper modal error")
	b.screenshot(t, "toast-wrapper-modal-error")
}

func TestMemberModalErrorFeedbackBrowserE2E(t *testing.T) {
	b := startPackageBrowser(t)
	for _, denial := range []string{"removed", "downgraded"} {
		t.Run(denial, func(t *testing.T) {
			f := newStaleProjectSaveFixture(t)
			f.changeRole(t, "project", "admin")
			member := seedAPIUser(t, f.h.repo,
				"member-feedback@example.test", "deployer")
			members := fmt.Sprintf("/api/v1/projects/%d/members", f.project.ID)
			f.request(t, "POST", members,
				fmt.Sprintf(`{"user_id":%d,"role":"deployer"}`, member.ID),
				f.adminToken, "", false, 201)
			b.setBackTestSession(t, f.server.URL, f.session)
			b.navigateBackTest(
				t,
				fmt.Sprintf("%s/projects/%d", f.server.URL, f.project.ID),
			)
			b.evaluate(t, `document.querySelector(
'#project-detail .page-header a[hx-get]').click(); true`)
			b.wait(t, `document.querySelector('#project-edit-dialog')
 ?.matches(':modal') && document.querySelector('#project-members')`)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector('[data-member-id="%d"]');
 row.querySelector('button').click();
 row.querySelector('select').value = 'maintainer';
 document.querySelector('[data-project-form] [name=description]').value =
  'Retained project draft';
 return true;
})()`, member.ID))
			expected := "Member not found"
			path := fmt.Sprintf("%s/%d", members, member.ID)
			if denial == "removed" {
				f.request(t, "DELETE", path, "", f.adminToken, "", false, 204)
			} else {
				f.changeRole(t, "project", "viewer")
				expected = "project role"
			}
			b.evaluate(t, fmt.Sprintf(`document.querySelector(
'[data-member-id="%d"] form').requestSubmit(); true`, member.ID))
			assertSingleModalError(t, b, "#project-edit-dialog", expected)
			if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector('[data-member-id="%d"]');
 return row.querySelector('select').value === 'maintainer' &&
  document.querySelector('[data-project-form] [name=description]').value ===
   'Retained project draft';
})()`, member.ID))) != "true" {
				t.Fatal("member rejection discarded a role or project draft")
			}
			f.assertDescription(t, f.project.Description.String)
			assertMemberFeedbackRoster(
				t,
				f,
				members,
				member.ID,
				denial == "downgraded",
			)
			b.screenshot(t, "member-modal-error-"+denial)
		})
	}
}

func assertMemberFeedbackRoster(
	t *testing.T,
	f staleProjectSaveFixture,
	path string,
	memberID int64,
	wantFound bool,
) {
	t.Helper()
	var saved struct {
		Items []struct {
			UserID int64 `json:"user_id"`
			Role   string
		}
	}
	decodeStepLogTest(t, f.request(t, "GET", path, "",
		f.adminToken, "", false, 200), &saved)
	found := false
	for _, item := range saved.Items {
		if item.UserID == memberID {
			found = true
			if item.Role != "deployer" {
				t.Fatal("rejected modal changed the member role")
			}
		}
	}
	if found != wantFound {
		t.Fatal("rejected modal changed the member roster")
	}
}
