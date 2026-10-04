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
	"durpdeploy/views/pages"
)

const gateBytes = "exact-approved-plan-secret"

func newGateDeployment(
	t *testing.T,
	notifiers ...events.Notifier,
) (*artifactE2E, db.Deployment, pages.ArtifactGateInfo) {
	t.Helper()
	return createGateDeployment(t, newArtifactE2E(t), notifiers...)
}

func createGateDeployment(
	t *testing.T,
	f *artifactE2E,
	notifiers ...events.Notifier,
) (*artifactE2E, db.Deployment, pages.ArtifactGateInfo) {
	t.Helper()
	if len(notifiers) != 0 {
		bus := events.NewBus(f.h.repo)
		bus.Register(artifactNotifier{f.done})
		for _, notifier := range notifiers {
			bus.Register(notifier)
		}
		f.h.runner.SetEventBus(bus)
	}
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":                   "Generate",
		"container_image":        "docker.io/library/bash:5.2",
		"network_mode":           "bridge",
		"approval_artifact_path": "tfplan",
		"approval_review_path":   "review.json",
		"approval_review_format": "terraform",
		"script_body": `set -eu
printf 'exact-approved-plan-secret' > "$DURPDEPLOY_STAGE_DIR/tfplan"
printf '%s' '{"format_version":"1.2","resource_changes":[{"change":{"actions":["create"],"after":{"password":"generated-sensitive-value"}}}],"outputs":{"secret":"generated-sensitive-value"}}' > "$DURPDEPLOY_STAGE_DIR/review.json"
echo generated-sensitive-value
test -e /sys/class/net/eth0`,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":            "Apply",
		"container_image": "docker.io/library/bash:5.2",
		"network_mode":    "bridge",
		"script_body": `set -eu
test "$(cat "$DURPDEPLOY_APPROVED_DIR/tfplan")" = exact-approved-plan-secret
if printf 'changed' > "$DURPDEPLOY_APPROVED_DIR/tfplan"; then exit 8; fi
printf 'applied' > "$DURPDEPLOY_STAGE_DIR/result"`,
	}, 201)
	release := verificationRelease(t, f, "gate-plan")
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	var gates []pages.ArtifactGateInfo
	if err := json.Unmarshal(
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	); err != nil ||
		len(gates) != 1 {
		t.Fatalf("gates=%v err=%v", gates, err)
	}
	if gates[0].Review.Create != 1 || gates[0].Status != "awaiting" ||
		gates[0].ReviewSource != "step_output" || gates[0].ReviewVerified {
		t.Fatalf("gate=%+v", gates[0])
	}
	return f, deployment, gates[0]
}

func gateAPIPath(
	id int64,
) string {
	return fmt.Sprintf("/api/v1/deployments/%d/artifact-gates", id)
}

func TestArtifactGateApproveExactBytesE2E(t *testing.T) {
	// Given: a generated artifact paused before apply, visible through API/web.
	f, deployment, gate := newGateDeployment(t)
	path := gateAPIPath(deployment.ID)
	// A status filter must not silently become an unfiltered web list.
	filtered := f.web(t, "GET", "/deployments?status=rejected", nil, 200)
	if strings.Contains(filtered,
		fmt.Sprintf(`href="/deployments/%d"`, deployment.ID)) {
		t.Fatal("rejected filter returned an awaiting deployment")
	}
	var listed struct {
		Items []db.Deployment `json:"items"`
	}
	if err := json.Unmarshal(f.api(t, "GET",
		"/api/v1/deployments?status=awaiting_artifact_approval", nil, 200),
		&listed); err != nil || len(listed.Items) != 1 ||
		listed.Items[0].ID != deployment.ID {
		t.Fatalf("filtered gates=%v err=%v", listed, err)
	}
	// Parent deletion must preserve the active review and encrypted context.
	for _, parent := range []string{
		f.base(),
		fmt.Sprintf("/api/v1/environments/%d", deployment.EnvironmentID),
		fmt.Sprintf("%s/releases/%d", f.base(), deployment.ReleaseID),
	} {
		f.api(t, "DELETE", parent, nil, 409)
	}
	// A waiting review keeps the slot while later work joins the queue.
	var queued db.Deployment
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]int64{
			"release_id":     deployment.ReleaseID,
			"environment_id": deployment.EnvironmentID,
		},
		201,
	), &queued); err != nil || queued.Status != "queued" {
		t.Fatalf("queued deployment=%+v err=%v", queued, err)
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", queued.ID),
		nil,
		200,
	)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d/artifact-gates", deployment.ID),
		nil,
		200,
	)
	// The separate review claims a create for opaque, non-Terraform bytes.
	// Even a valid checksum must not present that summary as verified.
	if !strings.Contains(page, "Unverified summary") ||
		!strings.Contains(page, "independently inspect the exact artifact") {
		t.Fatal("producer-controlled review is not identified as unverified")
	}
	var raw []map[string]any
	if err := json.Unmarshal(
		f.api(t, "GET", path, nil, 200),
		&raw,
	); err != nil ||
		len(raw) != 1 ||
		raw[0]["review_verified"] != false ||
		raw[0]["review_source"] != "step_output" {
		t.Fatalf("review trust metadata=%v err=%v", raw, err)
	}
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deployment.ID),
			nil,
			200,
		),
	)
	if strings.Contains(page+logs, "generated-sensitive-value") ||
		strings.Contains(page, gateBytes) {
		t.Fatal("sensitive content leaked")
	}
	if data := f.api(
		t,
		"GET",
		path+"/0/artifact",
		nil,
		200,
	); string(
		data,
	) != gateBytes {
		t.Fatalf("artifact=%q", data)
	}
	// When: the API approves this exact checksum/revision.
	f.api(
		t,
		"POST",
		path+"/0/approve",
		map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
		200,
	)
	// Then: apply succeeds against the immutable approved bytes; replay fails.
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	f.api(
		t,
		"POST",
		path+"/0/approve",
		map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
		409,
	)
}

func TestArtifactGateRequiresReviewFormatE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Generate", "script_body": "true",
		"container_image":        "docker.io/library/bash:5.2",
		"approval_artifact_path": "plan", "approval_review_path": "review",
	}, 400)
	f.web(t, "POST", fmt.Sprintf("/projects/%d/steps", f.project.ID),
		url.Values{
			"name":            {"Generate"},
			"script_body":     {"true"},
			"container_image": {"docker.io/library/bash:5.2"},
			"approval_artifact_path": {
				"plan",
			},
			"approval_review_path": {"review"},
		}, 422)
}

func TestArtifactGateWebRejectE2E(t *testing.T) {
	// Given: a pending artifact gate.
	f, deployment, gate := newGateDeployment(t)
	// When: the web form rejects it.
	f.web(
		t,
		"POST",
		fmt.Sprintf("/deployments/%d/artifact-gates/0/reject", deployment.ID),
		url.Values{
			"sha256": {gate.SHA256}, "revision": {"1"},
		},
		303,
	)
	// Then: it is terminal and cannot approve/apply.
	var state db.Deployment
	if err := json.Unmarshal(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
			nil,
			200,
		),
		&state,
	); err != nil ||
		state.Status != "rejected" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
		409,
	)
}
