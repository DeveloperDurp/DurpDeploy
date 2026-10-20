//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestVariableNamesBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(
			t,
			fmt.Sprintf("%s/projects/%d/variables", f.baseURL, f.project.ID),
		)
		if string(b.evaluate(t, `(() => {
 const input = document.querySelector('#variables-content input[name=name]');
 input.value = 'bad name';
 input.form.querySelector('[name=value]').value = 'keep-draft';
 return input.validity.patternMismatch && input.maxLength === 255 &&
 input.title.includes('ASCII') && input.getAttribute('aria-description').includes('underscore') &&
 input.parentElement.querySelector('[data-variable-name-help]').textContent.includes('255');
})()`)) != "true" {
			t.Fatal(
				"Name field does not explain or apply the identifier policy",
			)
		}
		// Bypass native validation to verify the HTTP boundary and draft retention.
		b.evaluate(
			t,
			`(() => { const form = document.querySelector('#variables-content form'); window.namesForm = form; form.noValidate = true; form.requestSubmit(); return true; })()`,
		)
		b.wait(
			t,
			`(() => { const toast = document.querySelector('[x-data=toast]'); return Alpine.$data(toast).visible && Alpine.$data(toast).message.includes('1-255 ASCII') && Alpine.$data(toast).message.includes('starting with a letter or underscore'); })()`,
		)
		if string(b.evaluate(t, `(() => {
 const form = document.querySelector('#variables-content form');
 return form === window.namesForm && form.querySelector('[name=name]').value === 'bad name' &&
 form.querySelector('[name=value]').value === 'keep-draft' &&
 Alpine.$data(document.querySelector('[x-data=toast]')).type === 'error' &&
 document.documentElement.scrollWidth <= innerWidth;
})()`)) != "true" {
			t.Fatal(
				"server name rejection discarded the draft or error feedback",
			)
		}
		b.screenshot(t, fmt.Sprintf("variable-names-%d", width))
		name := fmt.Sprintf("Valid_%d", width)
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => { const form = document.querySelector('#variables-content form'); form.querySelector('[name=name]').value = %q; form.noValidate = false; form.requestSubmit(); return true; })()`,
				name,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`!document.querySelector('.htmx-request,.htmx-settling') && document.querySelector('#variables-content').textContent.includes(%q)`,
				name,
			),
		)
		checkVariableNameEdit(t, b, name, width)
	}
}

func checkVariableNameEdit(
	t *testing.T,
	b *packageBrowser,
	name string,
	width int,
) {
	t.Helper()
	b.evaluate(t, fmt.Sprintf(`(() => {
 const record = [...document.querySelectorAll('tr[id^="variable-row-"], [data-mobile-variable]')].find(e => e.getClientRects().length && e.textContent.includes(%q));
 record.querySelector('[data-variable-action=edit]').click();
 return true;
})()`, name))
	b.wait(
		t,
		`[...document.querySelectorAll('form[hx-put]')].some(f => f.elements.namedItem('name')?.getClientRects().length)`,
	)
	b.evaluate(t, `(() => {
 const form = [...document.querySelectorAll('form[hx-put]')].find(f => f.elements.namedItem('name')?.getClientRects().length);
 window.namesEditForm = form;
 const input = form.elements.namedItem('name');
 input.value = '1BAD';
 if (!input.validity.patternMismatch || !input.title.includes('ASCII')) throw new Error('edit has no name policy');
 form.elements.namedItem('value').value = 'keep-edit-draft';
 form.noValidate = true;
 form.requestSubmit();
 return true;
})()`)
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data=toast]')).message.includes('starting with a letter or underscore')`,
	)
	if string(
		b.evaluate(
			t,
			`window.namesEditForm.isConnected && window.namesEditForm.elements.namedItem('name').value === '1BAD' && window.namesEditForm.elements.namedItem('value').value === 'keep-edit-draft'`,
		),
	) != "true" {
		t.Fatal("name rejection discarded the edit draft")
	}
	b.screenshot(t, fmt.Sprintf("variable-names-edit-%d", width))
	corrected := fmt.Sprintf("Edited_%d", width)
	b.evaluate(
		t,
		fmt.Sprintf(
			`(() => { const form = window.namesEditForm; form.elements.namedItem('name').value = %q; form.noValidate = false; form.requestSubmit(); return true; })()`,
			corrected,
		),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`!document.querySelector('.htmx-request,.htmx-settling') && document.querySelector('#variables-content').textContent.includes(%q)`,
			corrected,
		),
	)
}
