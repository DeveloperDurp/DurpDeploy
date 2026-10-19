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
				"width": width, "height": 900, "deviceScaleFactor": 1,
				"mobile": false,
			}, &struct{}{})
			b.navigateBackTest(t, f.server.URL+"/admin/users/new")
			assertUserFormAX(t, b, true)
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
			email := fmt.Sprintf("accessible-browser-%d@example.test", width)
			b.evaluate(
				t,
				`document.querySelector('#user-role').value='maintainer'; true`,
			)
			fillDefaultRoleUser(t, b, email, "mismatch")
			assertUserFormErrorFocus(t, b, "Passwords do not match")
			assertUserFormAX(t, b, true)
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
			user := userWithRole(t, f, email, "maintainer")
			b.navigateBackTest(t, fmt.Sprintf("%s/admin/users/%d/edit",
				f.server.URL, user.ID))
			assertUserFormAX(t, b, false)
			b.evaluate(t, `(() => {
 const form=document.querySelector('form[hx-put]');
 form.elements.name.value='Retained accessible edit';
 form.elements.password.value='Accessible-edit-pass-1';
 form.elements.password_confirmation.value='mismatch';
 document.querySelector('button[form="'+form.id+'"]').click();
 return true;
})()`)
			assertUserFormErrorFocus(t, b, "Passwords do not match")
			assertUserFormAX(t, b, false)
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
		})
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
 return error?.textContent.includes(%q) && error.getAttribute('role') === 'alert' &&
 document.activeElement === error && document.querySelectorAll('#form-error').length === 1;
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
