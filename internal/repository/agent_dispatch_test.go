package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func TestPayload_SnapshotSurvivesReleaseRefresh(t *testing.T) {
	// Given
	repo, deploymentID, releaseID := remoteDispatchFixture(t)
	box := testSecretBox(t)
	payload := map[string]string{
		"step":   "original",
		"SECRET": "snapshot-marker",
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ciphertext, err := box.Encrypt(string(payloadJSON))
	if err != nil {
		t.Fatalf("encrypt payload: %v", err)
	}
	_, err = repo.DB.Exec(
		"INSERT INTO deployment_payloads (deployment_id, ciphertext) VALUES (?, ?)",
		deploymentID,
		ciphertext,
	)
	if err != nil {
		t.Fatalf("create payload: %v", err)
	}

	// When
	_, err = repo.DB.Exec(
		"UPDATE releases SET steps_json = ? WHERE id = ?",
		`[{"name":"changed"}]`,
		releaseID,
	)
	if err != nil {
		t.Fatalf("refresh release: %v", err)
	}

	// Then
	var stored string
	if err := repo.DB.QueryRow(
		"SELECT ciphertext FROM deployment_payloads WHERE deployment_id = ?",
		deploymentID,
	).Scan(&stored); err != nil {
		t.Fatalf("get payload: %v", err)
	}
	plain, err := box.Decrypt(stored)
	if err != nil {
		t.Fatalf("decrypt payload: %v", err)
	}
	if plain != string(payloadJSON) {
		t.Fatalf("payload = %q, want original snapshot %q", plain, payloadJSON)
	}
}

func TestClaim_OnlyOneConcurrentEligibleAgentWins(t *testing.T) {
	// Given
	repo, deploymentID, _ := remoteDispatchFixture(t)
	seedEligibleAgent(t, repo, "agent-1")
	if _, err := repo.DB.Exec(`
		INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state)
		VALUES (?, 'remote', 1, 'region=us', 'waiting')`, deploymentID); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}

	// When
	const claimers = 20
	start := make(chan struct{})
	results := make(chan bool, claimers)
	errs := make(chan error, claimers)
	var wait sync.WaitGroup
	for range claimers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			won, err := claimDispatch(repo, deploymentID, "agent-1")
			if err != nil {
				errs <- err
				return
			}
			results <- won
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)

	// Then
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d, want 1", winners)
	}
}

func TestClaim_RejectsAgentWithActiveJob(t *testing.T) {
	// Given
	repo, firstDeploymentID, releaseID := remoteDispatchFixture(t)
	seedEligibleAgent(t, repo, "agent-1")
	secondDeployment, err := repo.Queries.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID:     releaseID,
			EnvironmentID: 1,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatalf("create second deployment: %v", err)
	}
	for _, deploymentID := range []int64{firstDeploymentID, secondDeployment.ID} {
		if _, err := repo.DB.Exec(`
			INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state)
			VALUES (?, 'remote', 1, 'region=us', 'waiting')`, deploymentID); err != nil {
			t.Fatalf("create dispatch: %v", err)
		}
	}

	// When
	firstWon, err := claimDispatch(repo, firstDeploymentID, "agent-1")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !firstWon {
		t.Fatal("first claim did not win")
	}
	secondWon, err := claimDispatch(repo, secondDeployment.ID, "agent-1")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}

	// Then
	if secondWon {
		t.Fatal("second active claim succeeded")
	}
}

func TestClaim_RejectsAgentWithoutMatchingTag(t *testing.T) {
	// Given
	repo, deploymentID, _ := remoteDispatchFixture(t)
	seedEligibleAgent(t, repo, "agent-1")
	if _, err := repo.DB.Exec(
		"UPDATE agent_tags SET tag_value = 'eu' WHERE agent_id = 'agent-1'",
	); err != nil {
		t.Fatalf("change agent tag: %v", err)
	}
	if _, err := repo.DB.Exec(`
		INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state)
		VALUES (?, 'remote', 1, 'region=us', 'waiting')`, deploymentID); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}

	// When
	won, err := claimDispatch(repo, deploymentID, "agent-1")
	if err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}

	// Then
	if won {
		t.Fatal("agent without matching tag claimed dispatch")
	}
}

func TestClaim_StaleTokenCannotTransitionOrLog(t *testing.T) {
	// Given
	repo, deploymentID, _ := remoteDispatchFixture(t)
	seedEligibleAgent(t, repo, "agent-1")
	claimHash := bytes.Repeat([]byte{1}, 32)
	if _, err := repo.DB.Exec(`
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, selector, state, agent_id, claim_token_hash
		) VALUES (?, 'remote', 1, 'region=us', 'started', 'agent-1', ?)`, deploymentID, claimHash); err != nil {
		t.Fatalf("create claimed dispatch: %v", err)
	}

	// When
	result, err := repo.DB.Exec(`
		UPDATE deployment_dispatches SET state = 'succeeded'
		WHERE deployment_id = ? AND agent_id = ? AND claim_token_hash = ? AND state = 'started'`,
		deploymentID,
		"agent-1",
		bytes.Repeat([]byte{2}, 32),
	)
	if err != nil {
		t.Fatalf("stale transition: %v", err)
	}
	logResult, err := repo.DB.Exec(`
		INSERT INTO deployment_logs (deployment_id, line, sequence)
		SELECT ?, 'stale', 1
		WHERE EXISTS (
			SELECT 1 FROM deployment_dispatches
			WHERE deployment_id = ? AND agent_id = ? AND claim_token_hash = ? AND state = 'started'
		)`, deploymentID, deploymentID, "agent-1", bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatalf("stale log: %v", err)
	}

	// Then
	if rows, err := result.RowsAffected(); err != nil || rows != 0 {
		t.Fatalf("stale transition rows = %d, err = %v; want 0, nil", rows, err)
	}
	if rows, err := logResult.RowsAffected(); err != nil || rows != 0 {
		t.Fatalf("stale log rows = %d, err = %v; want 0, nil", rows, err)
	}
}

func TestSequencedLog_DuplicateReturnsExistingRow(t *testing.T) {
	// Given
	repo, deploymentID, _ := remoteDispatchFixture(t)

	// When
	for _, line := range []string{"first", "duplicate"} {
		_, err := repo.DB.Exec(`
			INSERT INTO deployment_logs (deployment_id, line, sequence) VALUES (?, ?, 1)
			ON CONFLICT(deployment_id, sequence) WHERE sequence IS NOT NULL DO UPDATE
			SET deployment_id = deployment_logs.deployment_id`, deploymentID, line)
		if err != nil {
			t.Fatalf("append sequenced log: %v", err)
		}
	}

	// Then
	var line string
	if err := repo.DB.QueryRow(
		"SELECT line FROM deployment_logs WHERE deployment_id = ? AND sequence = 1",
		deploymentID,
	).Scan(&line); err != nil {
		t.Fatalf("get sequenced log: %v", err)
	}
	if line != "first" {
		t.Fatalf("sequenced log line = %q, want existing row %q", line, "first")
	}
}
