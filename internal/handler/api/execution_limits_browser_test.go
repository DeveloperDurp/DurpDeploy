//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestStepExecutionLimitsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	checkStepExecutionLimitsBrowser(t, f)
}

func TestConfiguredExecutionPrecisionBrowserE2E(t *testing.T) {
	const retries int64 = 9007199254740993
	f := newExecutionLimitsFixture(t, retries, 600)
	var saved struct{ Runbook db.Runbook }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/runbooks",
			map[string]any{
				"name":  "Exact retries",
				"steps": []any{limitsStep(600, retries)},
			},
			201,
		),
		&saved,
	)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	book := fmt.Sprintf(
		"/projects/%d/runbooks/%d",
		f.project.ID,
		saved.Runbook.ID,
	)
	for _, edit := range []bool{false, true} {
		b.navigateBackTest(t, f.baseURL+book+"/edit")
		b.wait(
			t,
			`window.Alpine && document.querySelector('#runbook-version-form input[name=step_retries]')?.value === '9007199254740993'`,
		)
		if edit {
			b.evaluate(
				t,
				`document.querySelector('#runbook-version-form button.btn-secondary').click(); true`,
			)
			b.wait(
				t,
				`document.querySelector('dialog')?.matches(':modal') && document.querySelector('dialog [name=step_retries]')?.value === '9007199254740993'`,
			)
			b.evaluate(
				t,
				`document.querySelector('dialog form').requestSubmit(); true`,
			)
			b.wait(t, `!document.querySelector('dialog').open`)
		}
		b.captureNavigation(t, fmt.Sprintf("exact-retry-edit-%t", edit))
		b.evaluate(
			t,
			`document.querySelector('#runbook-version-form').requestSubmit(); true`,
		)
		b.waitBackTestURL(t, f.baseURL+book)
		var detail struct{ Versions []db.RunbookVersion }
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"GET",
				f.base()+fmt.Sprintf("/runbooks/%d", saved.Runbook.ID),
				nil,
				200,
			),
			&detail,
		)
		latest := db.RunbookVersion{}
		for _, version := range detail.Versions {
			if version.Version > latest.Version {
				latest = version
			}
		}
		var version struct {
			Steps []struct {
				MaxRetries int64 `json:"max_retries"`
			}
		}
		body := f.api(
			t,
			"GET",
			fmt.Sprintf(
				"%s/runbooks/%d/versions/%d",
				f.base(),
				saved.Runbook.ID,
				latest.ID,
			),
			nil,
			200,
		)
		require.NoError(t, json.Unmarshal(body, &version))
		require.Len(t, version.Steps, 1)
		require.Equal(t, retries, version.Steps[0].MaxRetries)
	}
}

func TestConfiguredExecutionLimitsBrowserE2E(t *testing.T) {
	for _, limits := range []struct{ retries, timeout int64 }{{0, 3}, {11, 172800}} {
		t.Run(
			fmt.Sprintf("%d/%d", limits.retries, limits.timeout),
			func(t *testing.T) {
				checkStepExecutionLimitsBrowser(
					t,
					newExecutionLimitsFixture(
						t,
						limits.retries,
						limits.timeout,
					),
				)
			},
		)
	}
}

func checkStepExecutionLimitsBrowser(t *testing.T, f *artifactE2E) {
	t.Helper()
	limits := f.h.repo.ResourceLimits().Execution
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/steps-page", f.baseURL, f.project.ID),
	)
	b.wait(
		t,
		`document.querySelector('[x-ref="addStepButton"]') && window.Alpine`,
	)
	b.evaluate(
		t,
		`document.querySelector('[x-ref="addStepButton"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content [name=timeout_seconds]')`,
	)
	b.evaluate(t, `(() => {
 const form=document.querySelector('#step-edit-content form');
 const fields={name:'bounded',script_body:'echo bounded',container_image:'harbor.durp.info/dockerhub/library/bash:5.2'};
 for (const [name,value] of Object.entries(fields)) form.querySelector('[name='+name+']').value=value;
 return true;
})()`)
	for _, name := range []string{"timeout_seconds", "max_retries"} {
		maximum := limits.MaxTimeoutSeconds
		if name == "max_retries" {
			maximum = limits.MaxRetries
		}
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => { const input=document.querySelector('#step-edit-content [name=%s]'); input.value=%q; document.querySelector('#step-edit-content form').requestSubmit(); return true; })()`,
				name,
				fmt.Sprint(maximum+1),
			),
		)
		if string(
			b.evaluate(
				t,
				fmt.Sprintf(
					`document.querySelector('#step-edit-content [name=%s]').max === %q && document.querySelector('#step-edit-content [name=%s]').validity.rangeOverflow && document.querySelector('#step-edit-dialog').open`,
					name,
					fmt.Sprint(maximum),
					name,
				),
			),
		) != "true" {
			t.Fatalf("%s did not enforce maximum", name)
		}
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('#step-edit-content [name=%s]').value=%q; true`,
				name,
				fmt.Sprint(maximum),
			),
		)
	}
	b.captureNavigation(t, "step-execution-limits")
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content form').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.querySelector('#step-list').textContent.includes('bounded')`,
	)
	var listed struct {
		Items []struct {
			TimeoutSeconds int64 `json:"timeout_seconds"`
			MaxRetries     int64 `json:"max_retries"`
		}
	}
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/steps", nil, 200),
		&listed,
	)
	if len(listed.Items) != 1 ||
		listed.Items[0].TimeoutSeconds != limits.MaxTimeoutSeconds ||
		listed.Items[0].MaxRetries != limits.MaxRetries {
		t.Fatalf("saved steps=%+v", listed)
	}
	var saved struct{ Runbook db.Runbook }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/runbooks",
			map[string]any{
				"name":  "bounded runbook",
				"steps": []any{limitsStep(0, 0)},
			},
			201,
		),
		&saved,
	)
	book := fmt.Sprintf(
		"/projects/%d/runbooks/%d",
		f.project.ID,
		saved.Runbook.ID,
	)
	b.navigateBackTest(t, f.baseURL+book+"/edit")
	b.wait(
		t,
		`document.querySelector('#runbook-version-form button.btn-secondary') && window.Alpine`,
	)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form button.btn-secondary').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('dialog')?.matches(':modal') && document.querySelector('dialog [name=step_timeout]')`,
	)
	for _, name := range []string{"step_timeout", "step_retries"} {
		maximum := limits.MaxTimeoutSeconds
		if name == "step_retries" {
			maximum = limits.MaxRetries
		}
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => {const input=document.querySelector('dialog [name=%s]');input.value=%q; input.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('dialog form').requestSubmit(); return true;})()`,
				name,
				fmt.Sprint(maximum+1),
			),
		)
		if string(
			b.evaluate(
				t,
				fmt.Sprintf(
					`document.querySelector('dialog [name=%s]').max === %q && document.querySelector('dialog [name=%s]').validity.rangeOverflow && document.querySelector('dialog').open`,
					name,
					fmt.Sprint(maximum),
					name,
				),
			),
		) != "true" {
			t.Fatalf("%s did not enforce maximum", name)
		}
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => {const input=document.querySelector('dialog [name=%s]');input.value=%q;input.dispatchEvent(new Event('input',{bubbles:true}));return true;})()`,
				name,
				fmt.Sprint(maximum),
			),
		)
	}
	b.captureNavigation(t, "runbook-execution-limits")
	b.evaluate(t, `document.querySelector('dialog form').requestSubmit(); true`)
	b.wait(t, `!document.querySelector('dialog').open`)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form').requestSubmit(); true`,
	)
	b.waitBackTestURL(t, f.baseURL+book)
	var detail struct{ Versions []db.RunbookVersion }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID),
			nil,
			200,
		),
		&detail,
	)
	if len(detail.Versions) != 2 {
		t.Fatalf("versions=%d", len(detail.Versions))
	}
}
