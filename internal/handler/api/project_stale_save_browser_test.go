//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestProjectStaleSaveBrowserE2E(t *testing.T) {
	b := startPackageBrowser(t)
	for _, scope := range []string{"project", "global"} {
		for _, role := range []string{"viewer", "deployer"} {
			t.Run(scope+"/"+role, func(t *testing.T) {
				checkProjectStaleSaveBrowser(t, b, scope, role)
			})
		}
	}
}

func checkProjectStaleSaveBrowser(
	t *testing.T,
	b *packageBrowser,
	scope, role string,
) {
	t.Helper()
	f := newStaleProjectSaveFixture(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	modal := (scope == "global") == (role == "viewer")
	if modal {
		b.navigateBackTest(t, f.server.URL+path)
		b.evaluate(t, `document.querySelector(
'#project-detail .page-header a[hx-get]').click(); true`)
		b.wait(t, `document.querySelector(
'#project-edit-dialog')?.matches(':modal')`)
	} else {
		path += "/edit"
		b.navigateBackTest(t, f.server.URL+path)
	}
	b.wait(t, `document.querySelector('[data-project-form] form') &&
!document.querySelector('.htmx-request, .htmx-settling')`)
	b.evaluate(t, `(() => {
window.staleSaveForm = document.querySelector('[data-project-form] form');
window.staleSaveForm.querySelector('[name=description]').value =
 'Keep this unsaved project description';
window.staleSaveResult = null;
document.body.addEventListener('htmx:afterRequest', event => {
 window.staleSaveResult = {
  status: event.detail.xhr.status,
  trigger: event.detail.xhr.getResponseHeader('HX-Trigger')
 };
}, {once:true});
return true;
})()`)
	f.changeRole(t, scope, role)
	b.evaluate(t, `window.staleSaveForm.requestSubmit(); true`)
	b.wait(t, `window.staleSaveResult !== null &&
window.staleSaveForm.querySelector('[role=alert]')?.getClientRects().length > 0`)
	result := b.evaluate(t, fmt.Sprintf(`(() => {
const toast = Alpine.$data(document.querySelector('[x-data="toast"]'));
const error = window.staleSaveForm.querySelector('[role=alert]');
const notification = JSON.parse(window.staleSaveResult.trigger).makeToast;
return {
 status: window.staleSaveResult.status,
 serverError: notification.level,
 toastVisible: toast.visible, toastType: toast.type, message: toast.message,
 inlineError: error?.getClientRects().length > 0 &&
  error.textContent === notification.message,
 singleModalError: !window.staleSaveForm.closest('dialog') ||
  (!toast.visible && window.staleSaveForm.closest('dialog')
   .querySelectorAll('[role=alert]').length === 1),
 formRetained: document.querySelector('[data-project-form] form') ===
  window.staleSaveForm,
 draftRetained: window.staleSaveForm.querySelector('[name=description]').value
  === 'Keep this unsaved project description',
 pathRetained: location.pathname === %q
};
})()`, path))
	t.Logf("rejected stale save: %s", result)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
const result = %s;
return result.status === 200 && result.serverError === 'danger' &&
 (%t ? result.singleModalError :
  result.toastVisible && result.toastType === 'error' && result.message) &&
 result.inlineError &&
 result.formRetained && result.draftRetained && result.pathRetained;
})()`, result, modal))) != "true" {
		t.Fatal("denied save showed success or discarded the draft")
	}
	f.assertDescription(t, f.project.Description.String)
	b.screenshot(t, "stale-save-denied-"+scope+"-"+role)

	// Restored permission must allow the retained draft to save.
	f.changeRole(t, scope, "maintainer")
	b.evaluate(t, `window.staleSaveForm.requestSubmit(); true`)
	b.wait(
		t,
		`document.querySelector('#project-detail')?.textContent
 .includes('Keep this unsaved project description')`,
	)
	f.assertDescription(t, "Keep this unsaved project description")
	if modal {
		b.wait(t, `(() => {
const toast = Alpine.$data(document.querySelector('[x-data="toast"]'));
return toast.visible && toast.type === 'success' &&
 toast.message === 'Project updated';
})()`)
	}
	b.screenshot(t, "stale-save-restored-"+scope+"-"+role)
}
