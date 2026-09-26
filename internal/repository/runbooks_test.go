package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

func TestRunbookExecutionKeepsVersionedEnvironmentSecret(t *testing.T) {
	repo := newTestRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "runbook-secret-project"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "runbook-secret-env"})
	if err != nil {
		t.Fatal(err)
	}
	variable, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: project.ID, Name: "API_TOKEN",
		Value: sql.NullString{String: "first-secret", Valid: true},
		EnvironmentID: sql.NullInt64{
			Int64: environment.ID, Valid: true,
		},
		Secret: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "runbook:1:1", StepsJson: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	book, first, err := repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID, Name: "maintenance",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateVariable(ctx, db.UpdateVariableParams{
		ID: variable.ID, Name: variable.Name,
		Value:         sql.NullString{String: "second-secret", Valid: true},
		EnvironmentID: variable.EnvironmentID, Secret: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, second, err := repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID, RunbookID: book.ID,
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email: "runbook-actor@example.com", PasswordHash: "test",
		Name: "Runbook Actor", Role: "deployer",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		versionID int64
		releaseID int64
		want      string
	}{
		{first.ID, first.ReleaseID, "first-secret"},
		{second.ID, second.ReleaseID, "second-secret"},
	} {
		if _, _, err := repo.CreateRunbookExecution(ctx,
			repository.RunbookExecutionRequest{
				ProjectID: project.ID, RunbookID: book.ID,
				VersionID:     test.versionID,
				EnvironmentID: environment.ID,
				ActorUserID:   sql.NullInt64{Int64: actor.ID, Valid: true},
			}); err != nil {
			t.Fatal(err)
		}
		stored, err := repo.Queries.ListReleaseVariablesByRelease(ctx,
			test.releaseID)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != 1 || strings.Contains(stored[0].Value.String,
			test.want) {
			t.Fatalf("release secret was not encrypted at rest")
		}
		variables, err := repo.ListReleaseVariablesByRelease(ctx,
			test.releaseID)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := runner.ResolveReleaseVariables(variables,
			environment.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(resolved) != 1 || resolved[0].Value != test.want {
			t.Fatalf("resolved variables=%+v want %q", resolved,
				test.want)
		}
	}
	if err := repo.Queries.DeleteUser(ctx, actor.ID); err != nil {
		t.Fatal(err)
	}
	executions, err := repo.Queries.ListRunbookExecutions(
		ctx,
		db.ListRunbookExecutionsParams{ProjectID: project.ID, Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range executions {
		if execution.ActorUserID.Valid {
			t.Fatalf("deleted actor still referenced: %+v", execution)
		}
	}
	_, _, err = repo.CreateRunbookExecution(ctx,
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID,
			VersionID:     second.ID + 100,
			EnvironmentID: environment.ID,
		})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid version error=%v", err)
	}
}
