//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestUsersFormAccessibilityBrowserE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	for _, width := range []int{390, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 520, "deviceScaleFactor": 1,
				"mobile": false,
			}, &struct{}{})
			b.navigateBackTest(t, f.server.URL+"/admin/users")
			email := fmt.Sprintf("accessible-browser-%d@example.test", width)
			assertUserCreateModalFlow(t, b, width, email)
			assertUserEditModalFlow(t, b, f, width, email)
		})
	}
}

func assertUserCreateModalFlow(
	t *testing.T,
	b *packageBrowser,
	width int,
	email string,
) {
	t.Helper()
	b.evaluate(
		t,
		`document.querySelector('a[hx-get="/admin/users/new"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal') &&
 document.activeElement?.id === 'user-email'`,
	)
	b.evaluate(
		t,
		`document.querySelector('#user-name').value='Discarded draft';
 document.querySelector('#project-edit-dialog .page-header a').click(); true`,
	)
	b.wait(t, `!document.querySelector('#project-edit-dialog').open &&
 !document.querySelector('#project-edit-content').hasChildNodes() &&
 document.activeElement?.matches('a[hx-get="/admin/users/new"]')`)
	b.evaluate(
		t,
		`document.querySelector('a[hx-get="/admin/users/new"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal') &&
 !document.querySelector('#user-name').value`,
	)
	if string(b.evaluate(t, `location.pathname === '/admin/users' &&
 document.querySelector('#project-edit-dialog').getAttribute('aria-label') === 'New user' &&
 document.querySelector('#user-role').value === 'viewer' &&
 !document.querySelector('#project-edit-dialog [hx-delete]')`)) != "true" {
		t.Fatal(
			"new user modal lost its title, default role, or list URL",
		)
	}
	assertUserFormAX(t, b, true)
	assertUserModalScroll(t, b)
	assertModalKeyboardViewport(t, b, "#project-edit-dialog",
		"[data-user-form-body]", "#user-password-confirmation")
	assertUserNativeRejection(t, b, true, width)
	b.evaluate(
		t,
		`document.querySelector('label[for="user-email"]').click(); true`,
	)
	b.wait(t, `document.activeElement.id === 'user-email'`)
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": "Tab", "code": "Tab",
		"windowsVirtualKeyCode": 9,
	}, &struct{}{})
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": "Tab", "code": "Tab",
		"windowsVirtualKeyCode": 9,
	}, &struct{}{})
	b.wait(t, `document.activeElement.id === 'user-name'`)
	b.evaluate(
		t,
		`document.querySelector('#user-role').value='maintainer'; true`,
	)
	fillDefaultRoleUser(t, b, email, "mismatch")
	assertUserFormErrorFocus(t, b, "Passwords do not match")
	assertUserFormAX(t, b, true)
	assertUserModalScroll(t, b)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal')`,
	)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const form=document.querySelector('#user-form-new');
 return form.elements.email.value === %q &&
 form.elements.name.value === 'Default role browser user' &&
 form.elements.role.value === 'maintainer' &&
 !form.elements.password.value && !form.elements.password_confirmation.value;
})()`, email))) != "true" {
		t.Fatal(
			"validation lost non-secret values or retained a password",
		)
	}
	b.screenshot(t, fmt.Sprintf("user-accessible-error-%d", width))
	fillDefaultRoleUser(t, b, email, "Default-role-pass-1")
	b.wait(
		t,
		`document.querySelector('[data-user-id]') && !document.querySelector('#user-form-new')`,
	)
}

func assertUserEditModalFlow(
	t *testing.T,
	b *packageBrowser,
	f usersPhoneFixture,
	width int,
	email string,
) {
	t.Helper()
	user := userWithRole(t, f, email, "maintainer")
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": 520, "deviceScaleFactor": 1,
		"mobile": false,
	}, &struct{}{})
	open := fmt.Sprintf(`[data-user-id="%d"] a[hx-get]`, user.ID)
	for _, closeAction := range []string{
		`document.querySelector('#project-edit-dialog .page-header a').click()`,
		`document.querySelector('#project-edit-dialog .modal-backdrop button').click()`,
	} {
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('%s').click(); true`,
				open,
			),
		)
		b.wait(
			t,
			`document.querySelector('#project-edit-dialog').matches(':modal') &&
 document.querySelector('#user-name') === document.activeElement`,
		)
		b.evaluate(t, closeAction+`; true`)
		b.wait(
			t,
			fmt.Sprintf(
				`!document.querySelector('#project-edit-dialog').open &&
 !document.querySelector('#project-edit-content').hasChildNodes() &&
 document.activeElement?.matches('%s')`,
				open,
			),
		)
	}
	b.evaluate(
		t,
		fmt.Sprintf(`document.querySelector('%s').click(); true`, open),
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal')`,
	)
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": "Escape", "code": "Escape",
		"windowsVirtualKeyCode": 27,
	}, &struct{}{})
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": "Escape", "code": "Escape",
		"windowsVirtualKeyCode": 27,
	}, &struct{}{})
	b.wait(
		t,
		fmt.Sprintf(
			`!document.querySelector('#project-edit-dialog').open &&
 document.activeElement?.matches('%s')`,
			open,
		),
	)
	b.evaluate(
		t,
		fmt.Sprintf(`document.querySelector('%s').click(); true`, open),
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal')`,
	)
	if string(b.evaluate(t, `(() => {
 const box=document.querySelector('#project-edit-dialog .modal-box');
 const bounds=box.getBoundingClientRect();
 return location.pathname === '/admin/users' && box.scrollWidth <= box.clientWidth &&
 bounds.left >= 0 && bounds.right <= innerWidth &&
 document.querySelectorAll('#user-name').length === 1;
})()`)) != "true" {
		t.Fatal("user modal overflowed or changed the list URL")
	}
	assertUserFormAX(t, b, false)
	assertUserModalScroll(t, b)
	assertModalKeyboardViewport(t, b, "#project-edit-dialog",
		"[data-user-form-body]", "#user-password-confirmation")
	assertUserNativeRejection(t, b, false, width)
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[hx-put]');
 form.elements.name.value='Retained accessible edit';
 form.elements.password.value='Accessible-edit-pass-1';
 form.elements.password_confirmation.value='mismatch';
 document.querySelector('button[form="'+form.id+'"]').click();
 return true;
})()`)
	assertUserFormErrorFocus(t, b, "Passwords do not match")
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal')`,
	)
	assertUserFormAX(t, b, false)
	assertUserModalScroll(t, b)
	if string(b.evaluate(t, `(() => {
 const form=document.querySelector('form[hx-put]');
 return form.elements.name.value === 'Retained accessible edit' &&
 form.elements.role.value === 'maintainer' &&
 !form.elements.password.value && !form.elements.password_confirmation.value;
})()`)) != "true" {
		t.Fatal(
			"HTMX validation lost edit values or retained a password",
		)
	}
	if saved := userWithRole(t, f, email, "maintainer"); saved.Name != user.Name {
		t.Fatal("rejected browser edit persisted")
	}
	b.screenshot(t, fmt.Sprintf("user-accessible-edit-error-%d", width))
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[hx-put]');
 document.querySelector('button[form="'+form.id+'"]').click();
 return true;
})()`)
	b.wait(
		t,
		`document.querySelector('[data-user-id]') && !document.querySelector('form[hx-put]')`,
	)
	if saved := userWithRole(t, f, email, "maintainer"); saved.Name != "Retained accessible edit" {
		t.Fatal("corrected browser edit did not persist")
	}
}

func assertUserNativeRejection(
	t *testing.T,
	b *packageBrowser,
	isNew bool,
	width int,
) {
	t.Helper()
	b.evaluate(t, `window.userValidationWrites=0;
 document.addEventListener('htmx:beforeRequest', event => {
 if (event.detail.requestConfig.verb !== 'get') window.userValidationWrites++;
 });
 window.userValidationName=document.querySelector('#user-name').value;
 document.querySelector('#user-name').value='';
 document.querySelector('[data-user-form] .page-header button').click(); true`)
	b.wait(
		t,
		`document.querySelector('#user-validation-error').getClientRects().length &&
 document.querySelector('#user-name-error').getClientRects().length &&
 document.querySelector('#user-name').validity.valueMissing`,
	)
	if isNew {
		b.wait(
			t,
			`['user-email','user-password','user-password-confirmation'].every(id =>
 document.querySelector('#'+id+'-error').getClientRects().length)`,
		)
		b.evaluate(t, `const form=document.querySelector('#user-form-new');
 form.elements.name.value='Native draft';
 form.elements.email.value='not-an-email';
 form.elements.password.value='Native-valid-pass-1';
 form.elements.password_confirmation.value='Native-valid-pass-1';
 document.querySelector('[data-user-form] .page-header button').click(); true`)
		b.wait(
			t,
			`document.querySelector('#user-email').validity.typeMismatch &&
 document.querySelector('#user-email-error').getClientRects().length`,
		)
	}
	if string(b.evaluate(t, `(() => {
 const error=document.querySelector('#user-validation-error');
 const bounds=error.getBoundingClientRect();
 return error.getAttribute('role') === 'alert' && bounds.top >= 0 &&
 bounds.bottom <= innerHeight && window.userValidationWrites === 0;
})()`)) != "true" {
		t.Fatal(
			"native validation did not show a visible warning or sent a write",
		)
	}
	b.screenshot(t, fmt.Sprintf("user-native-error-%t-%d", isNew, width))
	b.evaluate(t, fmt.Sprintf(`document.querySelector('#user-name').value=%q;
 const email=document.querySelector('#user-email');
 if (email) email.value='native-corrected@example.test'; true`, "Corrected name"))
	b.wait(
		t,
		`!document.querySelector('#user-validation-error').getClientRects().length &&
 !document.querySelector('#user-name-error').getClientRects().length`,
	)
	if !isNew {
		b.evaluate(
			t,
			`document.querySelector('#user-name').value=window.userValidationName; true`,
		)
	}
}

func assertUserModalScroll(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const dialog=document.querySelector('#project-edit-dialog');
 const box=dialog.querySelector('.modal-box');
 const body=dialog.querySelector('[data-user-form-body]');
 const header=dialog.querySelector('.page-header');
 const before=header.getBoundingClientRect();
 body.scrollTop=body.scrollHeight;
 const after=header.getBoundingClientRect();
 const bounds=body.getBoundingClientRect();
 const last=(body.querySelector('[hx-delete]') ||
 body.querySelector('[name="password_confirmation"]')).getBoundingClientRect();
 const result=body.scrollTop > 0 && getComputedStyle(body).overflowY === 'auto' &&
 Math.abs(before.top-after.top) < 1 && after.top >= 0 && after.bottom <= bounds.top &&
 last.top >= bounds.top && last.bottom <= bounds.bottom+1 &&
 box.scrollTop === 0 && box.getBoundingClientRect().bottom <= innerHeight;
 body.scrollTop=0;
 return result;
})()`)) != "true" {
		t.Fatal("user fields did not scroll beneath the fixed modal header")
	}
}

func assertUserFormErrorFocus(
	t *testing.T,
	b *packageBrowser,
	message string,
) {
	t.Helper()
	b.wait(t, fmt.Sprintf(`(() => {
 const error=document.querySelector('#form-error');
 const toast=Alpine.$data(document.querySelector('[x-data=toast]'));
 return error?.textContent.includes(%q) && error.getAttribute('role') === 'alert' &&
 document.activeElement === error && document.querySelectorAll('#form-error').length === 1 &&
 !toast.visible && document.querySelectorAll('[data-modal-error]').length === 0;
})()`, message))
}

func assertUserFormAX(t *testing.T, b *packageBrowser, isNew bool) {
	t.Helper()
	var tree struct {
		Nodes []struct {
			Name struct {
				Value string `json:"value"`
			} `json:"name"`
			Role struct {
				Value string `json:"value"`
			} `json:"role"`
		} `json:"nodes"`
	}
	b.call(t, "Accessibility.getFullAXTree", struct{}{}, &tree)
	expected := map[string]string{
		"Name": "textbox", "Role": "combobox",
		"Password": "textbox", "Confirm password": "textbox",
	}
	if isNew {
		expected["Email"] = "textbox"
	} else {
		delete(expected, "Password")
		delete(expected, "Confirm password")
		expected["New password (blank = keep current)"] = "textbox"
		expected["Confirm new password"] = "textbox"
	}
	for _, node := range tree.Nodes {
		if expected[node.Name.Value] == node.Role.Value {
			delete(expected, node.Name.Value)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("accessibility tree lacks named controls: %v", expected)
	}
}
