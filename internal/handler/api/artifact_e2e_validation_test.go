//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (f *artifactE2E) verifyRemoteSnapshotRejection(
	t *testing.T,
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
	rejectInvalidArtifactPull(t, f, "path-rejected")
	f.changePackage("package")
	// Use a separate release for malformed-package refresh validation.
	unused := verificationRelease(t, f, "refresh-validation")
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
		fmt.Sprintf("%s/releases/%d/refresh", f.base(), unused.ID),
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

func rejectInvalidArtifactPull(
	t *testing.T,
	f *artifactE2E,
	version string,
) []byte {
	t.Helper()
	body := f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{"version": version},
		201,
	)
	var release db.Release
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	body = f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 201)
	var deployment db.Deployment
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentFailed)
	return f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
}
