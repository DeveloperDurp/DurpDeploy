package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/secret"
)

func TestDeploymentExecutionTarget_APIFreezesLabelIntent(t *testing.T) {
	// Given
	harness := newAPIHarness(t)
	project := seedProject(t, harness.repo)
	environment := seedEnv(t, harness.repo)
	release := seedRelease(t, harness.repo, project.ID)
	label, err := harness.repo.Queries.CreateAgentLabel(
		context.Background(),
		db.CreateAgentLabelParams{Name: "API", NormalizedName: "api"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	createAPIEligibleAgent(t, harness, label.ID, "agent-a")
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost, "/",
		strings.NewReader(fmt.Sprintf(
			`{"release_id":%d,"environment_id":%d,"target_mode":"label","agent_label_id":%d,"agent_strategy":"round_robin"}`,
			release.ID,
			environment.ID,
			label.ID,
		)),
	)
	request = withAPIURLParam(request, "id", fmt.Sprint(project.ID))
	recorder := httptest.NewRecorder()

	// When
	api.NewDeploymentHandler(
		harness.repo, nil, dispatch.New(harness.repo, box, nil),
	).CreateDeployment(recorder, request)

	// Then
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var deployment db.Deployment
	mustDecode(t, recorder.Body, &deployment)
	snapshot, err := harness.repo.Queries.GetDeploymentRoutingSnapshot(
		context.Background(), deployment.ID,
	)
	if err != nil {
		t.Fatalf("get routing snapshot: %v", err)
	}
	if snapshot.Source != "request" || snapshot.TargetMode != "label" ||
		snapshot.AgentLabelID.Int64 != label.ID ||
		snapshot.AgentStrategy.String != "round_robin" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestDeploymentRoutingAtomicity_APIInvalidTargetCreatesNoRoot(
	t *testing.T,
) {
	// Given
	harness := newAPIHarness(t)
	project := seedProject(t, harness.repo)
	environment := seedEnv(t, harness.repo)
	release := seedRelease(t, harness.repo, project.ID)
	request := httptest.NewRequest(
		http.MethodPost, "/",
		strings.NewReader(fmt.Sprintf(
			`{"release_id":%d,"environment_id":%d,"target_mode":"label","agent_strategy":"all"}`,
			release.ID,
			environment.ID,
		)),
	)
	request = withAPIURLParam(request, "id", fmt.Sprint(project.ID))
	recorder := httptest.NewRecorder()

	// When
	api.NewDeploymentHandler(harness.repo, nil).
		CreateDeployment(recorder, request)

	// Then
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	deployments, err := harness.repo.Queries.ListDeployments(
		context.Background(),
	)
	if err != nil || len(deployments) != 0 {
		t.Fatalf(
			"deployments = %d, error = %v; want none",
			len(deployments),
			err,
		)
	}
}

func createAPIEligibleAgent(
	t *testing.T,
	harness *harness,
	labelID int64,
	agentID string,
) {
	t.Helper()
	ctx := context.Background()
	if _, err := harness.repo.DB.ExecContext(ctx, `
INSERT INTO agents (
    id, name, status, certificate_pem, certificate_fingerprint
) VALUES (?, ?, 'active', 'certificate', ?)
`, agentID, agentID, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := harness.repo.DB.ExecContext(ctx, `
INSERT INTO agent_pairings (
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, state, expires_at, paired_at
) VALUES (?, randomblob(32), ?, ?, 'server', ?, 'paired', 1, 1)
`, agentID, agentID, strings.Repeat("b", 64), strings.Repeat("c", 64)); err != nil {
		t.Fatalf("pair agent: %v", err)
	}
	if _, err := harness.repo.Queries.CreateAgentLabelMembership(
		ctx,
		db.CreateAgentLabelMembershipParams{
			AgentLabelID: labelID, AgentID: agentID,
		},
	); err != nil {
		t.Fatalf("create membership: %v", err)
	}
}
