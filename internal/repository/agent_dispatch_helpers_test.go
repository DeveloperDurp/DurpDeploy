package repository_test

import (
	"bytes"
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func remoteDispatchFixture(
	t *testing.T,
) (*repository.Repository, int64, int64) {
	t.Helper()
	repo := newTestRepo(t)
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "remote-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "remote-env"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := repo.DB.Exec(
		"INSERT INTO agent_pools (name) VALUES ('pool')",
	); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return repo, deployment.ID, release.ID
}

func seedEligibleAgent(
	t *testing.T,
	repo *repository.Repository,
	agentID string,
) {
	t.Helper()
	_, err := repo.DB.Exec(`
		INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
		VALUES (?, 'agent', 'active', 'certificate', ?)`, agentID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := repo.DB.Exec(
		"INSERT INTO agent_pool_memberships (pool_id, agent_id) VALUES (1, ?)",
		agentID,
	); err != nil {
		t.Fatalf("add membership: %v", err)
	}
	if _, err := repo.DB.Exec(
		"INSERT INTO agent_tags (agent_id, tag_key, tag_value) VALUES (?, 'region', 'us')",
		agentID,
	); err != nil {
		t.Fatalf("add tag: %v", err)
	}
}

func claimDispatch(
	repo *repository.Repository,
	deploymentID int64,
	agentID string,
) (bool, error) {
	result, err := repo.DB.Exec(
		`
		UPDATE deployment_dispatches SET state = 'claimed', agent_id = ?, claim_token_hash = ?
		WHERE deployment_id = ? AND state = 'waiting'
		  AND EXISTS (SELECT 1 FROM agents a JOIN agent_pool_memberships m ON m.agent_id = a.id WHERE a.id = ? AND a.status = 'active' AND m.pool_id = deployment_dispatches.pool_id)
		  AND (selector = '' OR (SELECT COUNT(*) FROM agent_tags t WHERE t.agent_id = ? AND instr(',' || deployment_dispatches.selector || ',', ',' || t.tag_key || '=' || t.tag_value || ',') > 0) = length(deployment_dispatches.selector) - length(replace(deployment_dispatches.selector, ',', '')) + 1)
		  AND NOT EXISTS (SELECT 1 FROM deployment_dispatches active_dispatch WHERE active_dispatch.agent_id = ? AND active_dispatch.state IN ('claimed', 'started', 'cancel_requested'))`,
		agentID,
		bytes.Repeat([]byte{1}, 32),
		deploymentID,
		agentID,
		agentID,
		agentID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func testSecretBox(t *testing.T) *secret.Box {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	return box
}
