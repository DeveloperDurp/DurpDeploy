package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestCancellationService_cancelsQueuedRemoteDispatches(t *testing.T) {
	tests := []struct {
		name  string
		claim bool
	}{
		{name: "waiting"},
		{name: "claimed", claim: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			repo := newCancellationRepository(t)
			deploymentID := createQueuedRemoteDeployment(t, repo, test.claim)

			// When
			state, err := NewCancellationService(repo, nil).Cancel(
				context.Background(),
				deploymentID,
			)

			// Then
			if err != nil {
				t.Fatalf("cancel queued remote deployment: %v", err)
			}
			if state != "cancelled" {
				t.Fatalf("cancellation state = %q, want cancelled", state)
			}
			deployment, err := repo.Queries.GetDeployment(
				context.Background(),
				deploymentID,
			)
			if err != nil || deployment.Status != "cancelled" {
				t.Fatalf(
					"deployment = %#v, %v; want cancelled",
					deployment,
					err,
				)
			}
			dispatch, err := repo.Queries.GetDeploymentDispatch(
				context.Background(),
				deploymentID,
			)
			if err != nil || dispatch.State != "cancelled" {
				t.Fatalf("dispatch = %#v, %v; want cancelled", dispatch, err)
			}
			if test.claim &&
				(!dispatch.AgentID.Valid || len(dispatch.ClaimTokenHash) == 0) {
				t.Fatalf(
					"queued cancellation cleared claim ownership: %#v",
					dispatch,
				)
			}
		})
	}
}

func TestCancellationService_rejectsRunningClaimedRemoteDeployment(
	t *testing.T,
) {
	// Given
	repo := newCancellationRepository(t)
	deploymentID := createQueuedRemoteDeployment(t, repo, true)
	if err := repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{ID: deploymentID, Status: "running"},
	); err != nil {
		t.Fatalf("mark deployment running: %v", err)
	}

	// When
	_, err := NewCancellationService(repo, nil).Cancel(
		context.Background(),
		deploymentID,
	)

	// Then
	if !errors.Is(err, ErrCancellationState) {
		t.Fatalf("cancel error = %v, want ErrCancellationState", err)
	}
	dispatch, err := repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil || dispatch.State != "claimed" {
		t.Fatalf("dispatch = %#v, %v; want claimed", dispatch, err)
	}
}

func TestCancellationService_requestsAcknowledgedCancellationForRunningRemote(
	t *testing.T,
) {
	// Given
	repo := newCancellationRepository(t)
	deploymentID := createQueuedRemoteDeployment(t, repo, true)
	dispatch, err := repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get claimed dispatch: %v", err)
	}
	if _, err := repo.Queries.StartClaimedDeploymentDispatch(
		context.Background(),
		db.StartClaimedDeploymentDispatchParams{
			StartedAt:       sql.NullInt64{Int64: 1, Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: 1, Valid: true},
			DeploymentID:    deploymentID,
			AgentID:         dispatch.AgentID,
			ClaimTokenHash:  dispatch.ClaimTokenHash,
		},
	); err != nil {
		t.Fatalf("start claimed dispatch: %v", err)
	}
	if err := repo.Queries.UpdateDeploymentStatus(context.Background(),
		db.UpdateDeploymentStatusParams{ID: deploymentID, Status: "running"},
	); err != nil {
		t.Fatalf("mark deployment running: %v", err)
	}

	// When
	state, err := NewCancellationService(repo, nil).Cancel(
		context.Background(),
		deploymentID,
	)

	// Then
	if err != nil || state != "cancel_requested" {
		t.Fatalf("cancellation = %q, %v; want cancel_requested", state, err)
	}
	dispatch, err = repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil || dispatch.State != "cancel_requested" {
		t.Fatalf("dispatch = %#v, %v; want cancel_requested", dispatch, err)
	}
}

func TestCancellationService_cancelsPendingApproval(t *testing.T) {
	// Given
	repo := newCancellationRepository(t)
	deploymentID := createDeployment(t, repo, "pending_approval")

	// When
	state, err := NewCancellationService(repo, nil).Cancel(
		context.Background(),
		deploymentID,
	)

	// Then
	if err != nil || state != "cancelled" {
		t.Fatalf("cancellation = %q, %v; want cancelled", state, err)
	}
	deployment, err := repo.Queries.GetDeployment(
		context.Background(),
		deploymentID,
	)
	if err != nil || deployment.Status != "cancelled" {
		t.Fatalf("deployment = %#v, %v; want cancelled", deployment, err)
	}
}

func newCancellationRepository(t *testing.T) *repository.Repository {
	t.Helper()
	connection, err := migrate.Run(
		"file:" + filepath.Join(t.TempDir(), "cancellation.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return repository.New(connection)
}

func createQueuedRemoteDeployment(
	t *testing.T,
	repo *repository.Repository,
	claim bool,
) int64 {
	t.Helper()
	ctx := context.Background()
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
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	if _, err := repo.Queries.CreatePendingAgent(
		ctx,
		db.CreatePendingAgentParams{
			ID: "agent", Name: "agent",
		},
	); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := repo.Queries.CreateDirectDeploymentDispatch(ctx,
		db.CreateDirectDeploymentDispatchParams{
			DeploymentID:    deployment.ID,
			AssignedAgentID: sql.NullString{String: "agent", Valid: true},
		},
	); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}
	if claim {
		if _, err := repo.Queries.ClaimDeploymentDispatch(ctx,
			db.ClaimDeploymentDispatchParams{
				AgentID: sql.NullString{String: "agent", Valid: true},
				ClaimTokenHash: []byte{
					1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
					1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
				},
				ClaimExpiresAt:  sql.NullInt64{Int64: 1, Valid: true},
				LastHeartbeatAt: sql.NullInt64{Int64: 1, Valid: true},
				DeploymentID:    deployment.ID,
			},
		); err != nil {
			t.Fatalf("claim dispatch: %v", err)
		}
	}
	return deployment.ID
}

func createDeployment(
	t *testing.T,
	repo *repository.Repository,
	status string,
) int64 {
	t.Helper()
	ctx := context.Background()
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
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
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
	return deployment.ID
}
