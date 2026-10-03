package repository_test

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestAgentDrainBlocksNewClaimsUntilResume(t *testing.T) {
	// Given: a paired agent with legacy and step work waiting.
	repo := remoteFixture(t)
	for _, statement := range []string{
		`UPDATE deployments SET status='running' WHERE id=3`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id) VALUES (3,0,'a')`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(repository.RemotePayloadSnapshot) (
		repository.RemotePreparedClaim, error,
	) {
		t.Fatal("draining agent reached payload preparation")
		return repository.RemotePreparedClaim{}, nil
	}

	// When: drain is enabled.
	if changed, err := repo.SetAgentDraining(
		t.Context(),
		"a",
		true,
	); err != nil ||
		!changed {
		t.Fatalf("drain=%v error=%v", changed, err)
	}

	// Then: both claim paths leave queued work intact, including a new repository.
	restarted := repository.New(repo.DB)
	if _, claimed, err := restarted.ClaimRemoteStepPayload(
		t.Context(), "a", prepare,
	); err != nil || claimed {
		t.Fatalf("step claim=%v error=%v", claimed, err)
	}
	if _, claimed, err := restarted.ClaimRemoteDeploymentPayload(
		t.Context(), "a", prepare,
	); err != nil || claimed {
		t.Fatalf("legacy claim=%v error=%v", claimed, err)
	}
	rows, err := restarted.ClaimRemoteDeployment(
		t.Context(), currentClaimArg(t, repo),
	)
	if err != nil || rows != 0 {
		t.Fatalf("direct claim=%d error=%v", rows, err)
	}
	if changed, err := restarted.SetAgentDraining(
		t.Context(),
		"a",
		false,
	); err != nil ||
		!changed {
		t.Fatalf("resume=%v error=%v", changed, err)
	}
	rows, err = restarted.ClaimRemoteDeployment(
		t.Context(), currentClaimArg(t, repo),
	)
	assertOne(t, rows, err)
}

func TestAgentDrainPreservesIssuedWork(t *testing.T) {
	// Given: a paired agent already owns a deployment claim.
	repo := remoteFixture(t)
	claim := currentClaimArg(t, repo)
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claim)
	assertOne(t, rows, err)

	// When: the administrator drains it before its start acknowledgement.
	if _, err := repo.SetAgentDraining(t.Context(), "a", true); err != nil {
		t.Fatal(err)
	}

	// Then: start, heartbeat, and completion all remain valid.
	identity := remoteIdentity(claim)
	if err := repo.StartRemoteDeployment(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.HeartbeatRemoteDeployment(
		t.Context(), identity,
	); err != nil {
		t.Fatal(err)
	}
	result, err := repo.FinishRemoteDeploymentLifecycle(
		t.Context(), identity, "succeeded",
	)
	if err != nil || !result.Changed {
		t.Fatalf("finish=%+v error=%v", result, err)
	}
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil || agent.Status != "active" || agent.Draining != 1 {
		t.Fatalf("agent=%+v error=%v", agent, err)
	}
}

func TestAgentDrainPreservesIssuedStep(t *testing.T) {
	// Given: a step claim issued before draining.
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(`UPDATE deployments SET status='running'
		WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	now, err := repo.Queries.CurrentUnixTime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
		(deployment_id,step_index,agent_id,state,claim_token_hash,
		 claim_expires_at) VALUES (3,0,'a','claimed',zeroblob(32),?)`,
		now+60); err != nil {
		t.Fatal(err)
	}
	identity := repository.RemoteLifecycleClaim{
		DeploymentID: 3, AgentID: "a", ClaimTokenHash: make([]byte, 32),
	}

	// When: drain is enabled.
	if _, err := repo.SetAgentDraining(t.Context(), "a", true); err != nil {
		t.Fatal(err)
	}

	// Then: the existing step can finish.
	if _, err := repo.StartRemoteStep(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.HeartbeatRemoteStep(
		t.Context(), identity,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FinishRemoteStep(
		t.Context(), identity, "succeeded",
	); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.Queries.ListRemoteStepRuns(
		t.Context(), db.ListRemoteStepRunsParams{DeploymentID: 3},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "succeeded" {
		t.Fatalf("runs=%+v error=%v", runs, err)
	}
}
