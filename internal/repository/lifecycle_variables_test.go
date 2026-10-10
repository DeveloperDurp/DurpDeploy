package repository_test

import (
	"database/sql"
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
	if err := repo.Queries.SetProjectLifecycle(t.Context(), db.SetProjectLifecycleParams{ID: project.ID, LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true}}); err != nil {
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
	for _, test := range []struct {
		release int64
		want    string
	}{{first.ReleaseID, "first-secret"}, {second.ReleaseID, "second-secret"}} {
		stored, err := repo.Queries.ListReleaseVariablesByRelease(
			t.Context(),
			test.release,
		)
		if err != nil || len(stored) != 1 ||
			stored[0].Value.String == test.want {
			t.Fatal("snapshot ciphertext absent", err)
		}
		plain, err := repo.ListReleaseVariablesByRelease(
			t.Context(),
			test.release,
		)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := runner.ResolveReleaseVariables(plain, env.ID)
		if err != nil || len(resolved) != 1 || resolved[0].Value != test.want ||
			!resolved[0].Secret {
			t.Fatal("snapshot changed", err)
		}
	}
	if err := repo.Queries.DeleteLifecycleStage(t.Context(), stage.ID); err != nil {
		t.Fatal(err)
	}
	merged, err := repo.ProjectSnapshotVariables(t.Context(), project.ID)
	if err != nil || len(merged) != 0 {
		t.Fatal("removed stage still inherited", err)
	}
	if _, err := repo.SaveLifecycleVariable(t.Context(), input); err == nil {
		t.Fatal("removed stage accepted new scoped value")
	}
}
