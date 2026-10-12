//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunbookLogRevocationBrowserE2E(t *testing.T) {
	// Given: a viewer watches an active runbook for their project.
	f := newArtifactE2E(t)
	book, _, err := f.h.repo.SaveRunbook(t.Context(), repository.RunbookSave{
		ProjectID: f.project.ID, Name: "revocation", StepsJSON: "[]",
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
	viewer := *f
	user := seedAPIUser(t, f.h.repo, "runbook-viewer@example.test", "viewer")
	_, viewer.token = seedAPIToken(t, f.h.repo, user.ID)
	viewer.session, viewer.csrf, err = auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID:        viewer.session,
			UserID:    user.ID,
			CsrfToken: viewer.csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", f.base()+"/members", map[string]any{
		"user_id": user.ID, "role": "deployer",
	}, 201)
	path := fmt.Sprintf(
		"/projects/%d/runbooks/executions/%d",
		f.project.ID,
		execution.ID,
	)
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, viewer.session)
	b.navigateBackTest(t, f.baseURL+path)
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^=deploymentStream]')).source.readyState === 1`,
	)
	// When: an administrator removes the membership through the public API.
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/members/%d", f.base(), user.ID),
		nil,
		204,
	)
	// Then: EventSource closes, and newly emitted output is inaccessible.
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^=deploymentStream]')).source.readyState === 2`,
	)
	row, err := f.h.repo.Queries.CreateDeploymentLog(
		t.Context(),
		db.CreateDeploymentLogParams{
			DeploymentID: result.Deployment.ID,
			Line:         "after-revocation",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.h.runner.Broker().Broadcast(result.Deployment.ID, row.Line)
	if string(
		b.evaluate(
			t,
			`!document.body.textContent.includes('after-revocation') && !document.querySelector('#runbook-execution-actions button')`,
		),
	) != "true" {
		t.Fatal("revoked viewer received output or write controls")
	}
	viewer.api(t, "GET", fmt.Sprintf("%s/runbook-executions/%d/logs/page",
		f.base(), execution.ID), nil, 403)
	viewer.web(t, "GET", path, nil, 403)
	b.screenshot(t, "runbook-membership-revoked")
}
