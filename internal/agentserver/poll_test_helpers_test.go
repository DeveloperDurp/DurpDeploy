package agentserver_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

const pollBody = `{"protocol":"agent/1","agent_version":"test-v1"}`

func seedPollPayload(
	t *testing.T,
	fixture agentFixture,
	status string,
	agentID string,
) int64 {
	t.Helper()
	ctx := t.Context()
	var sequence int64
	if err := fixture.repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM deployments",
	).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(sequence, 10)
	project, err := fixture.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "poll-project-" + suffix},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := fixture.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "poll-environment-" + suffix},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := fixture.repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "poll-v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []db.CreateReleaseVariableParams{
		{ReleaseID: release.ID, Name: "MODE", Value: validString("global")},
		{ReleaseID: release.ID, Name: "MODE", Value: validString("override"), EnvironmentID: validInt(environment.ID)},
		{ReleaseID: release.ID, Name: "TOKEN", Value: validString("top-secret"), Secret: 1},
	} {
		if _, err := fixture.repo.Queries.CreateReleaseVariable(
			ctx,
			variable,
		); err != nil {
			t.Fatal(err)
		}
	}
	deployment, err := fixture.repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: status,
			AssignedAgentID: validString(agentID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.SnapshotDeploymentSteps(
		ctx,
		deployment.ID,
		[]repository.DeploymentStepSnapshot{
			{
				CreateDeploymentStepParams: db.CreateDeploymentStepParams{
					Name:            "first",
					ScriptBody:      "echo first",
					TimeoutSeconds:  30,
					MaxRetries:      1,
					ExecutionTarget: "agent",
				},
			},
			{
				CreateDeploymentStepParams: db.CreateDeploymentStepParams{
					Name:            "second",
					ScriptBody:      "echo second",
					TimeoutSeconds:  60,
					ExecutionTarget: "agent",
				},
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if changed, err := fixture.repo.Queries.CreateRemoteDeploymentClaim(
		ctx,
		deployment.ID,
	); err != nil || changed != 1 {
		t.Fatalf("create remote claim rows=%d error=%v", changed, err)
	}
	return deployment.ID
}

func decodePollResponse(
	t *testing.T,
	response *http.Response,
) agentproto.PollResponse {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var poll agentproto.PollResponse
	if err := json.Unmarshal(body, &poll); err != nil {
		t.Fatal(err)
	}
	return poll
}

func assertWaitingWithoutPayload(
	t *testing.T,
	fixture agentFixture,
	deploymentID int64,
) {
	t.Helper()
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.State != "waiting" || claim.ClaimTokenHash != nil ||
		claim.Ciphertext.Valid {
		t.Fatalf("claim changed after rejected poll: %+v", claim)
	}
}

func validString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func validInt(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}
