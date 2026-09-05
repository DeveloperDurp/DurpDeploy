package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"durpdeploy/internal/repository"
)

func TestConcurrentDeploymentApproval_OneWinnerAcrossConnections(t *testing.T) {
	fixture := newRoutingFixture(t, 1)
	requireApprovalForFixture(t, fixture)
	creator := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)
	deployment, err := creator.Create(context.Background(), CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing: Input{
			Source:   SourceRequest,
			Mode:     "label",
			LabelID:  fixture.label.ID,
			Strategy: "round_robin",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	repositories := separateRoutingRepositories(t, fixture.repo, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, repo := range repositories {
		wait.Add(1)
		go func(repo *repository.Repository) {
			defer wait.Done()
			<-start
			results <- NewCreationService(repo, New(repo, fixture.box, nil)).Approve(
				context.Background(), deployment.ID, Approval{ApprovedBy: "admin"},
			)
		}(repo)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrDeploymentNotPending):
			conflicts++
		default:
			t.Fatalf("approval error = %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"successes=%d conflicts=%d, want 1 and 1",
			successes,
			conflicts,
		)
	}
	agents, err := fixture.repo.Queries.ListDeploymentRoutingAgents(
		context.Background(),
		deployment.ID,
	)
	if err != nil || len(agents) != 1 {
		t.Fatalf("agents=%d error=%v, want one", len(agents), err)
	}
	var approvals int
	if err := fixture.repo.DB.QueryRowContext(
		context.Background(), "SELECT COUNT(*) FROM deployment_approvals WHERE deployment_id = ?", deployment.ID,
	).Scan(&approvals); err != nil || approvals != 1 {
		t.Fatalf("approvals=%d error=%v, want one", approvals, err)
	}
}

func separateRoutingRepositories(
	t *testing.T,
	repo *repository.Repository,
	count int,
) []*repository.Repository {
	t.Helper()
	var sequence int
	var name, path string
	if err := repo.DB.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatalf("database path: %v", err)
	}
	repositories := make([]*repository.Repository, count)
	for i := range count {
		connection, err := sql.Open("sqlite", fmt.Sprintf(
			"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate",
			path,
		))
		if err != nil {
			t.Fatalf("open connection %d: %v", i, err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		repositories[i] = repository.New(connection)
	}
	return repositories
}
