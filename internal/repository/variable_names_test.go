package repository_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestProjectVariableNamesRejectInvalidWrites(t *testing.T) {
	repo := newTestRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "names"},
	)
	if err != nil {
		t.Fatal(err)
	}
	variable, err := repo.CreateVariable(t.Context(), db.CreateVariableParams{
		ProjectID: project.ID, Name: "Mixed_2", Value: sql.NullString{String: "keep", Valid: true}, Secret: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "bad name", "BAD\x00NAME", "BAD=NAME", "1BAD", "bad-name", "é", strings.Repeat("A", 256)} {
		_, err := repo.CreateVariable(
			t.Context(),
			db.CreateVariableParams{ProjectID: project.ID, Name: name},
		)
		if !errors.Is(err, repository.ErrVariableName) {
			t.Fatalf("create %q: %v", name, err)
		}
		_, err = repo.UpdateVariable(
			t.Context(),
			db.UpdateVariableParams{ID: variable.ID, Name: name},
		)
		if !errors.Is(err, repository.ErrVariableName) {
			t.Fatalf("update %q: %v", name, err)
		}
		_, err = repo.UpdateVariableKeepValue(
			t.Context(),
			db.UpdateVariableKeepValueParams{ID: variable.ID, Name: name},
		)
		if !errors.Is(err, repository.ErrVariableName) {
			t.Fatalf("keep-value update %q: %v", name, err)
		}
	}
	saved, err := repo.GetVariable(t.Context(), variable.ID)
	if err != nil || saved != variable {
		t.Fatalf("rejected writes changed original: %+v, %v", saved, err)
	}
	variables, err := repo.ListVariablesByProject(t.Context(), project.ID)
	if err != nil || len(variables) != 1 {
		t.Fatalf("invalid creates persisted: %+v, %v", variables, err)
	}
	updated, err := repo.UpdateVariableKeepValue(
		t.Context(),
		db.UpdateVariableKeepValueParams{
			ID: variable.ID, Name: "_Valid_2", Secret: 1,
		},
	)
	if err != nil || updated.Name != "_Valid_2" ||
		updated.Value != variable.Value ||
		updated.Secret != 1 {
		t.Fatalf(
			"valid rename did not preserve the encrypted secret: %+v, %v",
			updated,
			err,
		)
	}
}
