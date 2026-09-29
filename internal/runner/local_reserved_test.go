package runner

import (
	"database/sql"
	"os"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunnerRejectsPersistedReservedVariableBeforePodmanRun(t *testing.T) {
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started' > "$PODMAN_TRACE";;
esac
`)
	project, err := repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: "e"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: `[{"name":"unsafe","execution_target":"local",` +
				`"container_image":"example.com/worker:1",` +
				`"variable_names":["HOME"]}]`,
		})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.CreateReleaseVariable(t.Context(),
		db.CreateReleaseVariableParams{
			ReleaseID: release.ID,
			Name:      "HOME",
			Value:     sql.NullString{String: "/tmp/redirected", Valid: true},
		})
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

	r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)

	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("deployment status=%q: %v", stored.Status, err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("Podman ran with a reserved variable: %v", err)
	}
}
