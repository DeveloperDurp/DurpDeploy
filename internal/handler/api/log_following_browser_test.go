//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"github.com/google/uuid"
)

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
		b: b, base: base, api: api,
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
	if logPath != "" {
		var execution struct{ Status string }
		f.api(t, "GET", strings.TrimSuffix(logPath, "/logs"),
			nil, 200, &execution)
		var logs []db.DeploymentLog
		f.api(t, "GET", logPath, nil, 200, &logs)
		t.Logf("API execution status=%s log count=%d",
			execution.Status, len(logs))
		// Deployment and runbook APIs return different log orders.
		sort.Slice(logs, func(i, j int) bool {
			return logs[i].ID > logs[j].ID
		})
		for _, log := range logs[:min(5, len(logs))] {
			t.Logf("API log id=%d line=%q", log.ID,
				log.Line[:min(len(log.Line), 160)])
		}
	}
	t.Logf("Follow diagnostics: %s", f.b.evaluate(t, `({
following:window.followState?.followingLogs, hidden:document.hidden,
pending:!!window.followState?.logUpdate, y:scrollY,
rows:document.querySelectorAll('[data-log-id]').length,
text:document.body.textContent.slice(-1000),
last:window.lastLog?.()?.textContent,
bottom:window.lastLog?.()?.getBoundingClientRect().bottom,
button:document.querySelector('[data-follow-logs]')?.outerHTML})`))
}

func (f logFollowingBrowserFixture) check(t *testing.T, kind string) {
	t.Helper()
	b, base, api := f.b, f.base, f.api
	projectPath := fmt.Sprintf("/api/v1/projects/%d", f.projectID)
	var webPath, logPath string
	t.Cleanup(func() { f.logFailure(t, logPath) })
	body := map[string]int64{"environment_id": f.environmentID}
	if kind == "deployment" {
		body["release_id"] = f.releaseID
		var deployment db.Deployment
		api(t, "POST", projectPath+"/deployments", body, 201, &deployment)
		webPath = fmt.Sprintf("/deployments/%d", deployment.ID)
		logPath = "/api/v1" + webPath + "/logs"
	} else {
		var execution db.RunbookExecution
		api(t, "POST", fmt.Sprintf("%s/runbooks/%d/executions",
			projectPath, f.runbookID), body, 201, &execution)
		webPath = fmt.Sprintf("/projects/%d/runbooks/executions/%d",
			f.projectID, execution.ID)
		logPath = fmt.Sprintf("%s/runbook-executions/%d/logs",
			projectPath, execution.ID)
	}
	t.Logf("%s log example: %s%s", kind, base, webPath)
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
	b.wait(
		t,
		`followState.source === null && followRoot.textContent.includes('final-follow-marker')`,
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
