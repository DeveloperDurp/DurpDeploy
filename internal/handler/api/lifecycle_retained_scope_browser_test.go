//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func (b *packageBrowser) assertRetainedScope(
	t *testing.T, f *artifactE2E, lifecycleID int64,
) {
	t.Helper()
	base := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycleID)
	var variable struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", base+"/variables", map[string]any{
		"name": "RETAINED", "value": "browser-retained-secret", "secret": true,
		"environment_id": f.environment.ID,
	}, 201), &variable)
	stages, err := f.h.repo.Queries.ListLifecycleStages(
		t.Context(),
		lifecycleID,
	)
	if err != nil || len(stages) != 1 {
		t.Fatal("stage fixture", stages, err)
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/stages/%d/delete", base, stages[0].ID),
		nil,
		204,
	)
	b.navigateBackTest(t, fmt.Sprintf("%s/lifecycles/%d/variables/%d/edit",
		f.baseURL, lifecycleID, variable.ID))
	if string(
		b.evaluate(
			t,
			`!document.querySelector('#shared-environment').checkValidity() && document.querySelector('#shared-value').value === '' && !document.body.innerHTML.includes('browser-retained-secret')`,
		),
	) != "true" {
		t.Fatal("retained scope is not blocked or secret leaked")
	}
	b.evaluate(
		t,
		`document.querySelector('[data-shared-variable-form]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-shared-variable-form]').hasAttribute('hx-put') && !document.querySelector('.htmx-request')`,
	)
	b.captureNavigation(t, "lifecycle-retained-scope")
	b.evaluate(
		t,
		`document.querySelector('#shared-environment').value='all'; document.querySelector('[data-shared-variable-form]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('[data-shared-variable-form]').hasAttribute('hx-put')`,
	)
	b.captureNavigation(t, "lifecycle-retained-explicit-all")
	f.api(t, "POST", base+"/stages", map[string]int64{
		"environment_id": f.environment.ID,
	}, 201)
}
