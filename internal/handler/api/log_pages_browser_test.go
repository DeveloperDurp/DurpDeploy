//go:build e2e && packagebrowser

package api_test

import (
	"database/sql"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestDeploymentLogHistoryBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release, err := f.h.repo.Queries.CreateRelease(t.Context(),
		db.CreateReleaseParams{ProjectID: f.project.ID, Version: "history",
			StepsJson: `[{"name":"Quiet"},{"name":"Noisy"}]`})
	if err != nil {
		t.Fatal(err)
	}
	d := seedDeployment(t, f.h.repo, release.ID, f.environment.ID, "running")
	_, err = f.h.repo.Queries.CreateStepDeploymentLog(
		t.Context(),
		db.CreateStepDeploymentLogParams{
			DeploymentID: d.ID,
			Line:         "quiet-start",
			StepIndex:    sql.NullInt64{Int64: 0, Valid: true},
			StepName:     sql.NullString{String: "Quiet", Valid: true},
			StepState:    sql.NullString{String: "running", Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertBrowserLogHistory(t, f, d.ID,
		fmt.Sprintf("/deployments/%d", d.ID), "deploymentStepLogs")
}

func TestRunbookLogHistoryBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	book, _, err := f.h.repo.SaveRunbook(t.Context(), repository.RunbookSave{
		ProjectID: f.project.ID, Name: "log-pages", StepsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, result, err := f.h.repo.CreateRunbookExecution(t.Context(),
		repository.RunbookExecutionRequest{ProjectID: f.project.ID,
			RunbookID: book.ID, EnvironmentID: f.environment.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertBrowserLogHistory(t, f, result.Deployment.ID,
		fmt.Sprintf("/projects/%d/runbooks/executions/%d",
			f.project.ID, execution.ID), "deploymentStream")
}

func assertBrowserLogHistory(
	t *testing.T, f *artifactE2E, deploymentID int64, path, component string,
) {
	t.Helper()
	// Given: initial history exceeds the browser window.
	seedLargeLogCursor(t, f, deploymentID)
	seedLogPageRows(t, f, deploymentID)
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+path)
	b.wait(t, `document.querySelectorAll('[data-log-id]').length === 500`)
	// When: enough live output arrives to evict both initial and live rows.
	seedLogPageRows(t, f, deploymentID)
	last, err := f.h.repo.Queries.CreateDeploymentLog(t.Context(),
		db.CreateDeploymentLogParams{DeploymentID: deploymentID,
			Line: "latest-marker <img src=x onerror=alert(1)>"})
	if err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(deploymentID, "wake")
	b.wait(
		t,
		fmt.Sprintf(`document.querySelector('[data-log-id="%d"]')`, last.ID),
	)
	// Then: only 500 distinct rows remain, and output stays plain text.
	b.wait(
		t,
		`(() => { const rows = [...document.querySelectorAll('[data-log-id]')]; return rows.length === 500 && new Set(rows.map(row => row.dataset.logId)).size === 500 && !document.querySelector('img[src=x]'); })()`,
	)
	oldestID := last.ID - 499
	b.wait(t, fmt.Sprintf(
		`[...document.querySelectorAll('[data-log-id]')].every(row => BigInt(row.dataset.logId) >= BigInt('%d') && BigInt(row.dataset.logId) <= BigInt('%d')) && new URL(document.querySelector('[data-older-logs]').href).searchParams.get('before') === '%d'`,
		oldestID,
		last.ID,
		oldestID,
	))
	if component == "deploymentStepLogs" {
		b.wait(
			t,
			`document.querySelector('[data-step-index="0"] .badge').textContent === 'Running'`,
		)
	}
	b.captureStepLogs(t, component+"-bounded-live")
	// Older navigation pauses the stream, and latest navigation resumes it.
	b.evaluate(t, `document.querySelector('[data-older-logs]').click(); true`)
	b.wait(
		t,
		`document.readyState === 'complete' && location.search.includes('before=') && document.body.textContent.includes('Live updates are paused')`,
	)
	b.wait(t, fmt.Sprintf(
		`document.querySelector('[data-log-id="%d"]') && !document.querySelector('[data-log-id="%d"]')`,
		oldestID-1,
		oldestID,
	))
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('[x-data^="%s"]') === null || Alpine.$data(document.querySelector('[x-data^="%s"]')).source === null`,
				component,
				component,
			),
		),
	) != "true" {
		t.Fatal("historical page opened a live stream")
	}
	b.captureStepLogs(t, component+"-older")
	b.evaluate(
		t,
		`([...document.querySelectorAll('a')].find(a => a.textContent.trim() === 'Latest logs')).click(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`document.readyState === 'complete' && location.search === '' && document.querySelector('[data-log-id="%d"]')`,
			last.ID,
		),
	)
	if err := f.h.repo.Queries.UpdateDeploymentStatus(
		t.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     deploymentID,
			Status: "succeeded",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(deploymentID, "complete")
	b.wait(
		t,
		fmt.Sprintf(
			`Alpine.$data(document.querySelector('[x-data^="%s"]')).source === null`,
			component,
		),
	)
	f.assertBrowserLogBytes(t, b, logHistoryTarget{
		DeploymentID: deploymentID, WebPath: path, Component: component,
	})
}
