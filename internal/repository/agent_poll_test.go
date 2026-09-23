package repository_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentPollCapabilityReplacementIsTransactional(t *testing.T) {
	// Given
	repo := remoteFixture(t)
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}

	// When
	_, err = repo.RecordAgentPoll(
		t.Context(),
		db.HeartbeatAgentParams{
			Now: sql.NullInt64{Int64: 200, Valid: true},
			AgentVersion: sql.NullString{
				String: "invalid-report", Valid: true,
			},
			ID: "a",
			CertificateFingerprint: sql.NullString{
				String: agent.CertificateFingerprint.String, Valid: true,
			},
		},
		[]string{"python3", "/bin/sh"},
	)

	// Then
	if err == nil {
		t.Fatal("invalid capability report succeeded")
	}
	interpreters, queryErr := repo.Queries.ListAgentInterpreters(
		t.Context(),
		"a",
	)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if !slices.Equal(interpreters, []string{"bash"}) {
		t.Fatalf("capabilities changed after rollback: %v", interpreters)
	}
	updated, queryErr := repo.Queries.GetAgent(t.Context(), "a")
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if updated.LastHeartbeatAt.Int64 != agent.LastHeartbeatAt.Int64 ||
		updated.AgentVersion.Valid {
		t.Fatalf("heartbeat changed after rollback: %+v", updated)
	}
}

func TestAgentPollCapabilityRemovalFailsWaitingRuns(t *testing.T) {
	// Given
	repo := remoteFixture(t)
	ctx := context.Background()
	agent, err := repo.Queries.GetAgent(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx, `
INSERT INTO agent_environment_labels(agent_id, environment_id) VALUES('a', 2);
INSERT INTO agent_interpreters(agent_id, interpreter) VALUES('a', 'python3');
INSERT INTO agent_labels(agent_id, label) VALUES('a', 'other');
UPDATE deployments SET status = 'running' WHERE id = 3;
UPDATE deployment_steps SET interpreter = 'python3'
WHERE deployment_id = 3 AND step_index = 0;`); err != nil {
		t.Fatal(err)
	}
	created, err := repo.QueueRemoteStepRuns(ctx, 3, 0)
	if err != nil || created != 1 {
		t.Fatalf("queue runs=%d want=1; error=%v", created, err)
	}

	// When
	changed, err := repo.RecordAgentPoll(
		ctx,
		db.HeartbeatAgentParams{
			Now:                    sql.NullInt64{Int64: 200, Valid: true},
			AgentVersion:           sql.NullString{String: "v2", Valid: true},
			ID:                     "a",
			CertificateFingerprint: agent.CertificateFingerprint,
		},
		[]string{"bash"},
	)

	// Then
	if err != nil || changed != 1 {
		t.Fatalf("poll rows=%d want=1; error=%v", changed, err)
	}
	runs, err := repo.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{DeploymentID: 3, StepIndex: 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].State != "failed" ||
		!runs[0].FinishedAt.Valid {
		t.Fatalf("runs=%+v want one finished failed run", runs)
	}
}
