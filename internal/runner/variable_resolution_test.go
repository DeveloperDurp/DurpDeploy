package runner

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestResolveReleaseVariables_EnvironmentOverridesGlobalLastWrite(
	t *testing.T,
) {
	// Given
	variables := releaseVariablePrecedenceFixture()

	// When
	resolved := ResolveReleaseVariables(variables, 7)

	// Then
	if len(resolved) != 1 || resolved[0].Value.String != "last" {
		t.Fatalf("resolved = %#v, want final environment value", resolved)
	}
}

func TestReleaseEnvironment_MatchesRemotePrecedence(t *testing.T) {
	// Given
	variables := releaseVariablePrecedenceFixture()

	// When
	environment, _ := releaseEnvironment(variables, 7)

	// Then
	if len(environment) != 1 || environment["REGION"] != "last" {
		t.Fatalf(
			"environment = %#v, want one last environment REGION",
			environment,
		)
	}
}

func releaseVariablePrecedenceFixture() []db.ReleaseVariable {
	return []db.ReleaseVariable{
		{Name: "REGION", Value: sql.NullString{String: "global", Valid: true}},
		{
			Name:  "REGION",
			Value: sql.NullString{String: "first", Valid: true},
			EnvironmentID: sql.NullInt64{
				Int64: 7,
				Valid: true,
			},
		},
		{
			Name:  "REGION",
			Value: sql.NullString{String: "last", Valid: true},
			EnvironmentID: sql.NullInt64{
				Int64: 7,
				Valid: true,
			},
		},
	}
}
