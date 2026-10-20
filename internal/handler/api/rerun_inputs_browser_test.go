//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestRerunInputsBrowserE2E(t *testing.T) {
	fixture := newRerunInputsFixture(t)
	f := fixture.f
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/deployments/%d", fixture.original.ID)
	for index, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+path)
		var message string
		decodeStepLogTest(
			t,
			b.evaluate(
				t,
				`document.querySelector('#deployment-actions [hx-confirm]').getAttribute('hx-confirm')`,
			),
			&message,
		)
		for _, meaning := range []string{
			"current release", "environment", "original steps",
			"lifecycle variables", "Refreshing", "Original logs stay unchanged",
		} {
			if !strings.Contains(message, meaning) {
				t.Fatalf(
					"confirmation does not explain %q: %s",
					meaning,
					message,
				)
			}
		}
		confirmRerunInputs(t, b, false)
		var listed struct{ Items []db.Deployment }
		decodeStepLogTest(
			t,
			f.api(t, "GET", f.base()+"/deployments", nil, 200),
			&listed,
		)
		if len(listed.Items) != index+1 {
			t.Fatal("dismissed confirmation created a deployment")
		}
		b.screenshot(t, fmt.Sprintf("rerun-inputs-%d", width))
		confirmRerunInputs(t, b, true)
		b.wait(
			t,
			fmt.Sprintf(
				`location.pathname.startsWith('/deployments/') && location.pathname !== %q`,
				path,
			),
		)
		var nextPath string
		decodeStepLogTest(t, b.evaluate(t, `location.pathname`), &nextPath)
		id, err := strconv.ParseInt(
			strings.TrimPrefix(nextPath, "/deployments/"),
			10,
			64,
		)
		if err != nil {
			t.Fatal(err)
		}
		var next db.Deployment
		decodeStepLogTest(
			t,
			f.api(t, "GET", "/api/v1"+nextPath, nil, 200),
			&next,
		)
		if next.ID != id {
			t.Fatal("re-run navigation and API identity differ")
		}
		fixture.check(t, next)
	}
}

func confirmRerunInputs(t *testing.T, b *packageBrowser, accept bool) {
	t.Helper()
	b.wire.events = nil
	b.evaluate(
		t,
		`setTimeout(() => document.querySelector('#deployment-actions [hx-confirm]').click(), 0); true`,
	)
	if err := b.wire.waitEvent("Page.javascriptDialogOpening", b.session); err != nil {
		t.Fatal(err)
	}
	b.call(
		t,
		"Page.handleJavaScriptDialog",
		map[string]bool{"accept": accept},
		&struct{}{},
	)
}
