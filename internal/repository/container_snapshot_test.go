package repository_test

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func newContainerSnapshotRepo(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}

func TestCreateDeploymentMaterializesContainerFields(t *testing.T) {
	repo := newContainerSnapshotRepo(t)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "containers"},
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
			StepsJson: `[{"name":"containerized","script_body":"echo hi",` +
				`"interpreter":"bash","execution_target":"local",` +
				`"container_image":"alpine:3.20",` +
				`"variable_names":["APP_ENV","DEBUG"]},` +
				`{"name":"remote","script_body":"echo remote",` +
				`"interpreter":"bash","execution_target":"agent"}]`,
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
	if steps[0].ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"container image = %q, want alpine:3.20",
			steps[0].ContainerImage,
		)
	}
	if steps[0].VariableNames != `["APP_ENV","DEBUG"]` {
		t.Fatalf(
			"variable names = %q, want [\"APP_ENV\",\"DEBUG\"]",
			steps[0].VariableNames,
		)
	}
	if steps[1].ContainerImage != "" {
		t.Fatalf(
			"agent container image = %q, want empty",
			steps[1].ContainerImage,
		)
	}
	if steps[1].VariableNames != "[]" {
		t.Fatalf(
			"agent variable names = %q, want []",
			steps[1].VariableNames,
		)
	}
}

func TestRerunDeploymentCopiesContainerFields(t *testing.T) {
	repo := newContainerSnapshotRepo(t)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "rerun"},
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
			StepsJson: `[{"name":"deploy","script_body":"echo hi",` +
				`"interpreter":"bash","execution_target":"local",` +
				`"container_image":"alpine:3.20",` +
				`"variable_names":["TOKEN_NAME"]}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rerun, err := repo.CreateDeploymentFromDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "pending",
		},
		first.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := repo.Queries.ListDeploymentSteps(
		t.Context(), rerun.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("rerun steps = %+v", steps)
	}
	if steps[0].ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"rerun container image = %q, want alpine:3.20",
			steps[0].ContainerImage,
		)
	}
	if steps[0].VariableNames != `["TOKEN_NAME"]` {
		t.Fatalf(
			"rerun variable names = %q, want [\"TOKEN_NAME\"]",
			steps[0].VariableNames,
		)
	}
}

func TestStepTemplateVersionRetainsContainerMetadata(t *testing.T) {
	repo := newContainerSnapshotRepo(t)
	template, err := repo.CreateStepTemplateWithPlacement(
		t.Context(),
		db.CreateStepTemplateParams{
			Name: "deploy-app", ScriptBody: "echo hi", Interpreter: "bash",
			ContainerImage: "alpine:3.20", VariableNames: `["APP_ENV"]`,
		},
		"local", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := repo.Queries.ListStepTemplateVersions(
		t.Context(), template.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %+v", versions)
	}
	if versions[0].ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"version image = %q, want alpine:3.20",
			versions[0].ContainerImage,
		)
	}
	if versions[0].VariableNames != `["APP_ENV"]` {
		t.Fatalf(
			"version variable names = %q, want [\"APP_ENV\"]",
			versions[0].VariableNames,
		)
	}

	updated, err := repo.UpdateStepTemplateWithPlacement(
		t.Context(),
		db.UpdateStepTemplateParams{
			ID: template.ID, Name: template.Name,
			ScriptBody: template.ScriptBody, Interpreter: "bash",
			ContainerImage: "busybox:1.36", VariableNames: "[]",
		},
		"local", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	versions, err = repo.Queries.ListStepTemplateVersions(
		t.Context(), updated.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions after update = %+v", versions)
	}
	if versions[0].VersionNumber != 2 ||
		versions[0].ContainerImage != "busybox:1.36" {
		t.Fatalf(
			"version 2 image = %+v, want busybox:1.36",
			versions[0],
		)
	}
	if versions[0].VariableNames != "[]" {
		t.Fatalf(
			"version 2 variable names = %q, want []",
			versions[0].VariableNames,
		)
	}
	if versions[1].VersionNumber != 1 ||
		versions[1].ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"version 1 image changed to %+v, want alpine:3.20",
			versions[1],
		)
	}
}
