package handler_test

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

func TestProjectExecutionPolicy_HandlerPersistenceUsesTypedResolver(
	t *testing.T,
) {
	ctx := context.Background()
	repo := repository.New(newHandlerTestDatabase(t))
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name:        "handler-policy",
			Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	label, err := repo.Queries.CreateAgentLabel(
		ctx,
		db.CreateAgentLabelParams{Name: "Cat Fact", NormalizedName: "cat fact"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	policy, err := handler.ResolveProjectExecutionPolicy(
		ctx,
		repo,
		handler.ProjectExecutionPolicyInput{
			TargetMode: "label", LabelID: label.ID, Strategy: "round_robin",
		},
	)
	if err != nil {
		t.Fatalf("resolve typed policy: %v", err)
	}
	if err := handler.SaveProjectExecutionPolicy(
		ctx,
		repo.Queries,
		project.ID,
		policy,
	); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	stored, err := handler.LoadProjectExecutionPolicy(
		ctx,
		repo.Queries,
		project.ID,
	)
	if err != nil || stored.LabelName != "Cat Fact" ||
		stored.Strategy != "round_robin" {
		t.Fatalf("stored policy = %#v, %v", stored, err)
	}
	t.Logf(
		"SQLite policy: project=%d mode=%s label=%q strategy=%s",
		project.ID,
		stored.TargetMode,
		stored.LabelName,
		stored.Strategy,
	)
}
