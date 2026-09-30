package db_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestSteps_ContainerMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()

	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer dbConn.Close()
	queries := db.New(dbConn)

	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "container-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	step, err := queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:      project.ID,
		Name:           "container-step",
		ScriptBody:     "echo hi",
		SortOrder:      1,
		ContainerImage: "registry.example/worker:1",
		VariableNames:  `["SECRET"]`,
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	got, err := queries.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("get step: %v", err)
	}
	if got.ContainerImage != "registry.example/worker:1" {
		t.Fatalf(
			"container_image = %q, want registry.example/worker:1",
			got.ContainerImage,
		)
	}
	if got.VariableNames != `["SECRET"]` {
		t.Fatalf("variable_names = %q, want [\"SECRET\"]", got.VariableNames)
	}

	updated, err := queries.UpdateStep(ctx, db.UpdateStepParams{
		ID:             step.ID,
		Name:           "container-step",
		ScriptBody:     "echo hi",
		SortOrder:      1,
		ContainerImage: "",
		VariableNames:  "[]",
	})
	if err != nil {
		t.Fatalf("update step: %v", err)
	}
	if updated.ContainerImage != "" || updated.VariableNames != "[]" {
		t.Fatalf(
			"update did not clear metadata: container_image=%q variable_names=%q",
			updated.ContainerImage,
			updated.VariableNames,
		)
	}
}

func TestSteps_ContainerMetadataDefaultsOnExistingShape(t *testing.T) {
	ctx := context.Background()

	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer dbConn.Close()

	project, err := db.New(dbConn).
		CreateProject(ctx, db.CreateProjectParams{Name: "default-project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	res, err := dbConn.Exec(
		"INSERT INTO steps (project_id, name, script_body, sort_order) VALUES (?, ?, ?, ?)",
		project.ID,
		"legacy-step",
		"echo hi",
		1,
	)
	if err != nil {
		t.Fatalf("insert step without metadata columns: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	step, err := db.New(dbConn).GetStep(ctx, id)
	if err != nil {
		t.Fatalf("get step: %v", err)
	}
	if step.ContainerImage != "" {
		t.Fatalf(
			"container_image default = %q, want empty",
			step.ContainerImage,
		)
	}
	if step.VariableNames != "[]" {
		t.Fatalf("variable_names default = %q, want []", step.VariableNames)
	}
}
