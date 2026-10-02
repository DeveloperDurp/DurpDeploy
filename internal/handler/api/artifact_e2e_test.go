//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactsAPIWebContainerE2E(t *testing.T) {
	// Given: real HTTP, HTTPS repository, migrated DB, and Podman containers.
	f := newArtifactE2E(t)
	base := f.base()
	f.verifyLegacyArtifactVariable(t)
	webBase := fmt.Sprintf("/projects/%d", f.project.ID)
	f.web(t, "GET", webBase+"/package-repository/edit", nil, 200)
	f.web(
		t,
		"POST",
		webBase+"/package-repository",
		url.Values{
			"url_template": {f.upstream + "/{version}.zip"},
			"auth_type":    {"bearer"},
			"credential":   {"artifact-secret"},
		},
		303,
	)
	data := f.api(
		t,
		"PUT",
		base+"/package-repository",
		map[string]any{
			"url_template": f.upstream + "/{version}.zip",
			"auth_type":    "bearer",
			"credential":   "artifact-secret",
		},
		200,
	)
	if strings.Contains(string(data), "artifact-secret") {
		t.Fatal("credential exposed")
	}
	var source struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	f.verifyPackageVersionCheck(t)
	script := `set -eu
test "$(cat "$ARTIFACT_PATH/app.txt")" = package
! touch "$ARTIFACT_PATH/write"
! "$ARTIFACT_PATH/app.txt"
awk '$5 == "/artifacts" { if ($6 !~ /ro/ || $6 !~ /noexec/) exit 1; found=1 } END { exit !found }' /proc/self/mountinfo
printf 'artifact-readable\n'
`
	for _, name := range []string{"first", "second"} {
		f.api(
			t,
			"POST",
			base+"/steps",
			map[string]any{
				"name":            name,
				"script_body":     script,
				"interpreter":     "bash",
				"container_image": "docker.io/library/bash:5.2",
				"variable_names":  []string{"LIMITED"},
			},
			201,
		)
	}
	f.api(
		t,
		"POST",
		base+"/variables",
		map[string]any{"name": "LIMITED", "value": "selected"},
		201,
	)
	f.api(
		t,
		"POST",
		base+"/variables",
		map[string]string{"name": "ARTIFACT_PATH", "value": "override"},
		422,
	)
	f.web(
		t,
		"POST",
		webBase+"/variables",
		url.Values{"name": {"ARTIFACT_PATH"}, "value": {"override"}},
		422,
	)
	f.changePackage("metadata-heavy")
	quotaError := f.api(
		t,
		"POST",
		base+"/releases",
		map[string]string{"version": "metadata-rejected"},
		422,
	)
	if !strings.Contains(string(quotaError), "metadata") {
		t.Fatalf("metadata rejection not explained: %s", quotaError)
	}
	f.changePackage("package")
	data = f.api(
		t,
		"POST",
		base+"/releases",
		map[string]string{"version": "1.2.3"},
		201,
	)
	var release db.Release
	if err := json.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	pin := f.api(
		t,
		"GET",
		fmt.Sprintf("%s/releases/%d/artifact", base, release.ID),
		nil,
		200,
	)
	if !strings.Contains(string(pin), `"sha256"`) {
		t.Fatal("release pin missing")
	}
	f.verifyRemoteSnapshotRejection(t)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("%s/releases/%d", webBase, release.ID),
		nil,
		200,
	)
	if !strings.Contains(page, "Pinned ZIP package") {
		t.Fatal("web pin missing")
	}
	// When: deployment steps consume the package through the public API.
	data = f.api(
		t,
		"POST",
		base+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	)
	var deployment db.Deployment
	if err := json.Unmarshal(data, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", base, release.ID),
		nil,
		409,
	)
	// Then: both steps read the same pin under effective ro/noexec restrictions.
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
	if strings.Count(string(logs), "artifact-readable") != 2 {
		t.Fatalf("step output missing: %s", logs)
	}
	runbookVersion := f.verifyRunbookPackage(t, release.ID, script)
	f.api(
		t,
		"DELETE",
		base+"/package-repository",
		nil,
		204,
	)
	f.changePackage("republished")
	data = f.api(
		t,
		"POST",
		base+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	)
	if err := json.Unmarshal(data, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentFailed)
	f.changePackage("package")
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/releases/%d", base, release.ID),
		nil,
		204,
	)
	data = f.api(
		t,
		"GET",
		fmt.Sprintf(
			"%s/runbooks/%d/versions/%d",
			base,
			runbookVersion.RunbookID,
			runbookVersion.ID,
		),
		nil,
		200,
	)
	var retained struct {
		Artifact struct {
			SHA256          string `json:"sha256"`
			SourceReleaseID *int64 `json:"source_release_id"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(data, &retained); err != nil {
		t.Fatal(err)
	}
	if retained.Artifact.SHA256 == "" ||
		retained.Artifact.SourceReleaseID != nil {
		t.Fatal(
			"source deletion lost the copied pin or retained a dangling reference",
		)
	}
	data = f.api(
		t,
		"POST",
		fmt.Sprintf(
			"%s/runbooks/%d/executions",
			base,
			runbookVersion.RunbookID,
		),
		map[string]int64{
			"environment_id": f.environment.ID,
			"version_id":     runbookVersion.ID,
		},
		201,
	)
	var execution db.RunbookExecution
	if err := json.Unmarshal(data, &execution); err != nil {
		t.Fatal(err)
	}
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	f.verifyOrphanedPinEdits(t, runbookVersion, script)
}
