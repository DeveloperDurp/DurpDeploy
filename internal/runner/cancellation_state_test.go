package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestSetupFailureAfterCancellationFinalizesCancelled(t *testing.T) {
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetMaxOpenConns(1)
	repo := repository.New(connection)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "cancel-setup"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "cancel-setup"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	deploymentRunner := New(repo, NewLogBroker())

	deploymentRunner.failUnlessCancelled(
		t.Context(), cancelCtx, deployment.ID,
	)
	stored, err := repo.Queries.GetDeployment(t.Context(), deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "cancelled" || !stored.FinishedAt.Valid {
		t.Fatalf("deployment after cancelled setup failure=%+v", stored)
	}
}

func TestRemoteStepDoesNotQueueWhenAlreadyCancelled(t *testing.T) {
	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	deploymentRunner := &DeploymentRunner{}

	err := deploymentRunner.runRemoteStep(
		t.Context(), cancelCtx, remoteStepRequest{},
	)
	if !errors.Is(err, errDeploymentCancelled) {
		t.Fatalf("runRemoteStep error=%v", err)
	}
}

func TestAcceptedCancelCannotRaceTerminalSuccess(t *testing.T) {
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	repo := repository.New(connection)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "cancel-race"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "cancel-race"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deploymentRunner := New(repo, NewLogBroker())
	for iteration := 0; iteration < 50; iteration++ {
		deployment, err := repo.Queries.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: release.ID, EnvironmentID: environment.ID,
				Status: "running",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		cancelCtx, cancel := context.WithCancel(t.Context())
		deploymentRunner.RegisterCancel(deployment.ID, cancel)
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var cancelErr error
		go func() {
			defer wait.Done()
			<-start
			cancelErr = deploymentRunner.Cancel(deployment.ID)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, _ = deploymentRunner.completeDeployment(
				t.Context(), cancelCtx, deployment.ID, "succeeded", true,
			)
		}()
		close(start)
		wait.Wait()
		stored, err := repo.Queries.GetDeployment(t.Context(), deployment.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cancelErr == nil && stored.Status != "cancelled" {
			t.Fatalf(
				"accepted cancel stored status=%s on iteration %d",
				stored.Status, iteration,
			)
		}
		if cancelErr != nil && stored.Status != "succeeded" {
			t.Fatalf(
				"rejected cancel stored status=%s on iteration %d",
				stored.Status, iteration,
			)
		}
	}
}

func TestTerminalPersistenceFailureRetriesDurably(t *testing.T) {
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetMaxOpenConns(1)
	repo := repository.New(connection)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "terminal-retry"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "terminal-retry"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deploymentRunner := New(repo, NewLogBroker())
	persistCtx, cancelPersist := context.WithCancel(t.Context())
	cancelPersist()
	if _, persisted := deploymentRunner.persistCompletion(
		persistCtx, context.Background(), deployment.ID, "succeeded", true,
	); persisted {
		t.Fatal(
			"terminal write unexpectedly succeeded with a cancelled context",
		)
	}

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		stored, err := repo.Queries.GetDeployment(t.Context(), deployment.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status == "succeeded" && stored.FinishedAt.Valid {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("terminal retry did not persist: %+v", stored)
		case <-ticker.C:
		}
	}
}
