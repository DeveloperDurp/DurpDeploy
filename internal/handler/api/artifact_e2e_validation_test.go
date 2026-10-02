//go:build e2e

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func (f *artifactE2E) verifyRemoteSnapshotRejection(
	t *testing.T,
	releaseID int64,
) {
	t.Helper()
	for _, version := range []string{".", ".."} {
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": version},
			422,
		)
	}
	f.changePackage("long-name")
	f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{"version": "path-rejected"},
		422,
	)
	f.changePackage("package")
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) == 0 {
		t.Fatalf("missing steps: %v", err)
	}
	placement := db.SetStepExecutionTargetParams{
		ID:              steps[0].ID,
		ExecutionTarget: "agent",
	}
	if _, err := f.h.repo.Queries.SetStepExecutionTarget(
		t.Context(),
		placement,
	); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{"version": "remote-rejected"},
		422,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", f.base(), releaseID),
		nil,
		422,
	)
	placement.ExecutionTarget = "local"
	if _, err := f.h.repo.Queries.SetStepExecutionTarget(
		t.Context(),
		placement,
	); err != nil {
		t.Fatal(err)
	}
}
