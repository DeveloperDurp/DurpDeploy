package runner

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestResolveReleaseVariables_EnvironmentOverridesGlobal(t *testing.T) {
	variables := []db.ReleaseVariable{
		{ID: 4, Name: "TARGET", Value: sql.NullString{String: "old", Valid: true}, Secret: 0},
		{ID: 1, Name: "TARGET", Value: sql.NullString{String: "global", Valid: true}},
		{ID: 3, Name: "TARGET", Value: sql.NullString{String: "staging", Valid: true}, EnvironmentID: sql.NullInt64{Int64: 7, Valid: true}, Secret: 1},
		{ID: 2, Name: "OTHER", Value: sql.NullString{String: "value", Valid: true}},
	}

	resolved, err := ResolveReleaseVariables(variables, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 || resolved[0].Name != "TARGET" || resolved[0].Value != "staging" || !resolved[0].Secret || resolved[1].Name != "OTHER" {
		t.Fatalf("resolved variables = %+v", resolved)
	}
}

func TestResolveReleaseVariables_RejectsEmptyName(t *testing.T) {
	_, err := ResolveReleaseVariables([]db.ReleaseVariable{{ID: 1, Name: "  "}}, 7)
	if err == nil {
		t.Fatal("expected empty variable name rejection")
	}
}
