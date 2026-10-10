package repository_test

import (
	"database/sql"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

func TestLifecycleVariablesRunbookSnapshots(t *testing.T) {
	repo := newTestRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	lc, err := repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "shared"},
	)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "consumer"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.SetProjectLifecycle(
		t.Context(),
		db.SetProjectLifecycleParams{
			ID:          project.ID,
			LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true},
		},
	); err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "stage"},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := repo.Queries.CreateLifecycleStage(
		t.Context(),
		db.CreateLifecycleStageParams{
			LifecycleID:   lc.ID,
			EnvironmentID: env.ID,
			SortOrder:     1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input := repository.LifecycleVariableInput{
		CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
			LifecycleID:   lc.ID,
			Name:          "REGION",
			Value:         sql.NullString{String: "first-secret", Valid: true},
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
			Secret:        1,
		},
	}
	v, err := repo.SaveLifecycleVariable(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	book, first, err := repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: project.ID,
			Name:      "maintenance",
			StepsJSON: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input.ID = v.ID
	input.Value.String = "second-secret"
	if _, err := repo.SaveLifecycleVariable(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	_, second, err := repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: project.ID,
			RunbookID: book.ID,
			StepsJSON: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var captured []int64
	for _, releaseID := range []int64{first.ReleaseID, second.ReleaseID} {
		stored, err := repo.Queries.ListReleaseVariablesByRelease(
			t.Context(),
			releaseID,
		)
		if err != nil || len(stored) != 0 {
			t.Fatal("lifecycle variables saved in release", err)
		}
		result, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: releaseID, EnvironmentID: env.ID, Status: "pending",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		captured = append(captured, result.Deployment.ID)
		snapshot, err := repo.Queries.GetDeploymentVariableSnapshot(
			t.Context(),
			result.Deployment.ID,
		)
		if err != nil || strings.Contains(snapshot.Value, "second-secret") {
			t.Fatal("deployment snapshot not encrypted", err)
		}
		plain, err := repo.ListDeploymentVariables(
			t.Context(),
			result.Deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := runner.ResolveReleaseVariables(plain, env.ID)
		if err != nil || len(resolved) != 1 ||
			resolved[0].Value != "second-secret" ||
			!resolved[0].Secret {
			t.Fatal(
				"runbook execution did not resolve current shared value",
				err,
			)
		}
	}
	input.Value.String = "third-secret"
	if _, err := repo.SaveLifecycleVariable(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	for _, id := range captured {
		plain, err := repo.ListDeploymentVariables(t.Context(), id)
		if err != nil || len(plain) != 1 ||
			plain[0].Value.String != "second-secret" {
			t.Fatal("active execution changed after a shared edit", err)
		}
	}
	if err := repo.Queries.DeleteLifecycleStage(
		t.Context(),
		stage.ID,
	); err != nil {
		t.Fatal(err)
	}
	merged, err := repo.InheritedVariables(
		t.Context(),
		db.Project{LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true}},
		nil,
	)
	if err != nil || len(merged.Variables) != 0 {
		t.Fatal("removed stage still inherited", err)
	}
	if _, err := repo.SaveLifecycleVariable(t.Context(), input); err == nil {
		t.Fatal("removed stage accepted new scoped value")
	}
}
