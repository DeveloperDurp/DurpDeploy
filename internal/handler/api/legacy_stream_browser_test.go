//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestLegacyRunbookStreamDrainsAfterTerminalBadgeBrowserE2E(t *testing.T) {
	// Given: an idle runbook fixture has a live browser stream.
	f := newArtifactE2E(t)
	book, _, err := f.h.repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: f.project.ID,
			Name:      "cleanup-stream",
			StepsJSON: `[{"name":"check","script_body":"true","container_image":"harbor.durp.info/dockerhub/library/bash:5.2"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, deployment, err := f.h.repo.CreateRunbookExecution(
		t.Context(),
		repository.RunbookExecutionRequest{
			ProjectID:     f.project.ID,
			RunbookID:     book.ID,
			EnvironmentID: f.environment.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/projects/%d/runbooks/executions/%d",
			f.baseURL,
			f.project.ID,
			execution.ID,
		),
	)
	// Seed one frame so EventSource has completed its HTTP handshake.
	_, err = f.h.repo.Queries.CreateDeploymentLog(
		t.Context(),
		db.CreateDeploymentLogParams{
			DeploymentID: deployment.Deployment.ID,
			Line:         "before-cleanup",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(deployment.Deployment.ID, "before-cleanup")
	b.wait(
		t,
		`document.querySelector('[x-ref=logs]').textContent.includes('before-cleanup')`,
	)
	// When: the status UI becomes terminal before the server sends final cleanup output.
	b.evaluate(
		t,
		`const badge=document.querySelector('#status-badge'); badge.textContent='succeeded'; badge.dispatchEvent(new CustomEvent('htmx:afterSwap',{bubbles:true,detail:{target:badge}})); true`,
	)
	_, err = f.h.repo.Queries.CreateDeploymentLog(
		t.Context(),
		db.CreateDeploymentLogParams{
			DeploymentID: deployment.Deployment.ID,
			Line:         "final-cleanup",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(deployment.Deployment.ID, "final-cleanup")
	// Then: cleanup output remains visible, and server completion closes the source.
	b.wait(
		t,
		`document.querySelector('[x-ref=logs]').textContent.includes('final-cleanup')`,
	)
	if err := f.h.repo.Queries.UpdateDeploymentStatus(
		t.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     deployment.Deployment.ID,
			Status: "succeeded",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(deployment.Deployment.ID, "terminal")
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^=deploymentStream]')).source===null`,
	)
	b.screenshot(t, "legacy-final-cleanup")
}
