//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestVariableGroupEditBrowserE2E(t *testing.T) {
	f := newVariableGroupFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	list := strings.TrimPrefix(f.base(), "/api/v1") + "/variables"
	for _, width := range []int{320, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 800, "deviceScaleFactor": 1,
			"mobile": width == 320,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+list)
		b.evaluate(
			t,
			`window.variableGroupTestList = document.querySelector('#variables-content')`,
		)
		openVariableGroup(t, b, "APP_CONFIG")
		b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('[data-variable-group-form]');
 form.elements.namedItem('value_%d').value = 'discarded';
 form.querySelector('button[type="button"]').click();
 return true;
})()`, f.dev))
		b.wait(t, `!document.querySelector('[data-variable-group-form]') &&
 document.activeElement.matches('[data-variable-group-actions] summary')`)
		openVariableGroup(t, b, "APP_CONFIG")
		b.waitBackTestURL(t, f.baseURL+list)

		devValue := fmt.Sprintf("dev-%d", width)
		prdValue := fmt.Sprintf("prd-secret-%d", width)
		b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('[data-variable-group-form]');
 const dev = form.elements.namedItem('value_%d');
 const prd = form.elements.namedItem('value_%d');
 if (dev.value === 'discarded' || prd.type !== 'password' || prd.value !== '' ||
     document.documentElement.scrollWidth > innerWidth) {
   throw new Error('cancel, secret input, or viewport contract failed');
 }
 const rowSelector = innerWidth < 768 ? 'li[data-mobile-variable]' : 'tr[id^="variable-row-"]';
 for (const [input, scope] of [[dev, 'dev'], [prd, 'prd']]) {
   if (!input.parentElement.matches('[data-variable-value]') ||
       !input.closest(rowSelector)?.textContent.includes(scope) ||
       form.contains(input)) {
     throw new Error('editable value did not replace its original display');
   }
 }
 dev.value = %q;
 prd.value = %q;
 form.requestSubmit();
 return true;
})()`, f.dev, f.prd, devValue, prdValue))
		b.wait(t, fmt.Sprintf(`(() => {
 const groups = [...document.querySelectorAll('[data-variable-group="APP_CONFIG"]')];
 return document.querySelector('#variables-content') === window.variableGroupTestList &&
   !document.querySelector('[data-variable-group-form]') &&
   groups.length === 2 && groups.every(group => group.textContent.includes(%q)) &&
   document.activeElement.matches('[data-variable-group-actions] summary');
})()`, devValue))
		b.waitBackTestURL(t, f.baseURL+list)
		f.assertValues(t, devValue, prdValue)
	}
	assertVariableGroupNewFirstEnvironment(t, b, f)
	assertVariableGroupInlineConflict(t, b, f)
	b.waitBackTestURL(t, f.baseURL+list)
}

func openVariableGroup(t *testing.T, b *packageBrowser, name string) {
	t.Helper()
	selector := fmt.Sprintf(`[data-variable-group="%s"]`, name)
	b.wait(
		t,
		fmt.Sprintf(`[...document.querySelectorAll(%q)]
 .some(summary => summary.getClientRects().length &&
   !summary.closest('[data-variable-group]').classList.contains('htmx-settling'))`,
			selector+" [data-variable-group-actions] summary"),
	)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const group = [...document.querySelectorAll(%q)].find(e => e.getClientRects().length);
 const menus = [...group.querySelectorAll('summary[data-variable-action="menu"]')].filter(e => e.getClientRects().length);
 if (menus.length !== 1) throw new Error('expected only the heading menu');
 menus[0].click();
 group.querySelector('[data-variable-group-edit]').click();
 return true;
})()`, selector))
	b.wait(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('[data-variable-group-form]');
 const group = form?.closest('[data-variable-group]');
 return form?.getClientRects().length > 0 &&
   group?.dataset.variableGroup === %q &&
   !group.classList.contains('htmx-settling') &&
   document.activeElement.matches('[data-variable-group-heading]');
})()`, name))
}

func assertVariableGroupNewFirstEnvironment(
	t *testing.T, b *packageBrowser, f variableGroupFixture,
) {
	t.Helper()
	f.api(t, "POST", f.base()+"/variables", map[string]any{
		"name": "OTHER", "value": "new-first", "environment_id": f.devEnvironment,
	}, 201)
	openVariableGroup(t, b, "OTHER")
	b.evaluate(
		t,
		`document.querySelector('[data-variable-group-form]').requestSubmit()`,
	)
	b.wait(t, `(() => {
 const groups = [...document.querySelectorAll('[data-variable-group="OTHER"]')];
 return groups.length === 2 && groups.every(group =>
   !group.querySelector('form[data-variable-group-form]') &&
   group.textContent.includes('new-first'));
})()`)
}

func assertVariableGroupInlineConflict(
	t *testing.T, b *packageBrowser, f variableGroupFixture,
) {
	t.Helper()
	openVariableGroup(t, b, "APP_CONFIG")
	f.api(t, "PUT", f.groupPath(), map[string]any{
		"overrides": []map[string]any{
			{"id": f.dev, "value": "newer-dev"},
			{"id": f.prd, "value": ""},
		},
	}, 204)
	b.evaluate(t, `(() => {
 document.querySelector('[data-variable-group-form]').requestSubmit();
 return true;
})()`)
	b.wait(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('[data-variable-group-form]');
 return form?.closest('[data-variable-group]').querySelector('[role="alert"]')?.textContent.includes('The variable group changed.') &&
   form.elements.namedItem('value_%d').value === 'newer-dev' &&
   form.elements.namedItem('value_%d').value === '';
})()`, f.dev, f.prd))
	f.assertValues(t, "newer-dev", "prd-secret-1280")
}
