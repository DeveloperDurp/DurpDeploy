package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/secret"
)

func TestRetryApprovalExactSet_PreservesOrderAndRollsBackUnavailable(t *testing.T) {
	// Given
	h := newAPIHarness(t)
	ctx := context.Background()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release, err := h.repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	lifecycle, err := h.repo.Queries.CreateLifecycle(
		ctx, db.CreateLifecycleParams{Name: "approval"},
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	if err := h.repo.Queries.SetProjectLifecycle(
		ctx, db.SetProjectLifecycleParams{
			ID:          project.ID,
			LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
		},
	); err != nil {
		t.Fatalf("set lifecycle: %v", err)
	}
	if _, err := h.repo.Queries.CreateLifecycleStage(
		ctx, db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: environment.ID,
			RequiresApproval: 1,
		},
	); err != nil {
		t.Fatalf("create approval stage: %v", err)
	}
	label, err := h.repo.Queries.CreateAgentLabel(
		ctx, db.CreateAgentLabelParams{Name: "Builders", NormalizedName: "builders"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		createRetryEligibleAgent(t, h, label.ID, id)
	}
	source := seedDeployment(t, h.repo, release.ID, environment.ID, "failed")
	if _, err := h.repo.Queries.CreateDeploymentRoutingSnapshot(
		ctx, db.CreateDeploymentRoutingSnapshotParams{
			DeploymentID: source.ID, Source: "request", TargetMode: "label",
			AgentLabelID:   sql.NullInt64{Int64: label.ID, Valid: true},
			AgentLabelName: sql.NullString{String: label.Name, Valid: true},
			AgentStrategy:  sql.NullString{String: "all", Valid: true},
		},
	); err != nil {
		t.Fatalf("create source snapshot: %v", err)
	}
	for position, id := range []string{"agent-b", "agent-a"} {
		if _, err := h.repo.Queries.AddDeploymentRoutingAgent(
			ctx, db.AddDeploymentRoutingAgentParams{
				DeploymentID: source.ID, Position: int64(position),
				AgentID: id, AgentName: id,
			},
		); err != nil {
			t.Fatalf("add source agent: %v", err)
		}
	}
	service := dispatch.NewCreationService(
		h.repo, dispatch.New(h.repo, box, nil),
	)
	retry, err := service.Retry(ctx, source)
	if err != nil {
		t.Fatalf("create retry: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(
		ctx, "UPDATE agents SET status = 'disabled' WHERE id = 'agent-b'",
	); err != nil {
		t.Fatalf("disable exact agent: %v", err)
	}

	// When
	err = service.Approve(ctx, retry.ID, dispatch.Approval{ApprovedBy: "qa"})

	// Then
	if !errors.Is(err, dispatch.ErrNoEligibleAgents) {
		t.Fatalf("approve error = %v, want no eligible agents", err)
	}
	stored, err := h.repo.Queries.GetDeployment(ctx, retry.ID)
	if err != nil || stored.Status != "pending_approval" {
		t.Fatalf("retry = %#v, error = %v", stored, err)
	}
	if _, err := h.repo.DB.ExecContext(
		ctx, "UPDATE agents SET status = 'active' WHERE id = 'agent-b'",
	); err != nil {
		t.Fatalf("reactivate exact agent: %v", err)
	}
	if err := service.Approve(
		ctx, retry.ID, dispatch.Approval{ApprovedBy: "qa"},
	); err != nil {
		t.Fatalf("approve reactivated retry: %v", err)
	}
	agents, err := h.repo.Queries.ListDeploymentRoutingAgents(ctx, retry.ID)
	if err != nil || len(agents) != 2 || agents[0].AgentID != "agent-b" ||
		agents[1].AgentID != "agent-a" {
		t.Fatalf("exact agents = %#v, error = %v", agents, err)
	}
}

func createRetryEligibleAgent(
	t *testing.T,
	h *harness,
	labelID int64,
	id string,
) {
	t.Helper()
	ctx := context.Background()
	_, err := h.repo.DB.ExecContext(ctx, `
INSERT INTO agents (
    id, name, status, certificate_pem, certificate_fingerprint
) VALUES (?, ?, 'active', 'certificate', ?)
`, id, id, fmt.Sprintf("%-64s", id))
	if err != nil {
		t.Fatalf("create agent %s: %v", id, err)
	}
	_, err = h.repo.DB.ExecContext(ctx, `
INSERT INTO agent_pairings (
    agent_id, pairing_code_hash, agent_public_identity, agent_pin,
    server_public_identity, server_pin, state, expires_at, paired_at
) VALUES (?, randomblob(32), ?, ?, 'server', ?, 'paired', 1, 1)
`, id, id, fmt.Sprintf("%-64s", id+"-pin"),
		fmt.Sprintf("%-64s", id+"-server"))
	if err != nil {
		t.Fatalf("pair agent %s: %v", id, err)
	}
	if _, err := h.repo.Queries.CreateAgentLabelMembership(
		ctx, db.CreateAgentLabelMembershipParams{
			AgentLabelID: labelID, AgentID: id,
		},
	); err != nil {
		t.Fatalf("label agent %s: %v", id, err)
	}
}
