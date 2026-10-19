//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/events"
)

func TestArtifactGateCaptureFailureLogsE2E(t *testing.T) {
	for _, scenario := range []struct {
		name, script string
	}{
		{"missing-plan", `printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review"`},
		{"invalid-review", `printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf 'private-review-content' > "$DURPDEPLOY_STAGE_DIR/review"`},
		{"large-invalid-review", `printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
dd if=/dev/zero of="$DURPDEPLOY_STAGE_DIR/review" bs=1048576 count=2`},
		{"linked-context", `printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review"
ln -s /tmp/private-review-content "$DURPDEPLOY_STAGE_DIR/private-review-content"`},
		{"sparse-context", `printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review"
truncate -s 1048576 "$DURPDEPLOY_STAGE_DIR/private-review-content"`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			logs := artifactGateCaptureFailure(t, scenario.script)
			if scenario.name == "sparse-context" &&
				!strings.Contains(logs, "sparse files") {
				t.Fatal("sparse context did not report export rejection")
			}
		})
	}
}

func artifactGateCaptureFailure(t *testing.T, script string) string {
	t.Helper()
	// Given: successful generation with an invalid approval context.
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":                   "Generate",
		"container_image":        "harbor.durp.info/dockerhub/library/bash:5.2",
		"approval_artifact_path": "plan",
		"approval_review_path":   "review",
		"approval_review_format": "summary",
		"script_body":            "set -eu\n" + script,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":            "Apply",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"script_body":     "echo apply-must-not-run",
	}, 201)
	// When: the deployment tries to capture the approval context.
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "invalid-approval-context"))
	f.completion(t, deployment.ID, events.DeploymentFailed)
	// Then: the API and web logs explain the failure and apply never runs.
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	logs := string(f.api(t, "GET", path+"/logs.txt", nil, 200))
	page := f.web(t, "GET",
		fmt.Sprintf("/deployments/%d", deployment.ID), nil, 200)
	for surface, output := range map[string]string{"API": logs, "web": page} {
		if !strings.Contains(output, "Artifact approval failed:") ||
			strings.Contains(output, "apply-must-not-run") ||
			strings.Contains(output, "private-review-content") {
			t.Fatalf("%s omitted the approval failure or ran apply: %s",
				surface, output)
		}
	}
	if !strings.Contains(logs, "Step failed.") {
		t.Fatalf(
			"generation still reports success after capture failure: %s",
			logs,
		)
	}
	return logs
}
