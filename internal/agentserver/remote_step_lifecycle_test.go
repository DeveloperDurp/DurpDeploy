package agentserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteStepResultCompletesRun(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID, claim := claimedRemoteStep(t, fixture)
	path := func(pattern string) string {
		return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(deploymentID))
	}
	response := postAgent(t, fixture, path(agentproto.StartPath), claim)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	result := strings.Replace(
		claim,
		"}",
		`,"state":"succeeded","error":""}`,
		1,
	)
	response = postAgent(t, fixture, path(agentproto.ResultPath), result)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("result status=%d", response.StatusCode)
	}
	runs, err := fixture.repo.Queries.ListRemoteStepRuns(
		t.Context(),
		db.ListRemoteStepRunsParams{DeploymentID: deploymentID, StepIndex: 0},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "succeeded" ||
		!runs[0].FinishedAt.Valid {
		t.Fatalf("remote runs=%+v error=%v", runs, err)
	}
}

func TestRemoteStepStartRejectsExpiredClaim(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID, claim := claimedRemoteStep(t, fixture)
	if _, err := fixture.repo.DB.ExecContext(
		t.Context(),
		`UPDATE remote_step_runs SET claim_expires_at = unixepoch()
		 WHERE deployment_id = ? AND step_index = 0`,
		deploymentID,
	); err != nil {
		t.Fatal(err)
	}
	path := strings.ReplaceAll(
		agentproto.StartPath,
		"{id}",
		fmt.Sprint(deploymentID),
	)
	response := postAgent(t, fixture, path, claim)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expired start status=%d", response.StatusCode)
	}
}

func claimedRemoteStep(
	t *testing.T,
	fixture agentFixture,
) (int64, string) {
	t.Helper()
	deploymentID := seedPollPayload(t, fixture, "pending", "test-agent")
	deployment, err := fixture.repo.Queries.GetDeployment(t.Context(), deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.ExecContext(t.Context(), `
		DELETE FROM remote_deployment_claims WHERE deployment_id = ?;
		UPDATE deployments SET status = 'running', assigned_agent_id = NULL
		WHERE id = ?;
		INSERT INTO agent_labels(agent_id,label) VALUES('test-agent','test');
		INSERT INTO agent_environment_labels(agent_id,environment_id)
		VALUES('test-agent',?);
		INSERT INTO deployment_step_selectors(deployment_id,step_index,label)
		VALUES(?,0,'test')`, deploymentID, deploymentID,
		deployment.EnvironmentID, deploymentID,
	); err != nil {
		t.Fatal(err)
	}
	created, err := fixture.repo.QueueRemoteStepRuns(t.Context(), deploymentID, 0)
	if err != nil || created != 1 {
		t.Fatalf("queue remote step rows=%d error=%v", created, err)
	}

	poll := decodePollResponse(t, postAgent(t, fixture, agentproto.PollPath, pollBody))
	claim := fmt.Sprintf(
		`{"protocol":"agent/1","claim_token":%q}`,
		poll.ClaimToken,
	)
	return deploymentID, claim
}
