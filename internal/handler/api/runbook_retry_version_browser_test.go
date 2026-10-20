//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunbookRetryVersionBrowserE2E(t *testing.T) {
	fixture := newRunbookRetryVersion(t)
	f := fixture.f
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf(
		"/projects/%d/runbooks/executions/%d",
		f.project.ID,
		fixture.original.ID,
	)
	for index, width := range []int{375, 390, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+path)
		b.evaluate(
			t,
			`(() => {const original=window.confirm;window.confirm=message=>{window.retryMessage=message;return original(message)};return true;})()`,
		)
		confirmRunbookRetryVersion(t, b, false)
		var message string
		decodeStepLogTest(t, b.evaluate(t, `window.retryMessage`), &message)
		if !strings.Contains(message, "saved runbook version 1?") ||
			!strings.Contains(message, "Later runbook edits are not included") {
			t.Fatalf("Retry confirmation lost its pinned version: %s", message)
		}
		var page struct {
			Total int64
			Items []db.ListRunbookExecutionsRow
		}
		decodeStepLogTest(
			t,
			f.api(t, "GET", f.base()+"/runbook-executions", nil, 200),
			&page,
		)
		if page.Total != int64(index+2) {
			t.Fatal("Cancelling confirmation created an execution")
		}
		b.screenshot(t, fmt.Sprintf("runbook-retry-version-%d", width))
		confirmRunbookRetryVersion(t, b, true)
		b.wait(
			t,
			fmt.Sprintf(
				`location.pathname.startsWith('/projects/%d/runbooks/executions/') && location.pathname !== %q`,
				f.project.ID,
				path,
			),
		)
		var nextPath string
		decodeStepLogTest(t, b.evaluate(t, `location.pathname`), &nextPath)
		id, err := strconv.ParseInt(
			nextPath[strings.LastIndex(nextPath, "/")+1:],
			10,
			64,
		)
		if err != nil {
			t.Fatal(err)
		}
		decodeStepLogTest(
			t,
			f.api(t, "GET", f.base()+"/runbook-executions", nil, 200),
			&page,
		)
		if len(page.Items) == 0 || page.Items[0].ID != id {
			t.Fatal("Retry navigation and API identity differ")
		}
		row := page.Items[0]
		fixture.check(
			t,
			db.RunbookExecution{
				ID:               row.ID,
				RunbookVersionID: row.RunbookVersionID,
				DeploymentID:     row.DeploymentID,
			},
		)
	}
}

func confirmRunbookRetryVersion(t *testing.T, b *packageBrowser, accept bool) {
	t.Helper()
	b.wire.events = nil
	b.evaluate(
		t,
		`setTimeout(()=>document.querySelector('[x-data="runbookRetry"] button').click(),0);true`,
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
