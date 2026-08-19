package dispatch

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestCancellation_RequestsRemoteCancellation_whenClaimedOrStarted(
	t *testing.T,
) {
	for _, dispatchState := range []string{"claimed", "started"} {
		t.Run(dispatchState, func(t *testing.T) {
			// Given
			_, repo, deploymentID, _ := dispatchFixture(t, "running")
			configureRemotePolicy(t, repo, deploymentID)
			seedMatchingAgent(t, repo)
			claimHash := bytes.Repeat([]byte{1}, 32)
			createRemoteDispatch(
				t,
				repo,
				deploymentID,
				dispatchState,
				"agent-1",
				claimHash,
			)
			service := NewCancellationService(repo, nil)

			// When
			state, err := service.Cancel(context.Background(), deploymentID)

			// Then
			if err != nil {
				t.Fatalf("request remote cancellation: %v", err)
			}
			if state != "cancel_requested" {
				t.Fatalf("state = %q, want cancel_requested", state)
			}
			dispatch, err := repo.Queries.GetDeploymentDispatch(
				context.Background(),
				deploymentID,
			)
			if err != nil {
				t.Fatalf("get dispatch: %v", err)
			}
			if dispatch.State != "cancel_requested" ||
				!dispatch.CancelRequestedAt.Valid {
				t.Fatalf(
					"dispatch = %+v, want cancellation request timestamp",
					dispatch,
				)
			}
		})
	}
}

func TestCancellation_AcknowledgementRequiresCurrentOwnerAndIsIdempotent(
	t *testing.T,
) {
	// Given
	_, repo, deploymentID, _ := dispatchFixture(t, "running")
	configureRemotePolicy(t, repo, deploymentID)
	seedMatchingAgent(t, repo)
	claimHash := bytes.Repeat([]byte{1}, 32)
	createRemoteDispatch(
		t,
		repo,
		deploymentID,
		"cancel_requested",
		"agent-1",
		claimHash,
	)
	service := NewCancellationService(repo, nil)

	// When
	err := service.Acknowledge(
		context.Background(),
		deploymentID,
		"agent-2",
		claimHash,
	)

	// Then
	if !errors.Is(err, ErrCancellationOwner) {
		t.Fatalf("wrong owner error = %v, want ErrCancellationOwner", err)
	}
	dispatch, err := repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get unchanged dispatch: %v", err)
	}
	if dispatch.State != "cancel_requested" {
		t.Fatalf("wrong owner changed state to %q", dispatch.State)
	}

	// When
	err = service.Acknowledge(
		context.Background(),
		deploymentID,
		"agent-1",
		claimHash,
	)

	// Then
	if err != nil {
		t.Fatalf("acknowledge cancellation: %v", err)
	}
	if err := service.Acknowledge(
		context.Background(),
		deploymentID,
		"agent-1",
		claimHash,
	); err != nil {
		t.Fatalf("duplicate acknowledgement: %v", err)
	}
	deployment, err := repo.Queries.GetDeployment(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Status != "cancelled" {
		t.Fatalf("deployment status = %q, want cancelled", deployment.Status)
	}
}

func TestCancellation_ExpiresUnacknowledgedRequest(t *testing.T) {
	// Given
	_, repo, deploymentID, _ := dispatchFixture(t, "running")
	configureRemotePolicy(t, repo, deploymentID)
	seedMatchingAgent(t, repo)
	claimHash := bytes.Repeat([]byte{1}, 32)
	createRemoteDispatch(
		t,
		repo,
		deploymentID,
		"cancel_requested",
		"agent-1",
		claimHash,
	)
	if _, err := repo.DB.Exec(
		"UPDATE deployment_dispatches SET cancel_requested_at = ? WHERE deployment_id = ?",
		time.Now().Add(-31*time.Second).Unix(),
		deploymentID,
	); err != nil {
		t.Fatalf("age cancellation request: %v", err)
	}
	service := NewCancellationService(repo, nil)

	// When
	updated, err := service.Expire(context.Background(), time.Now())

	// Then
	if err != nil {
		t.Fatalf("expire cancellation request: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expired = %d, want 1", updated)
	}
	dispatch, err := repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if dispatch.State != "cancel_unconfirmed" {
		t.Fatalf("dispatch state = %q, want cancel_unconfirmed", dispatch.State)
	}
}

func TestCancellation_BlocksCompletionAfterCancellationRequest(t *testing.T) {
	// Given
	_, repo, deploymentID, _ := dispatchFixture(t, "running")
	configureRemotePolicy(t, repo, deploymentID)
	seedMatchingAgent(t, repo)
	claimHash := bytes.Repeat([]byte{1}, 32)
	createRemoteDispatch(t, repo, deploymentID, "started", "agent-1", claimHash)
	service := NewCancellationService(repo, nil)
	if _, err := service.Cancel(
		context.Background(),
		deploymentID,
	); err != nil {
		t.Fatalf("request cancellation: %v", err)
	}

	// When
	_, err := repo.Queries.TransitionDeploymentDispatch(
		context.Background(),
		db.TransitionDeploymentDispatchParams{
			DeploymentID:   deploymentID,
			AgentID:        sql.NullString{String: "agent-1", Valid: true},
			ClaimTokenHash: claimHash,
			CurrentState:   "started",
			NextState:      "succeeded",
		},
	)

	// Then
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf(
			"completion after cancellation error = %v, want sql.ErrNoRows",
			err,
		)
	}
	recorded, err := service.RecordLateResult(
		context.Background(),
		deploymentID,
		"agent-1",
		claimHash,
	)
	if err != nil {
		t.Fatalf("record late result: %v", err)
	}
	if !recorded {
		t.Fatal("late result was not recorded")
	}
	var events int
	if err := repo.DB.QueryRow(
		"SELECT COUNT(*) FROM agent_events WHERE deployment_id = ? AND event_type = 'late_result'",
		deploymentID,
	).Scan(&events); err != nil {
		t.Fatalf("count late result events: %v", err)
	}
	if events != 1 {
		t.Fatalf("late result events = %d, want 1", events)
	}
}

func createRemoteDispatch(
	t *testing.T,
	repo *repository.Repository,
	deploymentID int64,
	state, agentID string,
	claimHash []byte,
) {
	t.Helper()
	if _, err := repo.DB.Exec(`
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, selector, state, agent_id, claim_token_hash
		) VALUES (?, 'remote', 1, '', ?, ?, ?)`,
		deploymentID, state, agentID, claimHash,
	); err != nil {
		t.Fatalf("create remote dispatch: %v", err)
	}
}
