//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestVariableCancelBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		for _, secret := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/secret=%t", width, secret),
				func(t *testing.T) {
					checkVariableCancel(t, f, b, width, secret)
				})
		}
	}
}

func checkVariableCancel(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
	width int,
	secret bool,
) {
	t.Helper()
	name := fmt.Sprintf("CANCEL_%d_%t", width, secret)
	var variable struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST",
		f.base()+"/variables", map[string]any{
			"name": name, "value": "original-value",
			"secret": secret, "environment_id": f.environment.ID,
		}, 201), &variable)
	apiPath := fmt.Sprintf("%s/variables/%d",
		f.base(), variable.ID)
	before := string(f.api(t, "GET", apiPath, nil, 200))
	b.navigateBackTest(t, fmt.Sprintf(
		"%s/projects/%d/variables", f.baseURL, f.project.ID))
	selector := fmt.Sprintf("#variable-row-%d", variable.ID)
	if width == 375 {
		selector = fmt.Sprintf(
			"[data-mobile-variable='%d']", variable.ID)
	}
	b.evaluate(t, fmt.Sprintf(`(() => {
 window.cancelRecord = document.querySelector(%q);
 window.cancelForm = cancelRecord.querySelector('form');
 cancelRecord.querySelector('[data-variable-action=edit]').click();
 return true;
})()`, selector))
	b.wait(t, `!!cancelForm.getClientRects().length`)
	// Repeat cancellation to catch stale defaults after reopening.
	for range 2 {
		b.evaluate(t, `(() => {
 cancelForm.elements.namedItem('name').value = 'UNSAVED_NAME';
 cancelForm.elements.namedItem('value').value = 'unsaved-value';
 cancelForm.elements.namedItem('environment_id').value = '';
 cancelForm.elements.namedItem('secret').click();
 return true;
})()`)
		b.wait(t, fmt.Sprintf(
			`cancelForm.elements.namedItem('value').type === %q`,
			map[bool]string{false: "password", true: "text"}[secret],
		))
		b.evaluate(
			t,
			`[...cancelForm.querySelectorAll('button')].find(b => b.textContent === 'Cancel').click(); true`,
		)
		b.wait(t, `!cancelForm.getClientRects().length`)
		b.evaluate(
			t,
			`cancelRecord.querySelector('[data-variable-action=edit]').click(); true`,
		)
		value, valueType := "original-value", "text"
		if secret {
			value, valueType = "", "password"
		}
		b.wait(t, fmt.Sprintf(`(() => {
 const fields = cancelForm.elements;
 return !!cancelForm.getClientRects().length &&
 fields.namedItem('name').value === %q &&
 fields.namedItem('value').value === %q &&
 fields.namedItem('value').type === %q &&
 fields.namedItem('environment_id').value === %q &&
 fields.namedItem('secret').checked === %t;
})()`, name, value, valueType, fmt.Sprint(f.environment.ID), secret))
		if string(
			f.api(t, "GET", apiPath, nil, 200),
		) != before {
			t.Fatal("Cancel changed the stored variable")
		}
	}
	b.screenshot(
		t,
		fmt.Sprintf("variable-cancel-%d-%t", width, secret),
	)
	b.evaluate(
		t,
		`cancelForm.elements.namedItem('name').value += '_SAVED'; cancelForm.requestSubmit(); true`,
	)
	b.wait(
		t,
		`!cancelForm.isConnected && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	var saved struct {
		Name          string
		EnvironmentID int64 `json:"environment_id"`
		Secret        int64
	}
	decodeStepLogTest(
		t,
		f.api(t, "GET", apiPath, nil, 200),
		&saved,
	)
	if saved.Name != name+"_SAVED" ||
		saved.EnvironmentID != f.environment.ID ||
		(saved.Secret != 0) != secret {
		t.Fatalf("Save after Cancel changed fields: %+v", saved)
	}
}
