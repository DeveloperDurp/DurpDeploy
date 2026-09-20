package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
)

func TestRefreshReleasePreservesAgentPlacement(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	project := seedProject(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	if _, err := h.repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "remote", ScriptBody: "echo remote",
		},
		"agent",
		[]string{"linux"},
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf(
			"/api/v1/projects/%d/releases/%d/refresh",
			project.ID,
			release.ID,
		),
		nil,
	)
	req = withAPIUser(req, user)
	req = withAPIURLParam(req, "id", fmt.Sprint(project.ID))
	req = withAPIURLParam(req, "relId", fmt.Sprint(release.ID))
	rec := httptest.NewRecorder()

	api.NewReleaseHandler(h.repo).RefreshRelease(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		StepsJSON string `json:"steps_json"`
	}
	mustDecode(t, rec.Body, &response)
	var steps []struct {
		ExecutionTarget string   `json:"execution_target"`
		AgentSelectors  []string `json:"agent_selectors"`
	}
	if err := json.Unmarshal([]byte(response.StepsJSON), &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].ExecutionTarget != "agent" ||
		len(steps[0].AgentSelectors) != 1 ||
		steps[0].AgentSelectors[0] != "linux" {
		t.Fatalf("refreshed placement = %+v", steps)
	}
}
