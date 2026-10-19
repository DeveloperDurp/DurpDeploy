//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/events"
	"durpdeploy/views/pages"
)

const oversizedPreviewScript = `set -eu
printf exact-plan > "$DURPDEPLOY_STAGE_DIR/tfplan"
{
  printf '{"format_version":"1.2","resource_changes":[{"address":"example.large","change":{"actions":["create"],"after":"'
  printf '%02000000d' 0
  printf 'private-review-content","after_sensitive":false}}'
  i=0
  while [ "$i" -lt 110 ]; do
    printf ',{"address":"example.extra","change":{"actions":["create"]}}'
    i=$((i+1))
  done
  printf ']}'
} > "$DURPDEPLOY_STAGE_DIR/review.json"`

func TestArtifactGateBoundedPreviewE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Generate", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"approval_artifact_path": "tfplan", "approval_review_path": "review.json",
		"approval_review_format": "terraform", "script_body": oversizedPreviewScript,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Apply", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"script_body": `set -eu
test "$(cat "$DURPDEPLOY_APPROVED_DIR/tfplan")" = exact-plan
test "$(wc -c < "$DURPDEPLOY_APPROVED_DIR/review.json")" -gt 2000000
grep -q private-review-content "$DURPDEPLOY_APPROVED_DIR/review.json"
echo applied-complete-context`,
	}, 201)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "bounded-preview"),
	)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	path := gateAPIPath(deployment.ID)
	var gates []pages.ArtifactGateInfo
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &gates); err != nil ||
		len(gates) != 1 ||
		gates[0].Review.Create != 111 {
		t.Fatalf("gate counts=%+v err=%v", gates, err)
	}
	var preview artifact.TerraformReviewResponse
	if err := json.Unmarshal(f.api(t, "GET", path+"/0/review", nil, 200), &preview); err != nil ||
		!preview.PreviewTruncated ||
		preview.ResourcesOmitted != 11 ||
		len(preview.Resources) != 100 {
		t.Fatalf(
			"preview resources=%d omitted=%d err=%v",
			len(preview.Resources),
			preview.ResourcesOmitted,
			err,
		)
	}
	if preview.Resources[0].After != "[OMITTED FROM PREVIEW]" ||
		strings.Contains(fmt.Sprint(preview), "private-review-content") {
		t.Fatal("preview exposed an oversized value or hid its omission")
	}
	webPath := fmt.Sprintf("/deployments/%d/artifact-gates/0", deployment.ID)
	web := f.web(t, "GET", webPath+"/review", nil, 200)
	if !strings.Contains(web, "data-terraform-preview-truncated") ||
		!strings.Contains(web, "[OMITTED FROM PREVIEW]") ||
		strings.Contains(web, "private-review-content") ||
		len(web) > 1<<20 {
		t.Fatal("web preview lost omission notice or exceeded its budget")
	}
	if string(f.api(t, "GET", path+"/0/artifact", nil, 200)) != "exact-plan" {
		t.Fatal("preview limits altered the artifact")
	}
	f.api(
		t,
		"POST",
		path+"/0/approve",
		map[string]any{
			"revision": gates[0].Revision,
			"sha256":   gates[0].SHA256,
		},
		200,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deployment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "applied-complete-context") ||
		strings.Contains(logs, "private-review-content") {
		t.Fatal("continuation lost complete context or exposed review data")
	}
}
