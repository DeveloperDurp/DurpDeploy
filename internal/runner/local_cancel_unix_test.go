//go:build unix

package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestLocalCancellationStopsChildBeforeFiveSeconds(t *testing.T) {
	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	repo, deploymentRunner, _ := setupRunnerHarness(t)
	marker := filepath.Join(t.TempDir(), "started")
	script := fmt.Sprintf("printf ready > %q; sleep 10", marker)
	project, err := repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "cancel-child"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "cancel-child"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1",
		StepsJson: fmt.Sprintf(
			`[{"name":"wait","script_body":%q,"interpreter":"bash"}]`,
			script,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID,
		Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		deploymentRunner.Run(ctx, created.Deployment.ID,
			release.ID, environment.ID)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("local child did not start", err)
	}
	if err := deploymentRunner.Cancel(created.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			cancelRun()
			t.Fatal("cancelled local child did not stop")
		}
		t.Fatal(
			"cancelled local child kept deployment running over five seconds",
		)
	}
	stored, err := repo.Queries.GetDeployment(ctx, created.Deployment.ID)
	if err != nil || stored.Status != "cancelled" {
		t.Fatalf("deployment after cancellation=%+v error=%v", stored, err)
	}
}
