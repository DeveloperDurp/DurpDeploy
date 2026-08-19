package dispatch

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/runner"
)

func TestCancellation_CancelsLocalRunningDeployment(t *testing.T) {
	// Given
	_, repo, deploymentID, _ := dispatchFixture(t, "running")
	if _, err := repo.Queries.CreateDeploymentDispatch(
		context.Background(),
		db.CreateDeploymentDispatchParams{
			DeploymentID: deploymentID, Mode: "local", Selector: "", State: "waiting",
		},
	); err != nil {
		t.Fatalf("create local dispatch: %v", err)
	}
	rnr := runner.New(repo, runner.NewLogBroker())
	cancelled := make(chan struct{}, 1)
	rnr.RegisterCancel(deploymentID, func() { cancelled <- struct{}{} })
	service := NewCancellationService(repo, rnr)

	// When
	state, err := service.Cancel(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("cancel local deployment: %v", err)
	}
	if state != "cancelled" {
		t.Fatalf("state = %q, want cancelled", state)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("local cancellation context was not cancelled")
	}
}

func TestCancellation_CancelsPendingApprovalImmediately(t *testing.T) {
	// Given
	_, repo, deploymentID, _ := dispatchFixture(t, "pending_approval")
	service := NewCancellationService(repo, nil)

	// When
	state, err := service.Cancel(context.Background(), deploymentID)

	// Then
	if err != nil {
		t.Fatalf("cancel pending approval: %v", err)
	}
	if state != "cancelled" {
		t.Fatalf("state = %q, want cancelled", state)
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
