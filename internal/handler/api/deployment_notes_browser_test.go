//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestDeploymentNotesBrowserE2E(t *testing.T) {
	fixture := newDeploymentNotes(t)
	f := fixture.f
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, fmt.Sprintf(
		"%s/deployments?project_id=%d&limit=1", f.baseURL, f.project.ID))
	b.wait(t, `document.querySelectorAll('[data-deployment-id]').length === 1`)
	if string(
		b.evaluate(
			t,
			`document.querySelector('[data-deployment-note]') === null`,
		),
	) != "true" {
		t.Fatal("a deployment without a note shows an empty note field")
	}
	for count := 2; count <= 3; count++ {
		b.wait(t, `!document.querySelector('.htmx-request, .htmx-settling')`)
		b.evaluate(
			t,
			`document.querySelector('button[hx-target="#deployments-tbody"]').click(); true`,
		)
		b.wait(t, fmt.Sprintf(
			`document.querySelectorAll('[data-deployment-id]').length === %d`,
			count,
		))
	}
	b.captureNavigation(t, "deployment-notes", func() {
		for _, deployment := range []struct {
			id   int64
			note string
		}{
			{fixture.incident.ID, fixture.incident.Note.String},
			{fixture.rerun.ID, fixture.rerun.Note.String},
		} {
			if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const note = document.querySelector('[data-deployment-id="%d"] [data-deployment-note]');
 const box = note?.getBoundingClientRect();
 return note?.textContent === %q && box.height > 0 &&
 note.scrollWidth <= note.clientWidth &&
 getComputedStyle(note).whiteSpace === 'pre-wrap' &&
 !document.querySelector('img[src=x]');
})()`, deployment.id, deployment.note))) != "true" {
				t.Fatal(
					"history notes are missing, clipped, or interpreted as markup",
				)
			}
		}
	})
}
