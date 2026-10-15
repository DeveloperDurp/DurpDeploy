//go:build e2e && packagebrowser

package api_test

import (
	"database/sql"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentStepPanelsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release, err := f.h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: f.project.ID,
			Version:   "manual-panels",
			StepsJson: `[{"name":"Open manually"},{"name":"Close manually"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	for _, scenario := range []struct {
		status, state, label string
	}{
		{"pending", "pending", "Pending"},
		{"running", "running", "Running"},
		{"running", "waiting", "Waiting for agents"},
		{"failed", "failed", "Failed"},
		{"succeeded", "succeeded", "Succeeded"},
		{"cancelled", "cancelled", "Cancelled"},
		{"succeeded", "unknown", "State unavailable"},
	} {
		t.Run("initial-"+scenario.state, func(t *testing.T) {
			d := seedDeployment(t, f.h.repo, release.ID,
				f.environment.ID, scenario.status)
			output := "No output yet."
			if scenario.state != "pending" {
				output = "initial output"
				_, err := f.h.repo.Queries.CreateStepDeploymentLog(t.Context(),
					db.CreateStepDeploymentLogParams{
						DeploymentID: d.ID, Line: output,
						StepIndex: sql.NullInt64{Valid: true},
						StepState: sql.NullString{
							String: scenario.state, Valid: scenario.state != "unknown",
						},
					})
				if err != nil {
					t.Fatal(err)
				}
			}
			b.openStepLogPage(t, fmt.Sprintf(
				"%s/deployments/%d", f.baseURL, d.ID))
			if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const panels = [...document.querySelectorAll('details[data-step-index]')]
   .filter(panel => panel.dataset.stepIndex !== '-1');
 return panels.length === 2 && panels.every(panel => !panel.open) &&
 panels[0].querySelector('summary').textContent.includes('Open manually') &&
 panels[0].querySelector('.badge').textContent === %q &&
 panels[0].textContent.includes(%q) &&
 document.querySelector('[role="status"]').getBoundingClientRect().height > 0;
})()`, scenario.label, output))) != "true" {
				t.Fatal(
					"initial panels expanded or hid their summary information",
				)
			}
		})
	}

	// Real server streams must preserve both manual choices through each update.
	d := seedDeployment(t, f.h.repo, release.ID, f.environment.ID, "running")
	b.openStepLogPage(t, fmt.Sprintf("%s/deployments/%d", f.baseURL, d.ID))
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source.readyState === 1`,
	)
	emit := func(state string) {
		t.Helper()
		var last db.DeploymentLog
		for index := range 2 {
			last, err = f.h.repo.Queries.CreateStepDeploymentLog(t.Context(),
				db.CreateStepDeploymentLogParams{
					DeploymentID: d.ID, Line: "live output " + state,
					StepIndex: sql.NullInt64{Int64: int64(index), Valid: true},
					StepState: sql.NullString{
						String: state,
						Valid:  state != "",
					},
				})
			if err != nil {
				t.Fatal(err)
			}
		}
		f.h.runner.Broker().Broadcast(d.ID, "wake")
		b.wait(t, fmt.Sprintf(
			`document.querySelector('[data-log-id="%d"]')`, last.ID))
	}
	emit("waiting")
	if string(b.evaluate(
		t,
		`document.querySelectorAll('details[data-step-index="0"][open], details[data-step-index="1"][open]').length === 0`,
	)) != "true" {
		t.Fatal("waiting events expanded untouched panels")
	}
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 375, "height": 900, "deviceScaleFactor": 1, "mobile": true,
	}, &struct{}{})
	for _, index := range []int{0, 1, 1} {
		b.evaluate(t, fmt.Sprintf(
			`document.querySelector('[data-step-index="%d"] summary').focus(); true`,
			index,
		))
		for _, kind := range []string{"keyDown", "keyUp"} {
			text := ""
			if kind == "keyDown" {
				text = "\r"
			}
			b.call(t, "Input.dispatchKeyEvent", map[string]any{
				"type": kind, "key": "Enter", "code": "Enter",
				"windowsVirtualKeyCode": 13, "text": text,
			}, &struct{}{})
		}
	}
	choices := `document.querySelector('[data-step-index="0"]').open && !document.querySelector('[data-step-index="1"]').open`
	b.wait(t, choices)
	for _, state := range []string{"running", "", "failed"} {
		emit(state)
		if string(b.evaluate(t, choices)) != "true" {
			t.Fatalf("%q event changed a manual panel choice", state)
		}
	}
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{ID: d.ID, Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(d.ID, "complete")
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source === null && document.querySelector('[role="status"]').textContent === 'Deployment failed' && document.querySelector('#status-badge').dataset.deploymentStatus === 'failed'`,
	)
	if string(b.evaluate(t, choices)) != "true" {
		t.Fatal("completion changed a manual panel choice")
	}
	b.captureStepLogs(t, "manual-choices-failed")
	b.evaluate(t,
		`document.querySelector('[data-step-index="1"] summary').click(); true`)
	if string(b.evaluate(t, `(() => {
 const panel = document.querySelector('[data-step-index="1"]');
 return panel.open && panel.innerText.includes('live output waiting') &&
 panel.innerText.includes('live output running') &&
 panel.innerText.includes('live output failed') &&
 panel.querySelector('.badge').textContent === 'Failed';
})()`)) != "true" {
		t.Fatal(
			"opening a previously collapsed panel lost live output or status",
		)
	}
	b.captureStepLogs(t, "manual-retained-output")
}
