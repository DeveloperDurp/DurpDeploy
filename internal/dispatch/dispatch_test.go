package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

const payloadMarker = "remote-secret-marker"

func TestDispatch_createsLocalWaitingDispatch_whenEnvironmentHasNoPolicy(
	t *testing.T,
) {
	// Given
	dispatcher, repo, deploymentID, _ := dispatchFixture(t, "pending")

	// When
	err := dispatcher.Dispatch(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("dispatch local deployment: %v", err)
	}
	dispatch, err := repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get local dispatch: %v", err)
	}
	if dispatch.Mode != "local" || dispatch.State != "waiting" ||
		dispatch.PoolID.Valid {
		t.Fatalf(
			"local dispatch = %+v, want local waiting without pool",
			dispatch,
		)
	}
	_, err = repo.Queries.GetDeploymentPayload(
		context.Background(),
		deploymentID,
	)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("local payload error = %v, want sql.ErrNoRows", err)
	}
}

func TestDispatch_createsRemotePayload_whenPolicyHasMatchingAgent(
	t *testing.T,
) {
	// Given
	dispatcher, repo, deploymentID, _ := dispatchFixture(t, "pending")
	configureRemotePolicy(t, repo, deploymentID)
	seedMatchingAgent(t, repo)

	// When
	err := dispatcher.Dispatch(context.Background(), deploymentID)

	// Then
	assertRemoteWaitingPayload(t, dispatcher, repo, deploymentID)
	if err != nil {
		t.Fatalf("dispatch remote deployment: %v", err)
	}
}

func TestDispatch_keepsRemoteWaiting_whenPolicyHasNoMatchingAgent(
	t *testing.T,
) {
	// Given
	dispatcher, repo, deploymentID, _ := dispatchFixture(t, "pending")
	configureRemotePolicy(t, repo, deploymentID)

	// When
	err := dispatcher.Dispatch(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("dispatch unmatched remote deployment: %v", err)
	}
	assertRemoteWaitingPayload(t, dispatcher, repo, deploymentID)
}

func TestDispatch_preservesFirstRemotePayload_whenCalledTwice(t *testing.T) {
	// Given
	dispatcher, repo, deploymentID, _ := dispatchFixture(t, "pending")
	configureRemotePolicy(t, repo, deploymentID)
	if err := dispatcher.Dispatch(
		context.Background(),
		deploymentID,
	); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	first, err := repo.Queries.GetDeploymentPayload(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get first payload: %v", err)
	}

	// When
	err = dispatcher.Dispatch(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("repeat dispatch: %v", err)
	}
	second, err := repo.Queries.GetDeploymentPayload(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get repeated payload: %v", err)
	}
	if second.Ciphertext != first.Ciphertext {
		t.Fatal("repeat dispatch replaced immutable payload")
	}
}

func TestDispatch_doesNothing_whenApprovalIsPending(t *testing.T) {
	// Given
	dispatcher, repo, deploymentID, _ := dispatchFixture(t, "pending_approval")
	configureRemotePolicy(t, repo, deploymentID)

	// When
	err := dispatcher.Dispatch(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("dispatch pending approval: %v", err)
	}
	_, err = repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf(
			"pending approval dispatch error = %v, want sql.ErrNoRows",
			err,
		)
	}
}

func TestDispatch_payloadSurvivesReleaseRefresh(t *testing.T) {
	// Given
	dispatcher, repo, deploymentID, releaseID := dispatchFixture(t, "pending")
	configureRemotePolicy(t, repo, deploymentID)
	if err := dispatcher.Dispatch(
		context.Background(),
		deploymentID,
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	release, err := repo.Queries.GetRelease(context.Background(), releaseID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	_, err = repo.Queries.UpdateRelease(
		context.Background(),
		db.UpdateReleaseParams{
			ID: release.ID, ProjectID: release.ProjectID, Version: release.Version,
			StepsJson: `[{"name":"refreshed"}]`,
		},
	)
	if err != nil {
		t.Fatalf("refresh release: %v", err)
	}

	// When
	payload := decryptedPayload(t, dispatcher, repo, deploymentID)

	// Then
	if strings.Contains(payload, "refreshed") ||
		!strings.Contains(payload, "original") {
		t.Fatalf("payload = %q, want original release snapshot", payload)
	}
}

func TestDispatch_storesPayloadCiphertextWithoutPlaintextMarker(t *testing.T) {
	// Given
	dispatcher, repo, deploymentID, _ := dispatchFixture(t, "pending")
	configureRemotePolicy(t, repo, deploymentID)

	// When
	err := dispatcher.Dispatch(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	payload, err := repo.Queries.GetDeploymentPayload(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get ciphertext: %v", err)
	}
	if strings.Contains(payload.Ciphertext, payloadMarker) {
		t.Fatal("stored payload contains plaintext marker")
	}
}

func dispatchFixture(
	t *testing.T,
	status string,
) (*Dispatcher, *repository.Repository, int64, int64) {
	t.Helper()
	ctx := context.Background()
	repo := testRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	repo.SetSecretBox(box)
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: `[{"name":"original"}]`,
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	value, err := box.Encrypt(payloadMarker)
	if err != nil {
		t.Fatalf("encrypt release variable: %v", err)
	}
	_, err = repo.Queries.CreateReleaseVariable(
		ctx,
		db.CreateReleaseVariableParams{
			ReleaseID: release.ID, Name: "TOKEN",
			Value: sql.NullString{String: value, Valid: true}, Secret: 1,
		},
	)
	if err != nil {
		t.Fatalf("create release variable: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: status,
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return New(repo, box, nil), repo, deployment.ID, release.ID
}

func configureRemotePolicy(
	t *testing.T,
	repo *repository.Repository,
	deploymentID int64,
) {
	t.Helper()
	ctx := context.Background()
	if _, err := repo.DB.ExecContext(
		ctx,
		"INSERT INTO agent_pools (name) VALUES ('pool')",
	); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	deployment, err := repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	_, err = repo.DB.ExecContext(ctx, `
INSERT INTO environment_agent_policies (environment_id, pool_id, selector)
VALUES (?, 1, 'region=us')`, deployment.EnvironmentID)
	if err != nil {
		t.Fatalf("create environment policy: %v", err)
	}
}

func seedMatchingAgent(t *testing.T, repo *repository.Repository) {
	t.Helper()
	ctx := context.Background()
	_, err := repo.DB.ExecContext(ctx, `
INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
VALUES ('agent-1', 'agent', 'active', 'certificate', ?),
       ('agent-2', 'other', 'active', 'certificate', ?)`,
		strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatalf("create agents: %v", err)
	}
	_, err = repo.DB.ExecContext(ctx, `
INSERT INTO agent_pool_memberships (pool_id, agent_id) VALUES (1, 'agent-1');
INSERT INTO agent_tags (agent_id, tag_key, tag_value) VALUES ('agent-1', 'region', 'us')`)
	if err != nil {
		t.Fatalf("seed matching agent: %v", err)
	}
}

func assertRemoteWaitingPayload(
	t *testing.T,
	dispatcher *Dispatcher,
	repo *repository.Repository,
	deploymentID int64,
) {
	t.Helper()
	dispatch, err := repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get remote dispatch: %v", err)
	}
	if dispatch.Mode != "remote" || dispatch.State != "waiting" ||
		!dispatch.PoolID.Valid {
		t.Fatalf(
			"remote dispatch = %+v, want remote waiting with pool",
			dispatch,
		)
	}
	payload := decryptedPayload(t, dispatcher, repo, deploymentID)
	if !strings.Contains(payload, "original") ||
		!strings.Contains(payload, payloadMarker) {
		t.Fatalf("payload = %q, want release and selected variables", payload)
	}
}

func decryptedPayload(
	t *testing.T,
	dispatcher *Dispatcher,
	repo *repository.Repository,
	deploymentID int64,
) string {
	t.Helper()
	payload, err := repo.Queries.GetDeploymentPayload(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get payload: %v", err)
	}
	plaintext, err := dispatcher.box.Decrypt(payload.Ciphertext)
	if err != nil {
		t.Fatalf("decrypt payload: %v", err)
	}
	return plaintext
}

func testRepo(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/dispatch.db?_pragma=foreign_keys(1)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}
