package runner

import (
	"database/sql"
	"slices"
	"testing"

	"durpdeploy/internal/db"
)

func TestResolveDeploymentVariablesOwnsAcceptedIdentity(t *testing.T) {
	deployment := db.Deployment{EnvironmentID: 42,
		EnvironmentNameSnapshot: sql.NullString{
			String: `dev $(touch /tmp/unwanted); 'quoted'`,
			Valid:  true,
		},
	}
	variables := []db.ReleaseVariable{
		{
			Name:   "DURPDEPLOY_ENVIRONMENT_NAME",
			Value:  sql.NullString{String: "wrong", Valid: true},
			Secret: 1,
		},
		{
			Name:  "DURPDEPLOY_ENVIRONMENT_ID",
			Value: sql.NullString{String: "999", Valid: true},
		},
		{
			Name:  "KEPT",
			Value: sql.NullString{String: "configured", Valid: true},
		},
	}
	resolved, err := ResolveDeploymentVariables(variables, deployment)
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedVariable{
		{Name: "KEPT", Value: "configured"},
		{
			Name:  "DURPDEPLOY_ENVIRONMENT_NAME",
			Value: deployment.EnvironmentNameSnapshot.String,
		},
		{Name: "DURPDEPLOY_ENVIRONMENT_ID", Value: "42"},
	}
	if !slices.Equal(resolved, want) {
		t.Fatalf("resolved=%+v, want %+v", resolved, want)
	}
	configured, err := ResolveReleaseVariables(
		variables,
		deployment.EnvironmentID,
	)
	if err != nil || !slices.Contains(configured, ResolvedVariable{
		Name: "DURPDEPLOY_ENVIRONMENT_NAME", Value: "wrong", Secret: true,
	}) {
		t.Fatalf(
			"legacy secret missing from historical redaction: %+v: %v",
			configured,
			err,
		)
	}
	deployment.EnvironmentNameSnapshot.Valid = false
	if _, err := ResolveDeploymentVariables(variables, deployment); err == nil {
		t.Fatal("accepted execution without captured identity")
	}
}

func TestLocalIdentityBypassesAllowlistAsEnvironmentData(t *testing.T) {
	for _, names := range [][]string{nil, {"KEPT"}, {"DURPDEPLOY_ENVIRONMENT_NAME"}} {
		request := localStepAttempt{
			step: deploymentStep{VariableNames: names},
			environment: map[string]string{
				"KEPT": "configured", "OTHER": "excluded",
				"DURPDEPLOY_ENVIRONMENT_NAME": `dev $(touch /tmp/unwanted); 'quoted'`,
				"DURPDEPLOY_ENVIRONMENT_ID":   "42",
			},
		}
		entries, args, err := request.containerEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range []string{
			`DURPDEPLOY_ENVIRONMENT_NAME=dev $(touch /tmp/unwanted); 'quoted'`,
			"DURPDEPLOY_ENVIRONMENT_ID=42",
		} {
			if !slices.Contains(entries, entry) {
				t.Fatalf("missing runtime identity: %q", entries)
			}
		}
		if len(names) > 0 && slices.Contains(entries, "OTHER=excluded") {
			t.Fatal("identity bypass broadened configured-variable access")
		}
		if len(args) != 2*len(entries) || len(names) > 0 && len(names) != 1 {
			t.Fatal("environment argument pairing or stored allowlist changed")
		}
	}
}

func TestResolveReleaseVariables_EnvironmentOverridesGlobal(t *testing.T) {
	variables := []db.ReleaseVariable{
		{
			ID:     4,
			Name:   "TARGET",
			Value:  sql.NullString{String: "old", Valid: true},
			Secret: 0,
		},
		{
			ID:    1,
			Name:  "TARGET",
			Value: sql.NullString{String: "global", Valid: true},
		},
		{
			ID:            3,
			Name:          "TARGET",
			Value:         sql.NullString{String: "staging", Valid: true},
			EnvironmentID: sql.NullInt64{Int64: 7, Valid: true},
			Secret:        1,
		},
		{
			ID:    2,
			Name:  "OTHER",
			Value: sql.NullString{String: "value", Valid: true},
		},
	}

	resolved, err := ResolveReleaseVariables(variables, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 || resolved[0].Name != "TARGET" ||
		resolved[0].Value != "staging" ||
		!resolved[0].Secret ||
		resolved[1].Name != "OTHER" {
		t.Fatalf("resolved variables = %+v", resolved)
	}
}

func TestResolveReleaseVariables_RejectsEmptyName(t *testing.T) {
	_, err := ResolveReleaseVariables(
		[]db.ReleaseVariable{{ID: 1, Name: "  "}},
		7,
	)
	if err == nil {
		t.Fatal("expected empty variable name rejection")
	}
}
