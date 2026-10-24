package repository_test

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestAvailableVariablesMasksWithoutDecryptingSecrets(t *testing.T) {
	repo := newTestRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "catalog"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "catalog"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := repo.Queries.CreateVariable(
			t.Context(),
			db.CreateVariableParams{
				ProjectID: project.ID, Name: "LEGACY_DUPLICATE",
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.Queries.CreateVariable(
		t.Context(),
		db.CreateVariableParams{
			ProjectID: project.ID, Name: "SECRET", Secret: 1,
			Value: sql.NullString{String: "not-decryptable", Valid: true},
		},
	); err != nil {
		t.Fatal(err)
	}
	preview := repository.VariableContext{
		EnvironmentID: environment.ID,
		Target:        "local",
		AgentMode:     "host",
		Interpreter:   "bash",
	}
	catalog, err := repo.AvailableVariables(t.Context(), project, preview)
	if err != nil {
		t.Fatalf("display attempted to decrypt a secret: %v", err)
	}
	if len(catalog.Configured) != 3 ||
		catalog.Configured[0].Status != "overridden" ||
		catalog.Configured[0].OverriddenBy != catalog.Configured[1].Key ||
		!catalog.Configured[1].Effective ||
		catalog.Configured[2].Value != nil {
		t.Fatal("legacy last-ID precedence or secret masking changed")
	}
	if _, err := repo.ListVariablesByProject(
		t.Context(),
		project.ID,
	); err == nil {
		t.Fatal("fixture must fail when secret decryption is attempted")
	}
}
