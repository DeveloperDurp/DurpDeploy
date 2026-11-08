//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestProjectNamesBrowserE2E(t *testing.T) {
	f := newStaleProjectSaveFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	for _, path := range []string{
		"/projects/new", fmt.Sprintf("/projects/%d/edit", f.project.ID),
	} {
		b.navigateBackTest(t, f.server.URL+path)
		b.evaluate(t, `(() => {
 const form = document.querySelector('[name=name]').form;
 form.querySelector('[name=name]').value = 'x'.repeat(100 * 1024);
 form.querySelector('[name=description]').value = 'Retained description draft';
 window.projectStatus = 0;
 document.body.addEventListener('htmx:afterRequest', e => {
   window.projectStatus = e.detail.xhr.status;
   window.projectReferrer = e.detail.xhr.getResponseHeader('Referrer-Policy');
 });
 form.requestSubmit();
 return true;
})()`)
		b.wait(
			t,
			`window.projectStatus === 422 && document.querySelector('[data-form-error]')?.textContent.includes('255 UTF-16 code units')`,
		)
		if string(
			b.evaluate(
				t,
				`window.projectReferrer === 'no-referrer' && document.querySelector('[name=description]').value === 'Retained description draft' && document.querySelector('[name=name]').value.length === 100 * 1024`,
			),
		) != "true" {
			t.Fatal("name rejection discarded draft or omitted referrer policy")
		}
		b.screenshot(t, fmt.Sprintf("project-name-error-%d", len(path)))
		b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('[name=name]').form;
 form.querySelector('[name=name]').value = %q;
 form.requestSubmit();
 return true;
})()`, "Browser corrected "+path))
		b.wait(
			t,
			`!document.querySelector('[data-form-error]') && !document.querySelector('[name=description]') && location.pathname.match(/^\/projects(\/\d+)?$/)`,
		)
		b.screenshot(t, fmt.Sprintf("project-name-corrected-%d", len(path)))
	}
}
