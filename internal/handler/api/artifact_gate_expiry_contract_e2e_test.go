//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestArtifactGateExpiryDecisionContractE2E(t *testing.T) {
	f, deployment, gate := newGateDeployment(t)
	path := gateAPIPath(deployment.ID)
	identity := map[string]any{"revision": gate.Revision, "sha256": gate.SHA256}
	for _, action := range []string{"approve", "reject"} {
		f.api(t, "POST", path+"/99/"+action, identity, 409)
		f.web(
			t,
			"POST",
			fmt.Sprintf(
				"/deployments/%d/artifact-gates/99/%s",
				deployment.ID,
				action,
			),
			url.Values{"revision": {"1"}, "sha256": {gate.SHA256}},
			303,
		)
	}
	if _, err := f.h.repo.DB.Exec(
		"UPDATE artifact_gates SET expires_at=0 WHERE deployment_id=?",
		deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	// No maintenance call precedes either decision.
	for _, action := range []string{"reject", "approve"} {
		f.api(t, "POST", path+"/0/"+action, identity, 409)
	}
	f.api(t, "GET", path+"/0/artifact", nil, 409)
	f.api(t, "GET", path+"/0/review", nil, 409)
	panel := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d/artifact-gates", deployment.ID),
		nil,
		200,
	)
	if strings.Contains(panel, "/artifact\"") ||
		strings.Contains(panel, "/approve\"") ||
		strings.Contains(panel, "/reject\"") {
		t.Fatal("expired gate exposes unavailable controls")
	}
}

func TestArtifactGateQuickEditPreservesConfigurationE2E(t *testing.T) {
	f, _, _ := newGateDeployment(t)
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 2 {
		t.Fatalf("steps=%+v err=%v", steps, err)
	}
	step := steps[0]
	webPath := fmt.Sprintf("/projects/%d/steps/%d", f.project.ID, step.ID)
	form := f.web(t, "GET", webPath+"/edit", nil, 200)
	for _, field := range []string{"network_mode", "approval_artifact_path", "approval_review_path", "approval_review_format"} {
		if !strings.Contains(form, `name="`+field+`"`) {
			t.Fatalf("quick edit omits %s", field)
		}
	}
	f.web(t, "PUT", webPath, url.Values{
		"name": {
			step.Name,
		},
		"script_body":      {step.ScriptBody},
		"interpreter":      {step.Interpreter},
		"execution_target": {"local"},
		"container_image":  {step.ContainerImage},
		"network_mode": {
			step.NetworkMode,
		},
		"approval_artifact_path": {step.ApprovalArtifactPath},
		"approval_review_path": {
			step.ApprovalReviewPath,
		},
		"approval_review_format": {step.ApprovalReviewFormat},
		"max_retries":            {"0"},
		"sort_order":             {"0"},
	}, 200)
	var saved struct {
		NetworkMode          string `json:"network_mode"`
		ApprovalArtifactPath string `json:"approval_artifact_path"`
		ApprovalReviewPath   string `json:"approval_review_path"`
		ApprovalReviewFormat string `json:"approval_review_format"`
	}
	if err := json.Unmarshal(
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/steps/%d", f.base(), step.ID),
			nil,
			200,
		),
		&saved,
	); err != nil || saved.NetworkMode != step.NetworkMode || saved.ApprovalArtifactPath != step.ApprovalArtifactPath || saved.ApprovalReviewPath != step.ApprovalReviewPath ||
		saved.ApprovalReviewFormat != step.ApprovalReviewFormat {
		t.Fatalf("quick edit changed gate=%+v err=%v", saved, err)
	}
}
