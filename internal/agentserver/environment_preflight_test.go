package agentserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestInvalidLegacyEnvironmentFailsWithoutPoisoningPoll(t *testing.T) {
	for _, whole := range []bool{false, true} {
		for _, variable := range []struct{ name, value string }{
			{"BAD\x00NAME", "private-value"}, {"BAD=NAME", "private-value"},
			{"bad name", "private-value"}, {"2NAME", "private-value"},
			{strings.Repeat("A", 256), "private-value"}, {"VALID", "private\x00value"},
			{" ", "private-value"},
		} {
			t.Run(
				fmt.Sprintf("%s/whole=%t", variable.name, whole),
				func(t *testing.T) {
					checkLegacyEnvironmentPoll(
						t,
						whole,
						variable.name,
						variable.value,
					)
				},
			)
		}
	}
}

func checkLegacyEnvironmentPoll(
	t *testing.T,
	whole bool,
	name, value string,
) {
	t.Helper()
	f, id := seedLegacyEnvironmentPoll(t, whole, name, value)
	var request agentproto.PollRequest
	if err := json.Unmarshal([]byte(`{"protocol":"agent/3","agent_version":"test-host","supported_interpreters":["bash"],"execution_modes":["host"],"container_runtimes":[],"container_interpreters":[]}`), &request); err != nil {
		t.Fatal(err)
	}
	recordFixturePoll(t, f, request)
	ctx, cancel := context.WithTimeout(
		t.Context(),
		2*time.Second,
	)
	defer cancel()
	_, claimed, err := dispatch.New(f.repo).
		Poll(ctx, "test-agent", dispatch.PollRequest{PollRequest: request})
	if claimed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claim=%t error=%v", claimed, err)
	}
	assertLegacyEnvironmentClaim(t, f, id, whole)
	var diagnostic, leaked int
	if err := f.repo.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deployment_logs WHERE deployment_id=? AND line LIKE '%invalid execution environment%'`, id).Scan(&diagnostic); err != nil ||
		diagnostic != 1 {
		t.Fatalf(
			"diagnostic count=%d error=%v",
			diagnostic,
			err,
		)
	}
	if err := f.repo.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deployment_logs WHERE deployment_id=? AND line LIKE '%private%'`, id).Scan(&leaked); err != nil ||
		leaked != 0 {
		t.Fatalf("leaked=%d error=%v", leaked, err)
	}
	next := seedPollPayload(t, f, "pending", "test-agent")
	response, claimed, err := dispatch.New(f.repo).
		Poll(t.Context(), "test-agent", dispatch.PollRequest{PollRequest: request})
	if err != nil || !claimed ||
		int64(response.DeploymentID) != next {
		t.Fatalf(
			"healthy claim=%t id=%d error=%v",
			claimed,
			response.DeploymentID,
			err,
		)
	}
}

func seedLegacyEnvironmentPoll(
	t *testing.T,
	whole bool,
	name, value string,
) (agentFixture, int64) {
	t.Helper()
	f := newAgentFixture(t)
	id := seedPollPayload(t, f, "pending", "test-agent")
	deployment, err := f.repo.Queries.GetDeployment(
		t.Context(),
		id,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateReleaseVariable(t.Context(), db.CreateReleaseVariableParams{
		ReleaseID: deployment.ReleaseID, Name: name, Value: validString(value), Secret: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if !whole {
		if _, err := f.repo.DB.ExecContext(t.Context(), `INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('test-agent',?);`, deployment.EnvironmentID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.DB.ExecContext(t.Context(), `UPDATE deployments SET status='running',assigned_agent_id=NULL WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		if n, err := f.repo.QueueRemoteStepRuns(t.Context(), id, 0); err != nil ||
			n != 1 {
			t.Fatalf("queue=%d error=%v", n, err)
		}
	}
	return f, id
}

func assertLegacyEnvironmentClaim(
	t *testing.T,
	f agentFixture,
	id int64,
	whole bool,
) {
	t.Helper()
	assertDeploymentStatus(t, f, id, "failed")
	if whole {
		claim, err := f.repo.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			id,
		)
		if err != nil || claim.State != "failed" ||
			len(claim.ClaimTokenHash) != 0 ||
			claim.Ciphertext.Valid {
			t.Fatalf("invalid claim=%+v error=%v", claim, err)
		}
	} else {
		runs, err := f.repo.Queries.ListRemoteStepRuns(t.Context(), db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 0})
		if err != nil || len(runs) != 1 || runs[0].State != "failed" || len(runs[0].ClaimTokenHash) != 0 || runs[0].Ciphertext.Valid {
			t.Fatalf("invalid runs=%+v error=%v", runs, err)
		}
	}
}
