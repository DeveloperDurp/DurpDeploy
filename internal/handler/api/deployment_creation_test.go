package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
)

func TestRetryDeploymentCreatesNewSnapshot(t *testing.T) {
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
	seedActiveAssignedAgent(t, harness, environment.ID)

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
		retried.AssignedAgentID.String != "retry-agent" {
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
	claim, err := harness.repo.Queries.GetRemoteDeploymentClaim(
		context.Background(),
		retried.ID,
	)
	if err != nil || claim.AgentID != "retry-agent" {
		t.Fatalf("retry claim=%+v error=%v", claim, err)
	}

	before, err := harness.repo.Queries.ListDeployments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repo.Queries.SetAgentStatus(
		context.Background(),
		db.SetAgentStatusParams{ID: "retry-agent", Status: "disabled"},
	); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	api.NewDeploymentHandler(harness.repo, harness.runner).
		RetryDeployment(recorder, request)
	if recorder.Code != http.StatusConflict ||
		!strings.Contains(recorder.Body.String(), "active and paired") {
		t.Fatalf("inactive retry status=%d body=%s",
			recorder.Code, recorder.Body.String())
	}
	after, err := harness.repo.Queries.ListDeployments(context.Background())
	if err != nil || len(after) != len(before) {
		t.Fatalf("inactive retry created row: before=%d after=%d err=%v",
			len(before), len(after), err)
	}
}

func TestAssignedDeploymentNeverFallsBackLocal(t *testing.T) {
	harness := newAPIHarness(t)
	project := seedProject(t, harness.repo)
	environment := seedEnv(t, harness.repo)
	release := seedRelease(t, harness.repo, project.ID)
	seedActiveAssignedAgent(t, harness, environment.ID)
	body := strings.NewReader(fmt.Sprintf(
		`{"release_id":%d,"environment_id":%d}`,
		release.ID,
		environment.ID,
	))
	request := httptest.NewRequest(http.MethodPost, "/", body)
	request = withAPIURLParam(request, "id", fmt.Sprint(project.ID))
	recorder := httptest.NewRecorder()

	api.NewDeploymentHandler(harness.repo, harness.runner).
		CreateDeployment(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"create status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var deployment db.Deployment
	mustDecode(t, recorder.Body, &deployment)
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		persisted, err := harness.repo.Queries.GetDeployment(
			context.Background(),
			deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Status != "pending" {
			t.Fatalf("assigned deployment invoked local runner: status=%q",
				persisted.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func seedActiveAssignedAgent(
	t *testing.T,
	harness *harness,
	environmentID int64,
) {
	t.Helper()
	ctx := context.Background()
	if _, err := harness.repo.Queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: "retry-agent", Name: "retry-agent",
		Endpoint: "https://agent.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	code := bytes.Repeat([]byte{1}, 32)
	pin := strings.Repeat("a", 64)
	if _, err := harness.repo.Queries.CreateAgentPairing(
		ctx,
		db.CreateAgentPairingParams{
			AgentID: "retry-agent", PairingCodeHash: code,
			AgentPublicIdentity: "public", AgentPin: pin, ExpiresAt: 500,
		},
	); err != nil {
		t.Fatal(err)
	}
	rows, err := harness.repo.Queries.BeginPairingCommit(
		ctx,
		db.BeginPairingCommitParams{
			AgentID: "retry-agent", PairingCodeHash: code, Now: 100,
			ServerPublicIdentity: sql.NullString{String: "public", Valid: true},
			ServerPin:            sql.NullString{String: pin, Valid: true},
			EncryptedIdentity:    sql.NullString{String: "cipher", Valid: true},
		},
	)
	if err != nil || rows != 1 {
		t.Fatalf("begin pairing rows=%d error=%v", rows, err)
	}
	rows, err = harness.repo.CommitAgentPairing(
		ctx,
		db.CompleteAgentPairingParams{
			AgentID: "retry-agent", Now: sql.NullInt64{Int64: 100, Valid: true},
			ServerPin: sql.NullString{String: pin, Valid: true},
		},
		db.ActivatePairedAgentParams{
			CertificatePem: sql.NullString{String: "certificate", Valid: true},
			CertificateFingerprint: sql.NullString{
				String: pin, Valid: true,
			},
		},
	)
	if err != nil || rows != 1 {
		t.Fatalf("commit pairing rows=%d error=%v", rows, err)
	}
	rows, err = harness.repo.Queries.AssignEnvironmentAgent(
		ctx,
		db.AssignEnvironmentAgentParams{
			EnvironmentID: environmentID, AgentID: "retry-agent",
		},
	)
	if err != nil || rows != 1 {
		t.Fatalf("assign agent rows=%d error=%v", rows, err)
	}
}
