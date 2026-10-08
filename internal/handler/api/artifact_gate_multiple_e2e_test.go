//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/events"
	"durpdeploy/views/pages"
)

func TestArtifactGateMultipleReviewsE2E(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprint("expire=", expire), func(t *testing.T) {
			testArtifactGateMultipleReviews(t, expire)
		})
	}
}

func testArtifactGateMultipleReviews(t *testing.T, expire bool) {
	// Given: two generation gates before one final apply.
	f := newArtifactE2E(t)
	for index := range 2 {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": fmt.Sprintf(
				"Generate %d",
				index,
			),
			"container_image":        "docker.io/library/bash:5.2",
			"approval_artifact_path": "plan",
			"approval_review_path":   "review",
			"approval_review_format": "summary",
			"script_body": fmt.Sprintf(
				`printf '%d' > "$DURPDEPLOY_STAGE_DIR/plan"; printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review"`,
				index,
			),
		}, 201)
	}
	f.api(
		t,
		"POST",
		f.base()+"/steps",
		map[string]any{
			"name":            "Apply",
			"container_image": "docker.io/library/bash:5.2",
			"script_body":     `test "$(cat "$DURPDEPLOY_APPROVED_DIR/plan")" = 1`,
		},
		201,
	)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "two-gates"),
	)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	var gates []pages.ArtifactGateInfo
	if err := json.Unmarshal(
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	); err != nil {
		t.Fatal(err)
	}
	first := gates[0]
	assertSummaryGate(t, f, deployment.ID, first.ReviewFormat)
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"revision": 1, "sha256": first.SHA256},
		200,
	)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	// When: a consumed gate expires and a stale rejection is replayed.
	if _, err := f.h.repo.DB.Exec(
		"UPDATE artifact_gates SET expires_at = 0 WHERE deployment_id = ? AND step_index = 0",
		deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	); err != nil ||
		len(gates) != 2 {
		t.Fatalf("gates=%v err=%v", gates, err)
	}
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/reject",
		map[string]any{"revision": 1, "sha256": first.SHA256},
		409,
	)
	// Then: the fresh current gate still approves and applies its own bytes.
	if expire {
		if _, err := f.h.repo.DB.Exec(
			"UPDATE artifact_gates SET expires_at=0 WHERE deployment_id=? AND step_index=1",
			deployment.ID,
		); err != nil {
			t.Fatal(err)
		}
		if err := f.h.repo.MaintainArtifactGates(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(
			f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
			&gates,
		); err != nil {
			t.Fatal(err)
		}
		if len(gates) != 2 || gates[0].Status != "approved" ||
			gates[1].Status != "expired" {
			t.Fatalf("expiry changed consumed approval: %+v", gates)
		}
		return
	}
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/1/approve",
		map[string]any{"revision": 1, "sha256": gates[1].SHA256},
		200,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	var starts int
	if err := f.h.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id=? AND event_type='deployment_started'",
		deployment.ID,
	).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("deployment started events=%d err=%v", starts, err)
	}
}

func assertSummaryGate(t *testing.T, f *artifactE2E, id int64, format string) {
	t.Helper()
	if format != "summary" {
		t.Fatal("summary review format missing from API")
	}
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d/artifact-gates", id),
		nil,
		200,
	)
	if strings.Contains(page, "data-terraform-review") {
		t.Fatal("summary gate exposed Terraform resource disclosure")
	}
}
