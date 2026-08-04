package handler

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestNestedObjectsMustBelongToURLProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })
	repo := repository.New(dbConn)

	projectA, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "project-a",
	})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "project-b",
	})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}
	step, err := repo.Queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID: projectB.ID,
		Name:      "project-b-step",
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	variable, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: projectB.ID,
		Name:      "project-b-secret",
		Value:     sql.NullString{String: "secret", Valid: true},
		Secret:    1,
	})
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}

	request := httptest.NewRequest("GET", "/", nil)
	stepHandler := NewStepHandler(repo)
	if _, err := stepHandler.getProjectStep(
		request,
		projectA.ID,
		step.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("cross-project step error = %v, want sql.ErrNoRows", err)
	}
	variableHandler := NewVariableHandler(repo)
	if _, err := variableHandler.getProjectVariable(
		request,
		projectA.ID,
		variable.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("cross-project variable error = %v, want sql.ErrNoRows", err)
	}

	if _, err := stepHandler.getProjectStep(
		request,
		projectB.ID,
		step.ID,
	); err != nil {
		t.Fatalf("own-project step: %v", err)
	}
	if _, err := variableHandler.getProjectVariable(
		request,
		projectB.ID,
		variable.ID,
	); err != nil {
		t.Fatalf("own-project variable: %v", err)
	}
}
