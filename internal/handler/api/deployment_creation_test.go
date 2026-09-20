package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
)

func TestRetryDeploymentCreatesLocalSnapshot(t *testing.T) {
	harness := newAPIHarness(t)
	user := seedAPIUser(t, harness.repo, "retry@example.com", "admin")
	project := seedProject(t, harness.repo)
	environment := seedEnv(t, harness.repo)
	release := seedRelease(t, harness.repo, project.ID)
	sourceResult, err := harness.repo.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{
			ID: sourceResult.Deployment.ID, Status: "failed",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repo.Queries.CreateDeploymentLog(
		context.Background(),
		db.CreateDeploymentLogParams{
			DeploymentID: sourceResult.Deployment.ID,
			Line:         "source-only log",
		},
	); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/retry", sourceResult.Deployment.ID),
		nil,
	)
	request = withAPIUser(request, user)
	request = withAPIURLParam(
		request,
		"id",
		fmt.Sprint(sourceResult.Deployment.ID),
	)
	recorder := httptest.NewRecorder()

	api.NewDeploymentHandler(harness.repo, harness.runner).
		RetryDeployment(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"retry status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var retried db.Deployment
	mustDecode(t, recorder.Body, &retried)
	if retried.ID == sourceResult.Deployment.ID ||
		retried.ReleaseID != release.ID ||
		retried.EnvironmentID != environment.ID ||
		retried.AssignedAgentID.Valid {
		t.Fatalf(
			"retry deployment=%+v source=%+v",
			retried,
			sourceResult.Deployment,
		)
	}
	source, err := harness.repo.Queries.GetDeployment(
		context.Background(),
		sourceResult.Deployment.ID,
	)
	if err != nil || source.Status != "failed" {
		t.Fatalf("source status=%q error=%v", source.Status, err)
	}
	logs, err := harness.repo.Queries.ListDeploymentLogsByDeployment(
		context.Background(),
		source.ID,
	)
	if err != nil || len(logs) != 1 || logs[0].Line != "source-only log" {
		t.Fatalf("source logs=%v error=%v", logs, err)
	}
	if _, err := harness.repo.Queries.GetDeploymentStepSource(
		context.Background(),
		retried.ID,
	); err != nil {
		t.Fatalf("retry source snapshot: %v", err)
	}
}
