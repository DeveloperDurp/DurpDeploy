//go:build e2e && packagebrowser

package api_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func (f *artifactE2E) assertBrowserLogBytes(
	t *testing.T, b *packageBrowser, target logHistoryTarget,
) {
	t.Helper()
	deploymentID, path, component := target.DeploymentID, target.WebPath, target.Component
	// Given: long Unicode history and an active stream with exact 64-bit IDs.
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: deploymentID, Status: "running",
		}); err != nil {
		t.Fatal(err)
	}
	line := "byte-preview " + strings.Repeat("界😀\t\"<&", 3000) + "complete-tail"
	seed := func(count int) int64 {
		t.Helper()
		var last int64
		if err := f.h.repo.WithTx(t.Context(), func(q *db.Queries) error {
			for range count {
				row, err := q.CreateDeploymentLog(t.Context(),
					db.CreateDeploymentLogParams{
						DeploymentID: deploymentID, Line: line,
					})
				if err != nil {
					return err
				}
				last = row.ID
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return last
	}
	seed(80)
	b.navigateBackTest(t, f.baseURL+path)
	selector := fmt.Sprintf(
		`document.querySelector('[x-data^="%s"]')`,
		component,
	)
	b.wait(
		t,
		selector+` && window.Alpine && document.querySelector('[data-log-id]')`,
	)
	// When: live output exceeds the byte budget before reaching 500 entries.
	last := seed(80)
	f.h.runner.Broker().Broadcast(deploymentID, "wake")
	b.wait(t, fmt.Sprintf(`document.querySelector('[data-log-id="%d"]')`, last))
	b.wait(t, fmt.Sprintf(`(() => {
		const state = Alpine.$data(%s);
		const rows = [...document.querySelectorAll('[data-log-id]')];
		return state.retainedBytes <= state.byteLimit && rows.length > 0 &&
			rows.length < 500 && new Set(rows.map(row => row.dataset.logId)).size === rows.length &&
			rows.every(row => !row.textContent.includes('complete-tail')) &&
			new URL(document.querySelector('[data-older-logs]').href).searchParams.get('before') === state.retained[0].id &&
			!document.querySelector('img[src=x]');
	})()`, selector))
	// Then: displayed previews fit the budget at every supported viewport.
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900,
			"deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.evaluate(
			t,
			`document.querySelector('[aria-label="Log history"]').scrollIntoView(); true`,
		)
		var state struct {
			Bytes    int
			Overflow bool
		}
		if err := json.Unmarshal(b.evaluate(t, `(() => ({
			Bytes: [...document.querySelectorAll('[data-log-id]')].reduce((sum, row) => sum + new TextEncoder().encode(row.textContent).length, 0),
			Overflow: document.documentElement.scrollWidth > innerWidth
		}))()`), &state); err != nil {
			t.Fatal(err)
		}
		if state.Bytes > 1<<20 || state.Overflow {
			t.Fatalf(
				"width=%d bytes=%d overflow=%t",
				width,
				state.Bytes,
				state.Overflow,
			)
		}
		b.logPreviewScreenshot(
			t,
			fmt.Sprintf("%s-byte-preview-%d", component, width),
		)
	}
	b.evaluate(
		t,
		`document.querySelector('[data-log-id]').scrollIntoView({block:'end'}); true`,
	)
	b.logPreviewScreenshot(t, component+"-byte-line-end")
	b.evaluate(t, `document.querySelector('[data-older-logs]').click(); true`)
	b.wait(
		t,
		`location.search.includes('before=') && document.readyState === 'complete'`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`%s === null || Alpine.$data(%s).source === null`,
			selector,
			selector,
		),
	)
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900,
			"deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.evaluate(
			t,
			`document.querySelector('[aria-label="Log history"]').scrollIntoView(); true`,
		)
		b.logPreviewScreenshot(
			t,
			fmt.Sprintf("%s-byte-older-%d", component, width),
		)
	}
	b.evaluate(t, `document.querySelector('[data-latest-logs]').click(); true`)
	b.wait(
		t,
		fmt.Sprintf(
			`location.search === '' && document.querySelector('[data-log-id="%d"]')`,
			last,
		),
	)
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: deploymentID, Status: "succeeded",
		}); err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(deploymentID, "complete")
	b.wait(t, fmt.Sprintf(`Alpine.$data(%s).source === null`, selector))
}

// A viewport capture preserves layout evidence without rasterizing all history.
func (b *packageBrowser) logPreviewScreenshot(t *testing.T, name string) {
	t.Helper()
	b.evaluate(t, `(async () => {
		await document.fonts.ready;
		await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
		return true;
	})()`)
	var capture struct{ Data string }
	b.call(t, "Page.captureScreenshot", map[string]any{
		"format": "png", "captureBeyondViewport": false,
	}, &capture)
	image, err := base64.StdEncoding.DecodeString(capture.Data)
	if err != nil {
		t.Fatal(err)
	}
	directory := os.Getenv("DURPDEPLOY_PACKAGE_UI_EVIDENCE")
	if directory == "" {
		directory = t.TempDir()
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".png"), image, 0600); err != nil {
		t.Fatal(err)
	}
}
