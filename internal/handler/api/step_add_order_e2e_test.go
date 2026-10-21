//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestStepAddOrderAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	path := fmt.Sprintf("/projects/%d/steps", f.project.ID)
	for index, order := range []string{"0", "0", "7", "0", "", "-1"} {
		f.web(t, "POST", path, url.Values{
			"name":        {fmt.Sprintf("web-step-%d", index)},
			"script_body": {"echo ordered"}, "sort_order": {order},
			"container_image": {"harbor.durp.info/dockerhub/library/bash:5.2"},
		}, 200)
	}
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "api-appended", "sort_order": 0,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var result struct {
		Items []struct {
			SortOrder int64 `json:"sort_order"`
		}
	}
	decodeStepLogTest(t, f.api(t, "GET", f.base()+"/steps", nil, 200), &result)
	var orders []int64
	for _, step := range result.Items {
		orders = append(orders, step.SortOrder)
	}
	if !reflect.DeepEqual(orders, []int64{0, 1, 7, 8, 9, 10, 11}) {
		t.Fatalf("web/API creation order=%v", orders)
	}
}

func TestStepAddPreservesSavedFailureAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	for index, script := range []string{"exit 4", "echo must-not-run"} {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": fmt.Sprintf("saved-step-%d", index), "script_body": script,
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 201)
	}
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "saved-before-add"}, 201), &release)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/steps", f.project.ID),
		url.Values{
			"name": {"future-step"}, "script_body": {"echo future-only"},
			"sort_order":      {"0"},
			"container_image": {"harbor.durp.info/dockerhub/library/bash:5.2"},
		},
		200,
	)
	var retained db.Release
	decodeStepLogTest(t, f.api(t, "GET", fmt.Sprintf("%s/releases/%d",
		f.base(), release.ID), nil, 200), &retained)
	if retained.StepsJson != release.StepsJson {
		t.Fatal("adding a project step changed the saved release")
	}
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{"release_id": release.ID,
			"environment_id": f.environment.ID}, 201), &deployment)
	f.completion(t, deployment.ID, events.DeploymentFailed)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deployment.ID),
		nil,
		200,
	)
	doc := dashboardDocument(t, page)
	first := dashboardElements(doc, "data-step-index", "0")
	second := dashboardElements(doc, "data-step-index", "1")
	if len(first) != 1 || len(second) != 1 ||
		!strings.Contains(dashboardText(first[0]), "Failed") ||
		!strings.Contains(dashboardText(second[0]), "State unavailable") ||
		strings.Contains(page, "future-only") {
		t.Fatal(
			"saved failed deployment lost its recorded step-state boundaries",
		)
	}
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
	if strings.Contains(string(logs), "must-not-run") ||
		strings.Contains(string(logs), "future-only") {
		t.Fatal("failed saved deployment ran a later or current-project step")
	}
}
