//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (f *artifactE2E) verifyRemoteSnapshotAcceptance(
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
	// Keep the previously executed release snapshot frozen.
	unused := verificationRelease(t, f, "refresh-validation")
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) == 0 {
		t.Fatalf("missing steps: %v", err)
	}
	placement := db.SetStepExecutionTargetParams{
		ID:              steps[0].ID,
		ExecutionTarget: "agent",
	}
	if _, err := f.h.repo.DB.ExecContext(
		t.Context(),
		"UPDATE steps SET agent_execution_mode='container' WHERE id=?",
		steps[0].ID,
	); err != nil {
		t.Fatal(err)
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
		map[string]string{"version": "remote-accepted"},
		201,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", f.base(), unused.ID),
		nil,
		200,
	)
	webBase := fmt.Sprintf("/projects/%d", f.project.ID)
	f.web(
		t,
		"POST",
		webBase+"/releases",
		url.Values{"version": {"web-remote-accepted"}},
		303,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", webBase, unused.ID),
		nil,
		303,
	)
	placement.ExecutionTarget = "local"
	if _, err := f.h.repo.Queries.SetStepExecutionTarget(
		t.Context(),
		placement,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE steps SET agent_execution_mode='host' WHERE id=?", steps[0].ID,
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
