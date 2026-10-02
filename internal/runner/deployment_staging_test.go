package runner

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func stagingDeployment(
	t *testing.T,
	repo *repository.Repository,
) db.Deployment {
	t.Helper()
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "stage"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := json.Marshal([]deploymentStep{
		{Name: "producer", ContainerImage: "example/worker:1", MaxRetries: 1},
		{Name: "consumer", ContainerImage: "example/worker:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: string(steps),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return created.Deployment
}

func TestDeploymentStageSurvivesStepRetries(t *testing.T) {
	// Given: the first attempt fails and two steps run in fresh containers.
	r, repo, trace := podmanFixture(t, `
case "$3" in
run)
  test "$DURPDEPLOY_STAGE_DIR" = /stage || exit 9
  for arg do case "$arg" in --volume=*:*/stage:*) printf '%s\n' "$arg" >> "$PODMAN_TRACE";; esac; done
  count=$(wc -l < "$PODMAN_TRACE")
  if [ "$count" = 1 ]; then exit 7; fi;;
esac
`)
	dep := stagingDeployment(t, repo)
	// When: the complete deployment runs.
	r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	// Then: all attempts use one volume and terminal cleanup releases it.
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "succeeded" {
		t.Fatalf("deployment=%+v: %v", stored, err)
	}
	contents, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(contents))
	if len(lines) != 3 || lines[0] != lines[1] || lines[1] != lines[2] {
		t.Fatalf("attempt mounts=%q", contents)
	}
	if len(r.staging) != 0 {
		t.Fatal("completed staging remains registered")
	}
}

func TestDeploymentStageCleanupFailureBlocksCompletion(t *testing.T) {
	// Given: a staged deployment whose runtime refuses volume removal.
	r, repo, _ := podmanFixture(t, `
case "$3" in
volume) if [ "$4" = rm ]; then exit 7; fi;;
esac
`)
	dep := stagingDeployment(t, repo)
	// When: execution otherwise succeeds.
	r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	// Then: cleanup uncertainty replaces success and keeps resources tracked.
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil ||
		stored.Status != "cleanup_unconfirmed" || len(r.staging[dep.ID]) != 1 {
		t.Fatalf(
			"deployment=%+v: %v",
			stored,
			err,
		)
	}
}

func TestDeploymentStageRejectsPersistedOverrideBeforeRuntime(t *testing.T) {
	// Given: a legacy snapshot carries the reserved staging variable.
	r, _, trace := podmanFixture(t, `printf started > "$PODMAN_TRACE"`)
	if err := os.Remove(trace); err != nil {
		t.Fatal(err)
	}
	// When: staging is requested with the conflicting value.
	_, err := r.stageDeployment(t.Context(), 1, map[string]string{
		containerenv.StageVariable: "/override",
	})
	// Then: the runtime is never contacted.
	if err == nil {
		t.Fatal("accepted staging override")
	}
	if _, statErr := os.Stat(trace); !os.IsNotExist(statErr) {
		t.Fatalf("runtime started: %v", statErr)
	}
}

func TestDeploymentStagePartialCreationIsCleanedUp(t *testing.T) {
	// Given: volume creation fails after its cleanup identity is registered.
	r, repo, trace := podmanFixture(t, `
case "$3" in
volume)
  if [ "$4" = create ]; then exit 7; fi
  if [ "$4" = rm ]; then printf '%s\n' "$5" > "$PODMAN_TRACE"; fi;;
esac
`)
	dep := stagingDeployment(t, repo)
	// When: the deployment tries to stage its first local step.
	r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	// Then: it fails before execution and removes any partially created volume.
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "failed" || len(r.staging) != 0 {
		t.Fatalf("deployment=%+v staging=%v: %v", stored, r.staging, err)
	}
	removed, err := os.ReadFile(trace)
	if err != nil ||
		!strings.HasPrefix(string(removed), "durpdeploy-artifact-") {
		t.Fatalf("partial staging cleanup=%s: %v", removed, err)
	}
}

func TestShutdownRemovesStagingBetweenLocalSteps(t *testing.T) {
	// Given: local staging remains while the deployment is outside a local step.
	r, repo, _ := podmanFixture(t, "")
	dep := stagingDeployment(t, repo)
	if err := repo.Queries.UpdateDeploymentStatus(t.Context(), db.UpdateDeploymentStatusParams{
		ID: dep.ID, Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.stageDeployment(t.Context(), dep.ID, nil); err != nil {
		t.Fatal(err)
	}
	// When: shutdown begins without a local worker holding the staging volume.
	r.KillAll()
	// Then: staging is drained even though no step completion triggered cleanup.
	if len(r.staging) != 0 {
		t.Fatal("shutdown left staging resources registered")
	}
}

func TestDeploymentStageTerminalRetryPreservesOutcome(t *testing.T) {
	for _, status := range []string{"succeeded", "failed"} {
		t.Run(status, func(t *testing.T) {
			body := ""
			if status == "failed" {
				body = `case "$3" in run) exit 7;; esac`
			}
			r, repo, _ := podmanFixture(t, body)
			dep := stagingDeployment(t, repo)
			// Given: only terminal status writes temporarily fail.
			_, err := repo.DB.ExecContext(t.Context(), `
CREATE TRIGGER reject_terminal BEFORE UPDATE OF status ON deployments
WHEN NEW.status IN ('succeeded', 'failed')
BEGIN SELECT RAISE(FAIL, 'terminal write unavailable'); END`)
			if err != nil {
				t.Fatal(err)
			}
			// When: Run returns before its background persistence retry.
			r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
			if _, err := repo.DB.ExecContext(t.Context(),
				"DROP TRIGGER reject_terminal"); err != nil {
				t.Fatal(err)
			}
			// Then: cleanup and returning from Run do not change the outcome.
			deadline := time.Now().Add(3 * time.Second)
			for {
				stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.FinishedAt.Valid {
					if stored.Status != status || len(r.staging) != 0 {
						t.Fatalf("deployment=%+v staging=%v", stored, r.staging)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("terminal retry did not persist: %+v", stored)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
