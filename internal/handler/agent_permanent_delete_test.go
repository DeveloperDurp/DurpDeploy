package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentPairing_DeleteActiveAssignedAgent_detachesDispatchesAndDeletesData(
	t *testing.T,
) {
	// Given
	h := newProjectHarness(t)
	ctx := context.Background()
	if _, err := h.repo.DB.ExecContext(ctx, `
	INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
	VALUES ('delete-active', 'Delete Active', 'active', 'certificate', ?)`,
		strings.Repeat("c", 64),
	); err != nil {
		t.Fatalf("create active agent: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(ctx, `
	INSERT INTO agent_pairings (
		agent_id, pairing_code_hash, agent_public_identity, agent_pin,
		server_public_identity, server_pin, state, expires_at, paired_at
	) VALUES (?, ?, ?, ?, ?, ?, 'paired', ?, ?)`,
		"delete-active",
		[]byte(strings.Repeat("a", 32)),
		"certificate",
		strings.Repeat("d", 64),
		"server certificate",
		strings.Repeat("e", 64),
		2_000_000_000,
		1_000_000_000,
	); err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	environment := h.makeEnv("delete active environment")
	if _, err := h.repo.Queries.AssignAgentToEnvironment(
		ctx,
		db.AssignAgentToEnvironmentParams{
			EnvironmentID: environment.ID,
			AgentID:       "delete-active",
			UpdatedAt:     1_000_000_000,
		},
	); err != nil {
		t.Fatalf("assign agent: %v", err)
	}
	project := h.makeProject("delete active project")
	release := h.makeRelease(project.ID, "delete-active", "exit 0")
	deployment, err := h.repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "running",
			StartedAt:     sql.NullInt64{Int64: 1_000_000_000, Valid: true},
			FinishedAt:    sql.NullInt64{},
			Forced:        0,
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := h.repo.Queries.CreateDirectDeploymentDispatch(
		ctx,
		db.CreateDirectDeploymentDispatchParams{
			DeploymentID:    deployment.ID,
			AssignedAgentID: sql.NullString{String: "delete-active", Valid: true},
		},
	); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}
	claimToken := []byte(strings.Repeat("f", 32))
	if _, err := h.repo.DB.ExecContext(ctx, `
	UPDATE deployment_dispatches
	SET state = 'started', agent_id = ?, claim_token_hash = ?, started_at = ?,
	    last_heartbeat_at = ?
	WHERE deployment_id = ?`,
		"delete-active",
		claimToken,
		1_000_000_001,
		1_000_000_002,
		deployment.ID,
	); err != nil {
		t.Fatalf("start dispatch: %v", err)
	}
	if _, err := h.repo.Queries.CreateDeploymentPayload(
		ctx,
		db.CreateDeploymentPayloadParams{
			DeploymentID: deployment.ID,
			Ciphertext:   "encrypted payload",
		},
	); err != nil {
		t.Fatalf("create payload: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(ctx, `
	INSERT INTO agent_events (agent_id, deployment_id, event_type, dispatch_state)
	VALUES (?, ?, 'dispatch_started', 'started')`,
		"delete-active",
		deployment.ID,
	); err != nil {
		t.Fatalf("create agent event: %v", err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		h.server.URL+"/admin/agents/delete-active",
		nil,
	)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", h.csrfToken())

	// When
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("delete active agent: %v", err)
	}
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", resp.StatusCode)
	}
	if _, err := h.repo.Queries.GetAgent(ctx, "delete-active"); err != sql.ErrNoRows {
		t.Fatalf("agent after delete = %v", err)
	}
	if _, err := h.repo.Queries.GetAgentPairing(ctx, "delete-active"); err != sql.ErrNoRows {
		t.Fatalf("pairing after delete = %v", err)
	}
	if _, err := h.repo.Queries.GetEnvironmentAgentAssignment(
		ctx,
		environment.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("assignment after delete = %v", err)
	}
	if _, err := h.repo.Queries.GetDeploymentPayload(ctx, deployment.ID); err != sql.ErrNoRows {
		t.Fatalf("payload after delete = %v", err)
	}
	var deploymentStatus string
	if err := h.repo.DB.QueryRowContext(
		ctx,
		"SELECT status FROM deployments WHERE id = ?",
		deployment.ID,
	).Scan(&deploymentStatus); err != nil {
		t.Fatalf("load deployment after delete: %v", err)
	}
	if deploymentStatus != "failed" {
		t.Fatalf("deployment status = %q, want failed", deploymentStatus)
	}
	var mode, dispatchState string
	var agentID, assignedAgentID sql.NullString
	var storedClaimToken []byte
	if err := h.repo.DB.QueryRowContext(ctx, `
	SELECT mode, state, agent_id, assigned_agent_id, claim_token_hash
	FROM deployment_dispatches
	WHERE deployment_id = ?`, deployment.ID).Scan(
		&mode,
		&dispatchState,
		&agentID,
		&assignedAgentID,
		&storedClaimToken,
	); err != nil {
		t.Fatalf("load dispatch after delete: %v", err)
	}
	if mode != "local" || dispatchState != "lost" || agentID.Valid ||
		assignedAgentID.Valid || storedClaimToken != nil {
		t.Fatalf(
			"dispatch after delete = mode %q state %q agent %#v assigned %#v claim %x",
			mode,
			dispatchState,
			agentID,
			assignedAgentID,
			storedClaimToken,
		)
	}
	var eventCount int
	if err := h.repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM agent_events WHERE agent_id = ?",
		"delete-active",
	).Scan(&eventCount); err != nil {
		t.Fatalf("count agent events after delete: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("agent event count = %d, want 0", eventCount)
	}
}
