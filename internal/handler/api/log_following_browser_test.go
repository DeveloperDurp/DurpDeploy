//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/google/uuid"
)

var logFollowingMarkers = [...]string{
	"initial-549", "first-live-", "second-live-", "wrapped-tail-end",
	"final-follow-marker",
}

func TestLogFollowingBrowserE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	session := os.Getenv("DURPDEPLOY_LIVE_SESSION")
	token := os.Getenv("DURPDEPLOY_LIVE_API_TOKEN")
	if base == "" {
		f := newArtifactE2E(t)
		base, session, token = f.baseURL, f.session, f.token
	}
	if session == "" || token == "" {
		t.Fatal("log following requires its test session and API token")
	}
	b := startLivePackageBrowser(t, base)
	b.setStepLogSession(t, base, session)
	b.navigateBackTest(t, base+"/")
	api := func(t *testing.T, method, path string, body any, want int, target any) {
		t.Helper()
		input, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Status int
			Body   json.RawMessage
		}
		decodeStepLogTest(t, b.evaluate(t, fmt.Sprintf(`(async () => {
 const response = await fetch(%q, {method:%q,
 headers:{Authorization:%q,'Content-Type':'application/json'},
 body:%s === null ? undefined : JSON.stringify(%s)});
 return {Status:response.status,Body:await response.json()};
})()`, path, method, "Bearer "+token, input, input)), &response)
		if response.Status != want {
			t.Fatalf("%s %s: %d, want %d: %s",
				method, path, response.Status, want, response.Body)
		}
		if target != nil {
			decodeStepLogTest(t, response.Body, target)
		}
	}
	var project struct{ ID int64 }
	api(t, "POST", "/api/v1/projects", map[string]string{
		"name": "Log following " + uuid.NewString(),
	}, 201, &project)
	var environment struct{ ID int64 }
	if id := os.Getenv("DURPDEPLOY_LIVE_ENV_ID"); id != "" {
		api(t, "GET", "/api/v1/environments/"+id, nil, 200, &environment)
	} else {
		api(t, "POST", "/api/v1/environments", map[string]string{
			"name": "Log following " + uuid.NewString(),
		}, 201, &environment)
	}
	projectPath := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	steps := []map[string]string{
		{
			"name": "First output",
			"script_body": `for ((i=0; i<550; i++)); do echo "initial-$i"; done
for ((i=0; i<100; i++)); do echo "first-live-$i"; sleep 0.15; done`,
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		},
		{
			"name": "Second output",
			"script_body": `for ((i=0; i<80; i++)); do echo "second-live-$i"; sleep 0.15; done
printf '%s\n' '` + strings.Repeat(
				"wrapped-tail ",
				600,
			) + `wrapped-tail-end'
sleep 8
echo final-follow-marker`,
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		},
	}
	for _, step := range steps {
		api(t, "POST", projectPath+"/steps", step, 201, nil)
	}
	var release db.Release
	api(t, "POST", projectPath+"/releases", map[string]string{
		"version": "log-following",
	}, 201, &release)
	var saved deletionRunbook
	api(t, "POST", projectPath+"/runbooks", map[string]any{
		"name": "Log following", "steps": steps,
	}, 201, &saved)

	f := logFollowingBrowserFixture{
		b: b, base: base, api: api, token: token,
		projectID: project.ID, environmentID: environment.ID,
		releaseID: release.ID, runbookID: saved.Runbook.ID,
	}
	for _, kind := range []string{"deployment", "runbook"} {
		// A failed case can still own the shared environment queue.
		if !t.Run(kind, func(t *testing.T) { f.check(t, kind) }) {
			return
		}
	}
}

type logFollowingBrowserFixture struct {
	b             *packageBrowser
	base          string
	api           func(*testing.T, string, string, any, int, any)
	token         string
	projectID     int64
	environmentID int64
	releaseID     int64
	runbookID     int64
}

func (f logFollowingBrowserFixture) logFailure(t *testing.T, logPath string) {
	t.Helper()
	if !t.Failed() {
		return
	}
	// Start all reads at failure, before a slow probe can skew the evidence.
	var reads sync.WaitGroup
	if logPath != "" {
		client := &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		reads.Go(func() {
			var execution struct {
				Status        string `json:"status"`
				QueuePosition int64  `json:"queue_position"`
			}
			path := strings.TrimSuffix(logPath, "/logs")
			if f.readLogDiagnostic(t, client, path, &execution) {
				switch execution.Status {
				case "pending", "queued", "running", "pending_approval",
					"awaiting_artifact_approval", "publishing_artifact",
					"succeeded", "failed", "cancelled", "cleanup_unconfirmed":
				default:
					execution.Status = "unknown"
				}
				t.Logf("API execution status=%s queue_position=%d",
					execution.Status, execution.QueuePosition)
			}
		})
		reads.Go(func() { f.logPersistedMarkers(t, client, logPath) })
	}
	f.logBrowserState(t)
	reads.Wait()
}

func (f logFollowingBrowserFixture) readLogDiagnostic(
	t *testing.T, client *http.Client, path string, target any,
) bool {
	t.Helper()
	// Cleanup runs after t.Context is canceled. Use the client timeout here.
	req, err := http.NewRequest(http.MethodGet, f.base+path, nil)
	if err != nil {
		t.Logf("API diagnostic %s: invalid request", path)
		return false
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	response, err := client.Do(req)
	if err != nil {
		t.Logf("API diagnostic %s: request failed (timeout=%t)",
			path, os.IsTimeout(err))
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Logf("API diagnostic %s: HTTP %d", path, response.StatusCode)
		return false
	}
	const maxBytes = 2 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes {
		t.Logf(
			"API diagnostic %s: response unreadable or over byte limit",
			path,
		)
		return false
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Logf("API diagnostic %s: invalid response JSON", path)
		return false
	}
	return true
}

func (f logFollowingBrowserFixture) logPersistedMarkers(
	t *testing.T, client *http.Client, logPath string,
) {
	t.Helper()
	markers := make(map[string]int64, len(logFollowingMarkers))
	for _, marker := range logFollowingMarkers {
		markers[marker] = 0
	}
	var before int64
	complete := false
	truncated := 0
	// Two pages cover the default fixture. Allow one more for smaller pages.
	for range 3 {
		var page repository.LogPage
		path := logPath + "/page"
		if before != 0 {
			path += fmt.Sprintf("?before=%d", before)
		}
		if !f.readLogDiagnostic(t, client, path, &page) {
			break
		}
		if page.Items == nil {
			t.Log("API log diagnostic: response missing items")
			break
		}
		t.Logf("API log page before=%d next_before=%d last_id=%d rows=%d",
			before, page.NextBefore, page.LastID, len(page.Items))
		truncated += recordLogFollowingMarkers(page.Items, markers)
		if page.NextBefore == 0 {
			complete = true
			break
		}
		before = page.NextBefore
	}
	t.Logf(
		"API persisted marker row IDs (0=unseen, complete=%t, truncated=%d) %v",
		complete,
		truncated,
		markers,
	)
}

func recordLogFollowingMarkers(
	rows []repository.LogPreview, markers map[string]int64,
) int {
	truncated := 0
	for _, row := range rows {
		if row.Truncated {
			truncated++
		}
		for _, marker := range logFollowingMarkers {
			if markers[marker] == 0 && strings.Contains(row.Line, marker) {
				markers[marker] = row.ID
			}
		}
	}
	return truncated
}

func (f logFollowingBrowserFixture) logBrowserState(t *testing.T) {
	t.Helper()
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct{} `json:"exceptionDetails"`
	}
	expression := fmt.Sprintf(`(() => {
 const root = document.querySelector('[x-bind="logFollowing"]');
 const state = root && window.Alpine?.$data(root);
 const surface = root ?? document;
 const rows = [...surface.querySelectorAll('[data-log-id]')];
 const rowID = value => /^\d{1,19}$/.test(value) ? String(value) : null;
 const markers = %q.split(',');
 const visibleEnd = rows.findLast(row => row.checkVisibility());
 return {
  hasLogRoot: !!root, hasFollowButton: !!surface.querySelector('[data-follow-logs]'),
  historical: new URLSearchParams(location.search).has('before'),
  following: !!state?.followingLogs, hidden: document.hidden,
  pending: !!state?.logUpdate, lastID: rowID(state?.lastID),
  eventSource: state?.source?.readyState ?? null,
  openPanels: [...surface.querySelectorAll('details[data-step-index]')]
    .slice(0, 2).map(panel => panel.open),
  rows: rows.length, oldestRowID: rowID(rows[0]?.dataset.logId),
  newestRowID: rowID(rows.at(-1)?.dataset.logId),
  visibleEndBottom: visibleEnd?.getBoundingClientRect().bottom ?? null,
  viewportHeight: innerHeight, y: scrollY,
  markers: Object.fromEntries(markers.map(marker => [marker,
    rowID(rows.find(row => row.textContent.includes(marker))?.dataset.logId)]))
 };
})()`, strings.Join(logFollowingMarkers[:], ","))
	if err := f.b.wire.call("Runtime.evaluate", f.b.session, map[string]any{
		"expression": expression, "returnByValue": true, "timeout": 5000,
	}, &result); err != nil || result.Exception != nil ||
		len(result.Result.Value) == 0 {
		t.Log("Browser follow state unavailable")
		return
	}
	t.Logf("Browser follow state: %s", result.Result.Value)
}

func (f logFollowingBrowserFixture) check(t *testing.T, kind string) {
	t.Helper()
	b, base, api := f.b, f.base, f.api
	projectPath := fmt.Sprintf("/api/v1/projects/%d", f.projectID)
	var webPath, logPath string
	var deploymentID int64
	t.Cleanup(func() { f.logFailure(t, logPath) })
	body := map[string]int64{"environment_id": f.environmentID}
	if kind == "deployment" {
		body["release_id"] = f.releaseID
		var deployment db.Deployment
		api(t, "POST", projectPath+"/deployments", body, 201, &deployment)
		deploymentID = deployment.ID
		webPath = fmt.Sprintf("/deployments/%d", deployment.ID)
		logPath = "/api/v1" + webPath + "/logs"
	} else {
		var execution db.RunbookExecution
		api(t, "POST", fmt.Sprintf("%s/runbooks/%d/executions",
			projectPath, f.runbookID), body, 201, &execution)
		deploymentID = execution.DeploymentID
		webPath = fmt.Sprintf("/projects/%d/runbooks/executions/%d",
			f.projectID, execution.ID)
		logPath = fmt.Sprintf("%s/runbook-executions/%d/logs",
			projectPath, execution.ID)
	}
	t.Logf("%s log example: %s%s", kind, base, webPath)
	t.Logf("Log fixture deployment_id=%d", deploymentID)
	width := 1280
	if kind == "runbook" {
		width = 375
	}
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": 900, "deviceScaleFactor": 1,
		"mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, base+webPath)
	b.waitFor(
		t,
		`document.querySelector('[data-follow-logs]') && document.querySelectorAll('[data-log-id]').length === 500`,
		time.Minute,
	)
	if kind == "deployment" {
		b.wait(
			t,
			`document.querySelectorAll('details[data-step-index="0"][open], details[data-step-index="1"][open]').length === 0`,
		)
		b.evaluate(
			t,
			`document.querySelectorAll('details[data-step-index]').forEach(panel => panel.open = true); true`,
		)
	}
	b.evaluate(
		t,
		`window.followRoot = document.querySelector('[x-bind="logFollowing"]');
window.followState = Alpine.$data(followRoot);
window.lastLog = () => [...followRoot.querySelectorAll('[data-log-id]')].filter(row => row.checkVisibility()).at(-1);
lastLog().scrollIntoView({block:'end',behavior:'instant'}); true`,
	)
	b.wait(t, `followState.followingLogs`)
	b.evaluate(t, `window.previousLast = followState.lastID; true`)
	b.wait(
		t,
		`BigInt(followState.lastID) > BigInt(previousLast) + 5n && lastLog().getBoundingClientRect().bottom <= innerHeight + 2`,
	)

	// Scroll away and keep the same surviving row in place during trimming.
	b.evaluate(
		t,
		`window.followAnchor = [...followRoot.querySelectorAll('[data-log-id]')].at(-100);
followAnchor.scrollIntoView({block:'start',behavior:'instant'}); true`,
	)
	b.wait(
		t,
		`!followState.followingLogs && followState.logUpdate === null && (
window.anchorTop = followAnchor.getBoundingClientRect().top,
window.previousOldest = followRoot.querySelector('[data-log-id]').dataset.logId,
true)`,
	)
	b.wait(
		t,
		`BigInt(followRoot.querySelector('[data-log-id]').dataset.logId) > BigInt(previousOldest) + 8n && followState.logUpdate === null`,
	)
	b.wait(
		t,
		`followState.logUpdate === null && followAnchor.isConnected && Math.abs(followAnchor.getBoundingClientRect().top - anchorTop) < 2 && !followState.followingLogs`,
	)

	// Keyboard activation resumes following without changing panel choices.
	b.evaluate(
		t,
		`document.querySelector('[data-follow-logs]').focus(); true`,
	)
	for _, event := range []string{"keyDown", "keyUp"} {
		text := ""
		if event == "keyDown" {
			text = "\r"
		}
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type": event, "key": "Enter", "code": "Enter",
			"windowsVirtualKeyCode": 13, "text": text,
		}, &struct{}{})
	}
	b.wait(
		t,
		`followState.followingLogs && lastLog().getBoundingClientRect().bottom <= innerHeight + 2`,
	)

	// Exercise the visibility boundary while real SSE output continues.
	b.evaluate(t, `window.hiddenScrolls = 0;
window.savedIntoView = Element.prototype.scrollIntoView;
window.savedScrollBy = window.scrollBy;
Element.prototype.scrollIntoView = function(...args) { hiddenScrolls++; return savedIntoView.apply(this,args); };
window.scrollBy = function(...args) { hiddenScrolls++; return savedScrollBy.apply(this,args); };
Object.defineProperty(document, 'hidden', {configurable:true,value:true});
document.dispatchEvent(new Event('visibilitychange'));
window.hiddenLast = followState.lastID; true`)
	b.wait(t, `BigInt(followState.lastID) > BigInt(hiddenLast) + 5n`)
	b.wait(t, `!followState.followingLogs && hiddenScrolls === 0`)
	b.evaluate(
		t,
		`Element.prototype.scrollIntoView = savedIntoView; window.scrollBy = savedScrollBy;
delete document.hidden; document.dispatchEvent(new Event('visibilitychange'));
lastLog().scrollIntoView({block:'end',behavior:'instant'}); true`,
	)
	b.wait(t, `followState.followingLogs`)

	// Both panels stay open as the next step receives output.
	// The first producer sleeps 100 times for 150 ms before the next step starts.
	b.waitFor(t, `lastLog()?.textContent.includes('second-live-')`, time.Minute)
	b.wait(
		t,
		`lastLog().getBoundingClientRect().bottom <= innerHeight + 2`,
	)
	if kind == "deployment" {
		b.wait(
			t,
			`document.querySelectorAll('details[data-step-index="0"][open], details[data-step-index="1"][open]').length === 2`,
		)
		// New output in a closed panel must not become the visible endpoint.
		b.evaluate(
			t,
			`document.querySelector('[data-step-index="1"]').open = false;
window.visibleEndpoint = [...document.querySelectorAll('[data-step-index="0"] [data-log-id]')].at(-1);
document.querySelector('[data-follow-logs]').click();
window.closedLast = followState.lastID; true`,
		)
		b.wait(
			t,
			`followState.followingLogs && visibleEndpoint.getBoundingClientRect().bottom > 0 && visibleEndpoint.getBoundingClientRect().bottom <= innerHeight + 2 && !document.querySelector('[data-step-index="1"]').open`,
		)
		b.wait(
			t,
			`BigInt(followState.lastID) > BigInt(closedLast) + 4n && visibleEndpoint.getBoundingClientRect().bottom <= innerHeight + 2 && !document.querySelector('[data-step-index="1"]').open`,
		)
		b.evaluate(
			t,
			`document.querySelector('[data-step-index="0"]').open = false;
document.querySelector('[data-step-index="1"]').open = true;
document.querySelector('[data-follow-logs]').click(); true`,
		)
	}
	// A wrapped latest row can span both viewport edges.
	// The producer first sleeps 80 times for 150 ms. Allow execution overhead
	// within the same bounded budget used to wait for its initial output.
	b.waitFor(
		t,
		`followRoot.textContent.includes('wrapped-tail-end')`,
		time.Minute,
	)
	b.evaluate(
		t,
		`window.tallRow = [...followRoot.querySelectorAll('[data-log-id]')].find(row => row.textContent.includes('wrapped-tail-end'));
tallRow.scrollIntoView({block:'start',behavior:'instant'}); window.scrollBy(0,200); true`,
	)
	b.wait(
		t,
		`!followState.followingLogs && tallRow.getBoundingClientRect().height > innerHeight && tallRow.getBoundingClientRect().top < 0 && tallRow.getBoundingClientRect().bottom > innerHeight`,
	)
	b.evaluate(
		t,
		`document.querySelector('[data-follow-logs]').click(); true`,
	)
	b.wait(
		t,
		`followState.followingLogs && tallRow.getBoundingClientRect().bottom > 0 && tallRow.getBoundingClientRect().bottom <= innerHeight + 2`,
	)
	b.waitFor(
		t,
		`followState.source === null && followRoot.textContent.includes('final-follow-marker')`,
		time.Minute,
	)
	b.wait(
		t,
		`lastLog().getBoundingClientRect().bottom <= innerHeight + 2`,
	)
	if kind == "deployment" {
		b.wait(
			t,
			`!document.querySelector('[data-step-index="0"]').open`,
		)
	}
	var logs []db.DeploymentLog
	api(t, "GET", logPath, nil, 200, &logs)
	found := false
	for _, log := range logs {
		found = found || log.Line == "final-follow-marker"
	}
	if !found {
		t.Fatal("public log API lost final output")
	}
	b.captureStepLogs(t, kind+"-following-complete")
	b.evaluate(
		t,
		`document.querySelector('[data-older-logs]').click(); true`,
	)
	b.wait(
		t,
		`location.search.includes('before=') && document.body.textContent.includes('Live updates are paused') && !document.querySelector('[data-follow-logs]')`,
	)
}
