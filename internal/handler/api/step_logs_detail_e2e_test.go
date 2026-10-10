//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentStepLogsAPIWebE2E(t *testing.T) {
	// Given: a release snapshot of two steps with distinguishable scripts.
	f := newArtifactE2E(t)
	scripts := []string{"printf 'Step succeeded.\\nfirst-only\\n'", "sleep 1"}
	for _, script := range scripts {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": "Duplicate name", "script_body": script,
			"container_image": "docker.io/library/bash:5.2",
		}, http.StatusCreated)
	}
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "step-logs"}, 201), &release)
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	), &deployment)
	path := fmt.Sprintf(
		"/api/v1/deployments/%d/logs/stream?format=structured",
		deployment.ID,
	)
	logs := f.readStepLogs(t, path, "")
	states := map[int64][]string{}
	spoof := false
	var previous int64
	for _, log := range logs {
		if log.ID <= previous || log.StepIndex == nil {
			t.Fatalf("invalid ordered step event: %+v", log)
		}
		previous = log.ID
		if log.State != "" {
			states[*log.StepIndex] = append(states[*log.StepIndex], log.State)
		}
		if log.Line == "Step succeeded." && log.State == "" {
			spoof = true
		}
	}
	for index := int64(0); index < 2; index++ {
		if strings.Join(states[index], ",") != "running,succeeded" {
			t.Fatalf("step %d states=%v", index, states[index])
		}
	}
	if !spoof {
		t.Fatal("script output was confused with trusted lifecycle metadata")
	}
	resumed := f.readStepLogs(t, path+"&after=0", fmt.Sprint(logs[1].ID))
	if len(resumed) != len(logs)-2 || resumed[0].ID != logs[2].ID {
		t.Fatal("resumed stream duplicated or omitted events")
	}
	for _, failure := range []struct {
		path   string
		status int
	}{
		{path + "&after=-1", 400},
		{"/api/v1/deployments/9223372036854775807/logs/stream?format=structured", 404},
	} {
		var envelope map[string]string
		decodeStepLogTest(t, f.api(t, "GET", failure.path,
			nil, failure.status), &envelope)
		if envelope["error"] == "" {
			t.Fatal("stream failure omitted its JSON error envelope")
		}
	}
	if missing := f.web(t, "GET", "/deployments/9223372036854775807",
		nil, 404); missing != "404 page not found\n" {
		t.Fatalf("web missing-deployment response changed: %q", missing)
	}
	nonMember := seedAPIUser(
		t,
		f.h.repo,
		"logs-non-member@example.com",
		"deployer",
	)
	_, token := seedAPIToken(t, f.h.repo, nonMember.ID)
	adminToken := f.token
	f.token = token
	var denied map[string]string
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 403), &denied)
	f.token = adminToken
	if denied["error"] == "" {
		t.Fatal("non-member denial omitted its JSON error envelope")
	}
	// When: reading the deployment page and its release through HTTP.
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deployment.ID),
		nil,
		200,
	)
	for _, marker := range []string{`data-step-index="0"`, `data-step-index="1"`, "Step logs", "Succeeded", "first-only"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("web log panel omitted %s", marker)
		}
	}
	// Then: logs remain on the page and definitions remain in the API snapshot.
	if strings.Contains(page, `aria-label="Step definitions"`) {
		t.Fatal("deployment page still renders the step definitions")
	}
	var snapshot struct {
		StepsJSON string `json:"steps_json"`
	}
	decodeStepLogTest(t, f.api(t, "GET", fmt.Sprintf(
		"%s/releases/%d", f.base(), release.ID), nil, 200), &snapshot)
	var steps []struct {
		Name       string `json:"name"`
		ScriptBody string `json:"script_body"`
	}
	if err := json.Unmarshal([]byte(snapshot.StepsJSON), &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(scripts) {
		t.Fatal("release API lost its step definitions")
	}
	for index, step := range steps {
		if step.Name != "Duplicate name" || step.ScriptBody != scripts[index] {
			t.Fatalf("release API changed step %d: %+v", index, step)
		}
	}
}
