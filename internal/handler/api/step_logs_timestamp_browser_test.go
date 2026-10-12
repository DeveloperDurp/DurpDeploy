//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func (f *artifactE2E) verifyStreamingLogTimestamp(
	t *testing.T,
	browser *packageBrowser,
	deployment db.Deployment,
) {
	t.Helper()
	// Given: output that arrived through SSE after the first page load.
	browser.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).panels[1].live.some(entry => entry.line === 'second-only')`,
	)
	var logID int64
	decodeStepLogTest(t, browser.evaluate(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).panels[1].live.find(entry => entry.line === 'second-only').id`,
	), &logID)
	var stored db.DeploymentLog
	decodeStepLogTest(t, f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d/logs/%d", deployment.ID, logID,
	), nil, 200), &stored)
	timestamp := "[" + time.Unix(stored.CreatedAt, 0).
		Format("2006-01-02 15:04:05") + "] "
	assertion := fmt.Sprintf(
		`document.querySelector('[data-log-id="%d"] .text-gray-500')?.textContent === %q`,
		logID,
		timestamp,
	)
	// Then: the streamed entry already has its persisted display time.
	if string(browser.evaluate(t, assertion)) != "true" {
		t.Fatal("streamed log timestamp differs from its persisted time")
	}
	browser.captureStepLogs(t, "timestamp-live")
	// When: refreshing and reconnecting while the second step is active.
	browser.openStepLogPage(t, fmt.Sprintf(
		"%s/deployments/%d", f.baseURL, deployment.ID,
	))
	browser.wait(t, assertion)
	browser.captureStepLogs(t, "timestamp-refreshed")
	browser.evaluate(
		t,
		`(() => { const stream = Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')); stream.source.close(); stream.init(); return true; })()`,
	)
	browser.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source.readyState === 1`,
	)
	// Then: the timestamp remains unchanged and replay does not duplicate it.
	if string(browser.evaluate(t, fmt.Sprintf(
		`%s && document.querySelectorAll('[data-log-id="%d"]').length === 1`,
		assertion, logID,
	))) != "true" {
		t.Fatal("refresh/reconnect changed or duplicated the log timestamp")
	}
}
