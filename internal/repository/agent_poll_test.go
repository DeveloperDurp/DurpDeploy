package repository_test

import (
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
