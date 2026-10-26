//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestConfiguredLogsBrowserE2E(t *testing.T) {
	for _, raised := range []bool{false, true} {
		t.Run(fmt.Sprint(raised), func(t *testing.T) {
			limits := configuredLogLimits(raised)
			if !raised {
				limits.PageRows = 70
			}
			f := newConfiguredLogE2E(t, limits)
			b := startPackageBrowser(t)
			b.setStepLogSession(t, f.baseURL, f.session)
			for _, runbook := range []bool{false, true} {
				t.Run(fmt.Sprint(runbook), func(t *testing.T) {
					testConfiguredLogBrowser(t, f, b, limits, runbook)
				})
			}
		})
	}
}

func testConfiguredLogBrowser(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
	limits config.LogLimits,
	runbook bool,
) {
	t.Helper()
	target := configuredLogTarget(t, f, runbook)
	line := seedConfiguredLogRows(t, f, target.DeploymentID, limits)
	require.NoError(
		t,
		f.h.repo.Queries.UpdateDeploymentStatus(
			t.Context(),
			db.UpdateDeploymentStatusParams{
				ID: target.DeploymentID, Status: "running",
			},
		),
	)
	b.navigateBackTest(t, f.baseURL+target.WebPath)
	var initial repository.LogPage
	decodeStepLogTest(
		t,
		f.api(t, "GET", target.APIPath+"/page", nil, 200),
		&initial,
	)
	selector := fmt.Sprintf(
		`document.querySelector('[x-data^="%s"]')`,
		target.Component,
	)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf(
				"configured log browser state: %s",
				b.evaluate(t, fmt.Sprintf(`(() => {
 const root = %s;
 if (!root) return {missing: true};
 const state = Alpine.$data(root);
 return {lastID:state.lastID, count:state.retained.length, bytes:state.retainedBytes,
  source:state.source?.readyState, lastDOM:[...document.querySelectorAll('[data-log-id]')].at(-1)?.dataset.logId};
})()`, selector)),
			)
		}
	})
	b.wait(
		t,
		fmt.Sprintf(
			`window.Alpine && %s && document.querySelectorAll('[data-log-id]').length === %d`,
			selector,
			len(initial.Items),
		),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`Alpine.$data(%s).limit === %d && Alpine.$data(%s).byteLimit === %d`,
			selector,
			limits.PageRows,
			selector,
			limits.PageBytes,
		),
	)
	var last int64
	// Cross the raised 65-row query batch and both byte retention budgets.
	for range 66 {
		row, err := f.h.repo.Queries.CreateDeploymentLog(
			t.Context(),
			db.CreateDeploymentLogParams{
				DeploymentID: target.DeploymentID, Line: line,
			},
		)
		require.NoError(t, err)
		last = row.ID
	}
	f.h.broker.Broadcast(target.DeploymentID, "wake")
	b.waitFor(
		t,
		fmt.Sprintf(`document.querySelector('[data-log-id="%d"]')`, last),
		45*time.Second,
	)
	b.wait(t, fmt.Sprintf(`(() => {
 const state = Alpine.$data(%s);
 const rows = [...document.querySelectorAll('[data-log-id]')];
 return state.retainedBytes <= %d && rows.length > 0 && rows.length < %d &&
  new Set(rows.map(row => row.dataset.logId)).size === rows.length &&
  rows.every(row => !row.textContent.includes('complete-configured-tail')) &&
  new URL(document.querySelector('[data-older-logs]').href).searchParams.get('before') === state.retained[0].id;
})()`, selector, limits.PageBytes, limits.PageRows))
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		require.Equal(
			t,
			"true",
			string(
				b.evaluate(
					t,
					`document.documentElement.scrollWidth <= innerWidth`,
				),
			),
		)
		b.evaluate(
			t,
			`document.querySelector('[aria-label="Log history"]').scrollIntoView(); true`,
		)
		b.logPreviewScreenshot(
			t,
			fmt.Sprintf(
				"configured-%s-%d-%d",
				target.Component,
				limits.LineBytes,
				width,
			),
		)
	}
	b.evaluate(t, `document.querySelector('[data-older-logs]').click(); true`)
	b.wait(
		t,
		`location.search.includes('before=') && document.readyState === 'complete' && document.body.innerText.includes('Live updates are paused')`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`%s === null || Alpine.$data(%s).source === null`,
			selector,
			selector,
		),
	)
	b.evaluate(t, `document.querySelector('[data-latest-logs]').click(); true`)
	b.wait(
		t,
		fmt.Sprintf(
			`location.search === '' && document.querySelector('[data-log-id="%d"]')`,
			last,
		),
	)
	require.NoError(
		t,
		f.h.repo.Queries.UpdateDeploymentStatus(
			t.Context(),
			db.UpdateDeploymentStatusParams{
				ID: target.DeploymentID, Status: "succeeded",
			},
		),
	)
	f.h.broker.Broadcast(target.DeploymentID, "complete")
	b.wait(t, fmt.Sprintf(`Alpine.$data(%s).source === null`, selector))
}
