package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/secret"
)

type retryPayload struct {
	DeploymentID int64 `json:"deployment_id"`
	Release      struct {
		Steps json.RawMessage `json:"steps"`
	} `json:"release"`
	Variables []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"variables"`
}

func TestDeploymentRetry_CreatesFreshDeployment(t *testing.T) {
	// Given
	h, box, source := retryFixture(t, "failed")
	handler := api.NewDeploymentHandler(h.repo, nil, dispatch.New(h.repo, box, nil))
	rec := httptest.NewRecorder()
	req := withAPIURLParam(httptest.NewRequest(http.MethodPost, "/", nil), "id", fmt.Sprint(source.ID))

	// When
	handler.RetryDeployment(rec, req)

	// Then
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created db.Deployment
	mustDecode(t, rec.Body, &created)
	if created.ID == source.ID || created.ReleaseID != source.ReleaseID || created.EnvironmentID != source.EnvironmentID {
		t.Fatalf("created = %#v, want distinct row for source %#v", created, source)
	}
	preserved, err := h.repo.Queries.GetDeployment(context.Background(), source.ID)
	if err != nil || preserved != source {
		t.Fatalf("source after retry = %#v, %v; want unchanged %#v", preserved, err, source)
	}
	payload := decryptRetryPayload(t, h, box, created.ID)
	if string(payload.Release.Steps) != `[{"name":"deploy","script_body":"echo immutable"}]` ||
		len(payload.Variables) != 1 || payload.Variables[0].Name != "TOKEN" || payload.Variables[0].Value != "snapshot" {
		t.Fatalf("payload = %#v, want release snapshot steps and variables", payload)
	}
}

func TestDeploymentRetry_RejectsUnknownId(t *testing.T) {
	// Given
	h := newAPIHarness(t)
	rec := httptest.NewRecorder()
	req := withAPIURLParam(httptest.NewRequest(http.MethodPost, "/", nil), "id", "999999")

	// When
	api.NewDeploymentHandler(h.repo, nil).RetryDeployment(rec, req)

	// Then
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeploymentRetry_RejectsMalformedID(t *testing.T) {
	// Given
	h := newAPIHarness(t)
	rec := httptest.NewRecorder()
	req := withAPIURLParam(
		httptest.NewRequest(http.MethodPost, "/", nil),
		"id",
		"not-a-deployment",
	)

	// When
	api.NewDeploymentHandler(h.repo, nil).RetryDeployment(rec, req)

	// Then
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDeploymentRetry_MatchesRedeployPayload(t *testing.T) {
	// Given
	h, box, retrySource := retryFixture(t, "failed")
	redeploySource := seedDeployment(t, h.repo, retrySource.ReleaseID, retrySource.EnvironmentID, "failed")
	handler := api.NewDeploymentHandler(h.repo, nil, dispatch.New(h.repo, box, nil))

	// When
	retry := invokeDeploymentAction(t, handler.RetryDeployment, retrySource.ID)
	redeploy := invokeDeploymentAction(t, handler.RedeployDeployment, redeploySource.ID)

	// Then
	retryPayload := decryptRetryPayload(t, h, box, retry.ID)
	redeployPayload := decryptRetryPayload(t, h, box, redeploy.ID)
	if string(retryPayload.Release.Steps) != string(redeployPayload.Release.Steps) ||
		fmt.Sprint(retryPayload.Variables) != fmt.Sprint(redeployPayload.Variables) {
		t.Fatalf("retry payload %#v differs from redeploy payload %#v", retryPayload, redeployPayload)
	}
}

func TestDeploymentRetry_AllowsRepeatedRetries(t *testing.T) {
	// Given
	h, box, source := retryFixture(t, "cancelled")
	handler := api.NewDeploymentHandler(h.repo, nil, dispatch.New(h.repo, box, nil))

	// When
	first := invokeDeploymentAction(t, handler.RetryDeployment, source.ID)
	second := invokeDeploymentAction(t, handler.RetryDeployment, source.ID)

	// Then
	if first.ID == second.ID || first.ID == source.ID || second.ID == source.ID {
		t.Fatalf("retries = %d, %d; want two fresh rows", first.ID, second.ID)
	}
}

func TestDeploymentRetry_LeavesFreshPendingRowWhenDispatchFails(t *testing.T) {
	// Given
	h, _, source := retryFixture(t, "failed")
	rec := httptest.NewRecorder()
	req := withAPIURLParam(
		httptest.NewRequest(http.MethodPost, "/", nil),
		"id",
		fmt.Sprint(source.ID),
	)

	// When
	api.NewDeploymentHandler(h.repo, nil).RetryDeployment(rec, req)

	// Then
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	deployments, err := h.repo.Queries.ListDeploymentsByRelease(
		context.Background(),
		source.ReleaseID,
	)
	if err != nil || len(deployments) != 2 {
		t.Fatalf("deployments = %#v, %v; want source and pending retry", deployments, err)
	}
	for _, deployment := range deployments {
		if deployment.ID != source.ID && deployment.Status == "pending" {
			return
		}
	}
	t.Fatalf("deployments = %#v, want fresh pending row", deployments)
}

func retryFixture(t *testing.T, status string) (*harness, *secret.Box, db.Deployment) {
	t.Helper()
	h := newAPIHarness(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release, err := h.repo.Queries.CreateRelease(context.Background(), db.CreateReleaseParams{ProjectID: project.ID, Version: "snapshot", StepsJson: `[{"name":"deploy","script_body":"echo immutable"}]`})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	ciphertext, err := box.Encrypt("snapshot")
	if err != nil {
		t.Fatalf("encrypt variable: %v", err)
	}
	if _, err = h.repo.Queries.CreateReleaseVariable(context.Background(), db.CreateReleaseVariableParams{ReleaseID: release.ID, Name: "TOKEN", Value: sql.NullString{String: ciphertext, Valid: true}}); err != nil {
		t.Fatalf("create release variable: %v", err)
	}
	if _, err = h.repo.Queries.CreatePendingAgent(context.Background(), db.CreatePendingAgentParams{ID: "retry-agent", Name: "retry-agent"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err = h.repo.Queries.AssignAgentToEnvironment(context.Background(), db.AssignAgentToEnvironmentParams{EnvironmentID: environment.ID, AgentID: "retry-agent"}); err != nil {
		t.Fatalf("assign agent: %v", err)
	}
	source, err := h.repo.Queries.CreateDeployment(context.Background(), db.CreateDeploymentParams{ReleaseID: release.ID, EnvironmentID: environment.ID, Status: status, StartedAt: sql.NullInt64{Int64: 1, Valid: true}, FinishedAt: sql.NullInt64{Int64: 2, Valid: true}})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	return h, box, source
}

func invokeDeploymentAction(t *testing.T, action http.HandlerFunc, id int64) db.Deployment {
	t.Helper()
	rec := httptest.NewRecorder()
	action(rec, withAPIURLParam(httptest.NewRequest(http.MethodPost, "/", nil), "id", fmt.Sprint(id)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var deployment db.Deployment
	mustDecode(t, rec.Body, &deployment)
	return deployment
}

func decryptRetryPayload(t *testing.T, h *harness, box *secret.Box, deploymentID int64) retryPayload {
	t.Helper()
	stored, err := h.repo.Queries.GetDeploymentPayload(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("get payload: %v", err)
	}
	plaintext, err := box.Decrypt(stored.Ciphertext)
	if err != nil {
		t.Fatalf("decrypt payload: %v", err)
	}
	var payload retryPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return payload
}
