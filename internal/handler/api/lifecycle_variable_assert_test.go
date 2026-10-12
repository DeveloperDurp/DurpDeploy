package api_test

import (
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"testing"
)

func assertLifecycleReleaseValue(
	t *testing.T,
	repo *repository.Repository,
	releaseID, envID int64,
	want string,
) {
	t.Helper()
	variables, err := repo.ListReleaseVariablesByRelease(t.Context(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := runner.ResolveReleaseVariables(variables, envID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range resolved {
		if value.Name == "REGION" {
			if value.Value != want {
				t.Fatalf("REGION=%q want %q", value.Value, want)
			}
			return
		}
	}
	t.Fatal("REGION absent")
}
