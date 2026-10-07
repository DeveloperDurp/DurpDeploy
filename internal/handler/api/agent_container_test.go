package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSteps_AgentContainerContract(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "container@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps", project.ID)
	body := `{"name":"container","script_body":"echo hi","interpreter":"bash",
        "execution_target":"agent","agent_execution_mode":"container",
        "container_image":"alpine:3.20","variable_names":["DEPLOY_ENV"]}`
	rec := h.request(t, "POST", path, token, body)
	h.assertStatus(t, rec, 201)
	assertAgentContainer(t, rec.Body.Bytes())
	rec = h.request(t, "GET", path+"/1", token, "")
	h.assertStatus(t, rec, 200)
	assertAgentContainer(t, rec.Body.Bytes())
	for _, invalid := range []string{
		strings.Replace(body, "alpine:3.20", "", 1),
		strings.Replace(body, "alpine:3.20", "-privileged", 1),
		strings.Replace(body, `"agent_execution_mode":"container"`, `"agent_execution_mode":"host"`, 1),
		strings.Replace(body, `"execution_target":"agent"`, `"execution_target":"local"`, 1),
		strings.Replace(body, `"agent_execution_mode":"container"`, `"agent_execution_mode":"unknown"`, 1),
		strings.Replace(body, "DEPLOY_ENV", "DOCKER_HOST", 1),
	} {
		rec = h.request(t, "PUT", path+"/1", token, invalid)
		h.assertStatus(t, rec, 400)
	}
	rec = h.request(t, "GET", path+"/1", token, "")
	assertAgentContainer(t, rec.Body.Bytes())
}

func assertAgentContainer(t *testing.T, raw []byte) {
	t.Helper()
	var step struct {
		ExecutionTarget    string   `json:"execution_target"`
		AgentExecutionMode string   `json:"agent_execution_mode"`
		ContainerImage     string   `json:"container_image"`
		VariableNames      []string `json:"variable_names"`
	}
	if err := json.Unmarshal(raw, &step); err != nil {
		t.Fatal(err)
	}
	if step.ExecutionTarget != "agent" ||
		step.AgentExecutionMode != "container" ||
		step.ContainerImage != "alpine:3.20" ||
		strings.Join(step.VariableNames, ",") != "DEPLOY_ENV" {
		t.Fatalf("step=%+v", step)
	}
}

func TestTemplates_AgentContainerVersionsAreImmutable(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	body := `{"name":"container","script_body":"echo hi","interpreter":"bash",
        "execution_target":"agent","agent_execution_mode":"container",
        "container_image":"alpine:3.20","variable_names":["DEPLOY_ENV"]}`
	rec := h.request(t, "POST", "/api/v1/templates", token, body)
	h.assertStatus(t, rec, 201)
	assertAgentContainer(t, rec.Body.Bytes())
	rec = h.request(
		t,
		"PUT",
		"/api/v1/templates/1",
		token,
		`{"name":"container","script_body":"echo host","execution_target":"agent"}`,
	)
	h.assertStatus(t, rec, 200)
	rec = h.request(t, "GET", "/api/v1/templates/1/history", token, "")
	h.assertStatus(t, rec, 200)
	var versions []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("history=%s", rec.Body.String())
	}
	assertAgentContainer(t, versions[1])
}

func TestRunbookAPI_AgentContainerSnapshot(t *testing.T) {
	request, projectID := newRunbookContainerRouter(t)
	base := fmt.Sprintf("/api/v1/projects/%d/runbooks", projectID)
	created := request("POST", base, `{"name":"container","steps":[
        {"name":"container","script_body":"echo hi","execution_target":"agent",
        "agent_execution_mode":"container","container_image":"alpine:3.20",
        "variable_names":["DEPLOY_ENV"]}]}`)
	if created.Code != 201 {
		t.Fatalf("status=%d body=%s", created.Code, created.Body.String())
	}
	var saved struct {
		Runbook struct{ ID int64 } `json:"runbook"`
		Version struct{ ID int64 } `json:"version"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	detail := request(
		"GET",
		fmt.Sprintf(
			"%s/%d/versions/%d",
			base,
			saved.Runbook.ID,
			saved.Version.ID,
		),
		"",
	)
	var version struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if detail.Code != 200 {
		t.Fatalf("detail=%s", detail.Body.String())
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if len(version.Steps) != 1 {
		t.Fatalf("steps=%s", detail.Body.String())
	}
	assertAgentContainer(t, version.Steps[0])
}
