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
	fixture.assertCount(t, 1)
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
		fixture.assertCount(t, index+1)
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
		if !next.Note.Valid ||
			next.Note.String != fmt.Sprintf(
				"Re-run of #%d",
				fixture.original.ID,
			) {
			t.Fatal("confirmed re-run lost its original deployment lineage")
		}
		fixture.assertCount(t, index+2)

		b.evaluate(t, "history.back(); true")
		b.waitBackTestURL(t, f.baseURL+path)
		t.Logf("browser Back returned to original %s", path)
		fixture.assertCount(t, index+2)
		b.screenshot(t, fmt.Sprintf("rerun-browser-back-%d", width))

		b.evaluate(t, "history.forward(); true")
		b.waitBackTestURL(t, f.baseURL+nextPath)
		t.Logf("browser Forward returned to re-run %s", nextPath)
		fixture.assertCount(t, index+2)

		b.wire.events = nil
		b.call(t, "Page.reload", struct{}{}, &struct{}{})
		if err := b.wire.waitEvent(
			"Page.frameStoppedLoading", b.session,
		); err != nil {
			t.Fatal(err)
		}
		b.waitBackTestURL(t, f.baseURL+nextPath)
		t.Logf("reload retained re-run %s", nextPath)
		fixture.assertCount(t, index+2)
	}
}

func (fixture rerunInputsFixture) assertCount(t *testing.T, want int) {
	t.Helper()
	f := fixture.f
	var listed struct {
		Items []db.Deployment
		Total int
	}
	decodeStepLogTest(t,
		f.api(t, "GET", f.base()+"/deployments", nil, 200), &listed)
	if listed.Total != want || len(listed.Items) != want {
		t.Fatalf("deployment count = %d/%d, want %d",
			listed.Total, len(listed.Items), want)
	}
	ids := make([]int64, 0, len(listed.Items))
	for _, item := range listed.Items {
		ids = append(ids, item.ID)
	}
	t.Logf("deployment count=%d, IDs=%v", listed.Total, ids)
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
