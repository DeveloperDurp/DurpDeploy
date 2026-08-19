package repository_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestAgentEnrollment_activatesPendingAgentAndRecordsHeartbeat(
	t *testing.T,
) {
	// Given
	db := newAgentEnrollmentRepo(t)
	queries := loadAgentEnrollmentQueries(t)
	ctx := context.Background()
	createPendingAgent(t, db, queries, "agent-a")

	// When
	var status, fingerprint, enrolledVersion string
	var enrolledAt int64
	err := db.QueryRowContext(
		ctx,
		queries["ActivatePendingAgent"],
		"certificate",
		strings.Repeat("a", 64),
		"1.0.0",
		int64(100),
		int64(100),
		int64(100),
		"agent-a",
	).Scan(new(string), &status, &fingerprint, &enrolledVersion, &enrolledAt)
	if err != nil {
		t.Fatalf("activate pending agent: %v", err)
	}
	_, err = db.ExecContext(ctx, queries["UpdateAgentHeartbeat"],
		"1.1.0", int64(200), int64(200), "agent-a",
	)
	if err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	// Then
	var agentID, agentName, version, certificate, persistedFingerprint string
	var heartbeat, persistedEnrolledAt int64
	if err := db.QueryRowContext(ctx, queries["GetActiveAgentByFingerprint"], fingerprint).
		Scan(
			&agentID, &agentName, &version, &certificate, &persistedFingerprint, &heartbeat, &persistedEnrolledAt,
		); err != nil {
		t.Fatalf("get activated agent: %v", err)
	}
	if agentID != "agent-a" || agentName != "agent-a" || status != "active" ||
		fingerprint != strings.Repeat("a", 64) ||
		enrolledVersion != "1.0.0" ||
		enrolledAt != 100 ||
		version != "1.1.0" ||
		certificate != "certificate" ||
		persistedFingerprint != fingerprint ||
		heartbeat != 200 ||
		persistedEnrolledAt != 100 {
		t.Fatalf("agent lifecycle did not persist activation and heartbeat")
	}
}

func TestAgentEnrollment_rejectsDuplicateFingerprintAndDisabledAgent(
	t *testing.T,
) {
	// Given
	db := newAgentEnrollmentRepo(t)
	queries := loadAgentEnrollmentQueries(t)
	ctx := context.Background()
	createPendingAgent(t, db, queries, "agent-a")
	activateAgent(t, db, queries, "agent-a", strings.Repeat("a", 64))
	createPendingAgent(t, db, queries, "agent-b")

	// When
	_, duplicateErr := db.ExecContext(
		ctx,
		queries["ActivatePendingAgent"],
		"certificate",
		strings.Repeat("a", 64),
		"1.0.0",
		int64(100),
		int64(100),
		int64(100),
		"agent-b",
	)
	_, disableErr := db.ExecContext(
		ctx,
		queries["DisableAgent"],
		int64(300),
		"agent-a",
	)
	if disableErr != nil {
		t.Fatalf("disable agent: %v", disableErr)
	}
	result, heartbeatErr := db.ExecContext(ctx, queries["UpdateAgentHeartbeat"],
		"1.2.0", int64(400), int64(400), "agent-a",
	)
	if heartbeatErr != nil {
		t.Fatalf("update disabled heartbeat: %v", heartbeatErr)
	}

	// Then
	if duplicateErr == nil {
		t.Fatal("duplicate fingerprint activation succeeded")
	}
	updated, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("disabled heartbeat rows affected: %v", err)
	}
	if updated != 0 {
		t.Fatalf("disabled heartbeat updated %d rows", updated)
	}
}

func TestAgentEnrollment_revokePreventsHeartbeat(t *testing.T) {
	// Given
	db := newAgentEnrollmentRepo(t)
	queries := loadAgentEnrollmentQueries(t)
	ctx := context.Background()
	createPendingAgent(t, db, queries, "agent-a")
	activateAgent(t, db, queries, "agent-a", strings.Repeat("a", 64))

	// When
	if _, err := db.ExecContext(
		ctx,
		queries["RevokeAgent"],
		int64(300),
		int64(300),
		"agent-a",
	); err != nil {
		t.Fatalf("revoke agent: %v", err)
	}
	result, err := db.ExecContext(ctx, queries["UpdateAgentHeartbeat"],
		"1.2.0", int64(400), int64(400), "agent-a",
	)
	if err != nil {
		t.Fatalf("update revoked heartbeat: %v", err)
	}

	// Then
	updated, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("revoked heartbeat rows affected: %v", err)
	}
	if updated != 0 {
		t.Fatalf("revoked heartbeat updated %d rows", updated)
	}
}

func TestAgentEnrollment_consumesTokenOnlyOnceForBoundPendingAgent(
	t *testing.T,
) {
	// Given
	db := newAgentEnrollmentRepo(t)
	queries := loadAgentEnrollmentQueries(t)
	ctx := context.Background()
	createPendingAgent(t, db, queries, "agent-a")
	usedHash := strings.Repeat("a", 32)
	if _, err := db.ExecContext(ctx, queries["CreateAgentEnrollmentToken"],
		usedHash, "prefix", "agent-a", int64(200),
	); err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}

	// When
	if _, err := db.ExecContext(ctx, queries["ConsumeAgentEnrollmentToken"],
		int64(100), usedHash, "agent-a", int64(100),
	); err != nil {
		t.Fatalf("consume enrollment token: %v", err)
	}
	wrongAgentHash := strings.Repeat("b", 32)
	if _, err := db.ExecContext(ctx, queries["CreateAgentEnrollmentToken"],
		wrongAgentHash, "prefix", "agent-a", int64(200),
	); err != nil {
		t.Fatalf("create wrong-agent enrollment token: %v", err)
	}
	expiredHash := strings.Repeat("c", 32)
	if _, err := db.ExecContext(ctx, queries["CreateAgentEnrollmentToken"],
		expiredHash, "prefix", "agent-a", int64(100),
	); err != nil {
		t.Fatalf("create expired enrollment token: %v", err)
	}

	// Then
	assertTokenCannotConsume(t, db, queries, usedHash, "agent-a", 101, 101)
	assertTokenCannotConsume(
		t,
		db,
		queries,
		wrongAgentHash,
		"wrong-agent",
		101,
		101,
	)
	assertTokenCannotConsume(t, db, queries, expiredHash, "agent-a", 100, 100)
}

func TestAgentEnrollment_consumesTokenExactlyOnceConcurrently(t *testing.T) {
	// Given
	db := newAgentEnrollmentRepo(t)
	queries := loadAgentEnrollmentQueries(t)
	ctx := context.Background()
	createPendingAgent(t, db, queries, "agent-a")
	hash := strings.Repeat("b", 32)
	if _, err := db.ExecContext(ctx, queries["CreateAgentEnrollmentToken"],
		hash, "prefix", "agent-a", int64(200),
	); err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}

	// When
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := db.ExecContext(
				ctx,
				queries["ConsumeAgentEnrollmentToken"],
				int64(100),
				hash,
				"agent-a",
				int64(100),
			)
			if err != nil {
				results <- false
				return
			}
			updated, err := result.RowsAffected()
			results <- err == nil && updated == 1
		}()
	}
	wg.Wait()
	close(results)

	// Then
	successes := 0
	for consumed := range results {
		if consumed {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent token consumes = %d, want 1", successes)
	}
}

func createPendingAgent(
	t *testing.T,
	db *sql.DB,
	queries map[string]string,
	id string,
) {
	t.Helper()
	if _, err := db.ExecContext(
		context.Background(),
		queries["CreatePendingAgent"],
		id,
		id,
		"1.0.0",
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
}

func activateAgent(
	t *testing.T,
	db *sql.DB,
	queries map[string]string,
	id, fingerprint string,
) {
	t.Helper()
	if _, err := db.ExecContext(
		context.Background(),
		queries["ActivatePendingAgent"],
		"certificate",
		fingerprint,
		"1.0.0",
		int64(100),
		int64(100),
		int64(100),
		id,
	); err != nil {
		t.Fatalf("activate agent: %v", err)
	}
}

func assertTokenCannotConsume(
	t *testing.T,
	db *sql.DB,
	queries map[string]string,
	hash, agentID string,
	usedAt, now int64,
) {
	t.Helper()
	result, err := db.ExecContext(
		context.Background(),
		queries["ConsumeAgentEnrollmentToken"],
		usedAt,
		hash,
		agentID,
		now,
	)
	if err != nil {
		t.Fatalf("consume enrollment token: %v", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("token consume rows affected: %v", err)
	}
	if updated != 0 {
		t.Fatalf("invalid token consume updated %d rows", updated)
	}
}

func newAgentEnrollmentRepo(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := migrate.Run(
		"file:" + filepath.Join(
			t.TempDir(),
			"agents.db",
		) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn).DB
}

func loadAgentEnrollmentQueries(t *testing.T) map[string]string {
	t.Helper()
	content, err := os.ReadFile(
		filepath.Join("..", "..", "queries", "agents.sql"),
	)
	if err != nil {
		t.Fatalf("read agent queries: %v", err)
	}
	queries := make(map[string]string)
	for _, part := range strings.Split(string(content), "-- name: ")[1:] {
		name, statement, found := strings.Cut(part, "\n")
		if !found {
			t.Fatalf("query %q is missing a statement", name)
		}
		queries[strings.TrimSpace(name[:strings.Index(name, " ")])] = strings.TrimSpace(
			statement,
		)
	}
	return queries
}

func TestAgentEnrollment_queryFileHasExpectedQueries(t *testing.T) {
	// Given
	queries := loadAgentEnrollmentQueries(t)

	// When
	required := []string{
		"CreatePendingAgent", "CreateAgentEnrollmentToken",
		"ConsumeAgentEnrollmentToken", "ActivatePendingAgent",
		"UpdateAgentHeartbeat", "DisableAgent", "RevokeAgent",
	}

	// Then
	for _, name := range required {
		if _, found := queries[name]; !found {
			t.Errorf("missing %s", name)
		}
	}
}
