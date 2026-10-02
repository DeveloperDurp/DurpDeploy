//go:build e2e && artifactstress

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactCapacityE2E(t *testing.T) {
	// Given: a ZIP at the logical byte, entry, and implicit-node limits.
	filename := nearLimitArtifact(t)
	f := newArtifactE2E(t)
	f.changePackage("file:" + filename)
	f.completionTimeout = 3 * time.Minute
	if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") != "docker" {
		t.Log(
			"local Podman checks contents; CI uses Docker to enforce tmpfs capacity",
		)
	}
	script := fmt.Sprintf(`set -eu
test "$(find "$ARTIFACT_PATH" -type f | wc -l)" -eq %d
test "$(find "$ARTIFACT_PATH" -type d | wc -l)" -eq %d
bytes=$(find "$ARTIFACT_PATH" -type f -exec stat -c %%s {} + | awk '{n += $1} END {printf "%%.0f", n}')
test "$bytes" -eq %d
! touch "$ARTIFACT_PATH/write"
awk '$5 == "/artifacts" {if ($6 !~ /ro/ || $6 !~ /noexec/) exit 1; found=1} END {exit !found}' /proc/self/mountinfo
echo capacity-verified
`, artifact.MaxFiles, artifact.MaxStagingNodes-artifact.MaxFiles+1, artifact.MaxExtracted)
	_, release := f.createArtifactRelease(t, script)

	// When: the public API stages and executes this maximum accepted release.
	body := f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 201)
	var deployment db.Deployment
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)

	// Then: every node and logical byte is readable under ro/noexec restrictions.
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(string(logs), "capacity-verified") {
		t.Fatalf("near-limit package was not verified: %s", logs)
	}
}
