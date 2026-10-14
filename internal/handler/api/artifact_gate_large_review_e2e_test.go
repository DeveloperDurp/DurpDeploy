//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/events"
	"durpdeploy/views/pages"
)

func TestArtifactGateLargeTerraformReviewE2E(t *testing.T) {
	t.Run(
		"large review",
		func(t *testing.T) { artifactGateLargeReview(t, false) },
	)
	t.Run(
		"large bundle",
		func(t *testing.T) { artifactGateLargeReview(t, true) },
	)
}

func artifactGateLargeReview(t *testing.T, largeBundle bool) {
	t.Helper()
	// Given: a Terraform review larger than 1 MiB because it includes prior state.
	f := newArtifactE2E(t)
	script := `set -eu
printf exact-plan > "$DURPDEPLOY_STAGE_DIR/tfplan"
{
  printf '{"format_version":"1.2","prior_state":{"values":{"outputs":{"state":{"value":"'
  printf '%02000000d' 0
  printf 'prior-state-hidden"}}}},"resource_changes":[{"address":"example.large","change":{"actions":["create"],"after":{"message":"visible change","password":"private-review-content"},"after_sensitive":{"password":true}}}]}'
} > "$DURPDEPLOY_STAGE_DIR/review.json"`
	apply := `set -eu
test "$(cat "$DURPDEPLOY_APPROVED_DIR/tfplan")" = exact-plan
test "$(wc -c < "$DURPDEPLOY_APPROVED_DIR/review.json")" -gt 1048576
echo applied-large-review`
	if largeBundle {
		f.completionTimeout = 3 * time.Minute
		f.client.Timeout = 3 * time.Minute
		script += `
dd if=/dev/zero of="$DURPDEPLOY_STAGE_DIR/context.bin" bs=1048576 count=301`
		apply += `
test "$(wc -c < "$DURPDEPLOY_APPROVED_DIR/context.bin")" -gt 314572800
echo applied-large-bundle`
	}
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":                   "Generate",
		"container_image":        "docker.io/library/bash:5.2",
		"approval_artifact_path": "tfplan",
		"approval_review_path":   "review.json",
		"approval_review_format": "terraform",
		"script_body":            script,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":            "Apply",
		"container_image": "docker.io/library/bash:5.2",
		"script_body":     apply,
	}, 201)
	// When: the public API starts generation and reads the saved review.
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "large-terraform-review"))
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	var gates []pages.ArtifactGateInfo
	path := gateAPIPath(deployment.ID)
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &gates); err != nil ||
		len(gates) != 1 ||
		gates[0].Review.Create != 1 {
		t.Fatalf("large review gate=%+v err=%v", gates, err)
	}
	if string(f.api(t, "GET", path+"/0/artifact", nil, 200)) != "exact-plan" {
		t.Fatal("large context changed the saved artifact")
	}
	apiReview := string(f.api(t, "GET", path+"/0/review", nil, 200))
	webPath := fmt.Sprintf("/deployments/%d/artifact-gates/0", deployment.ID)
	webReview := f.web(t, "GET", webPath+"/review", nil, 200)
	// Then: both review surfaces retain changes and omit sensitive prior state.
	for surface, body := range map[string]string{"API": apiReview, "web": webReview} {
		if !strings.Contains(body, "example.large") ||
			!strings.Contains(body, "visible change") ||
			!strings.Contains(body, "[REDACTED]") ||
			strings.Contains(body, "private-review-content") ||
			strings.Contains(body, "prior-state-hidden") || len(body) > 1<<20 {
			t.Fatalf("%s exposed or lost large review data", surface)
		}
	}
	// Web approval resumes execution using the complete retained context.
	f.web(t, "POST", webPath+"/approve", url.Values{
		"revision": {fmt.Sprint(gates[0].Revision)},
		"sha256":   {gates[0].SHA256},
	}, 303)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := string(f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d/logs.txt", deployment.ID), nil, 200))
	if !strings.Contains(logs, "applied-large-review") ||
		(largeBundle && !strings.Contains(logs, "applied-large-bundle")) ||
		strings.Contains(logs, "private-review-content") ||
		strings.Contains(logs, "prior-state-hidden") {
		t.Fatal("large review continuation failed or exposed state")
	}
}
