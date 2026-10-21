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
 const line = parseFloat(getComputedStyle(note).lineHeight);
 return note?.textContent === %q && box.height > 0 &&
 box.height <= 2 * line + 1 &&
 (note.textContent.includes('final-note-marker') ? note.scrollHeight > note.clientHeight : true) &&
 note.scrollWidth <= note.clientWidth &&
 getComputedStyle(note).whiteSpace === 'pre-wrap' &&
 !document.querySelector('img[src=x]');
})()`, deployment.id, deployment.note))) != "true" {
				t.Fatal(
					"history note previews exceed two lines or lose saved text/escaping",
				)
			}
		}
	})
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('[data-deployment-id="%d"] a').click(); true`,
		fixture.incident.ID,
	))
	b.wait(t, `document.querySelector('[data-deployment-note-full]')`)
	b.captureNavigation(t, "deployment-note-full", func() {
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const note = document.querySelector('[data-deployment-note-full]');
 const line = parseFloat(getComputedStyle(note).lineHeight);
 return note.textContent.trim() === %q && note.clientHeight > 2 * line &&
 note.scrollHeight <= note.clientHeight && note.scrollWidth <= note.clientWidth &&
 getComputedStyle(note).whiteSpace === 'pre-wrap' && !document.querySelector('img[src=x]');
})()`, fixture.incident.Note.String))) != "true" {
			t.Fatal("opening a history card lost the complete readable note")
		}
	})
}
