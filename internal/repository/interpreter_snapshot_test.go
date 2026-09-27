package repository_test

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestCreateDeploymentPreservesReleaseInterpreter(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repo := repository.New(conn)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "interpreters"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1",
			StepsJson: `[{"name":"bash","script_body":"echo ok",` +
				`"interpreter":"bash","execution_target":"local"},` +
				`{"name":"python","script_body":"print('ok')",` +
				`"interpreter":"python3","execution_target":"local"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := repo.Queries.ListDeploymentSteps(
		t.Context(), result.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("deployment steps = %+v", steps)
	}
	wantInterpreters := map[string]string{
		"bash": "bash", "python": "python3",
	}
	for _, step := range steps {
		if want := wantInterpreters[step.Name]; step.Interpreter != want {
			t.Fatalf(
				"step %q interpreter=%q want %q",
				step.Name, step.Interpreter, want,
			)
		}
	}
	if _, err := repo.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{
		ID: release.ID, ProjectID: project.ID, Version: release.Version,
		StepsJson: `[{"name":"changed","script_body":"Write-Output changed",` +
			`"interpreter":"pwsh","execution_target":"local"}]`,
	}); err != nil {
		t.Fatal(err)
	}
	rerun, err := repo.CreateDeploymentFromDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "pending",
		},
		result.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	rerunSteps, err := repo.Queries.ListDeploymentSteps(
		t.Context(),
		rerun.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rerunSteps) != 2 {
		t.Fatalf("rerun steps = %+v", rerunSteps)
	}
	for _, step := range rerunSteps {
		if want := wantInterpreters[step.Name]; step.Interpreter != want {
			t.Fatalf(
				"rerun step %q interpreter=%q want %q",
				step.Name, step.Interpreter, want,
			)
		}
	}
	rerunSource, err := repo.Queries.GetDeploymentStepSource(
		t.Context(),
		rerun.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rerunSource.StepsJson != `[{"name":"bash","script_body":"echo ok",`+
		`"interpreter":"bash","execution_target":"local"},`+
		`{"name":"python","script_body":"print('ok')",`+
		`"interpreter":"python3","execution_target":"local"}]` {
		t.Fatalf("rerun source = %s", rerunSource.StepsJson)
	}
}
