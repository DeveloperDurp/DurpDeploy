package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestAdminAgentEnvironmentLabelsDoNotRouteDeployments(t *testing.T) {
	// Given: an active agent and an environment with no routing assignment.
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "environment-label-admin")
	environmentID := seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	request := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/environments",
		"environment-label-admin",
		url.Values{
			"environment_id": {"1"},
			"csrf_token":     {"csrf"},
		},
	)
	response := httptest.NewRecorder()

	// When: the environment is added as agent metadata.
	router.ServeHTTP(response, request)

	// Then: the label is visible but deployment routing remains local.
	if response.Code != http.StatusSeeOther {
		t.Fatalf("add status=%d body=%s", response.Code, response.Body)
	}
	project, err := h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "environment-label-routing"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environmentID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != repository.ExecutionLocal ||
		result.Deployment.AssignedAgentID.Valid {
		t.Fatalf("environment label changed routing: %+v", result)
	}
	detail := httptest.NewRequest(
		http.MethodGet,
		"/admin/agents/agent-a",
		nil,
	)
	detail.AddCookie(&http.Cookie{
		Name: "session", Value: "environment-label-admin",
	})
	detailResponse := httptest.NewRecorder()
	router.ServeHTTP(detailResponse, detail)
	detailBody := detailResponse.Body.String()
	if detailResponse.Code != http.StatusOK ||
		!strings.Contains(detailBody, "Environment labels") ||
		!strings.Contains(detailBody, "agent environment") ||
		!strings.Contains(detailBody, "do not route deployments") ||
		strings.Contains(detailBody, ">agent environment</option>") ||
		!strings.Contains(detailBody, "All environments are already selected") {
		t.Fatalf(
			"detail status=%d body=%s",
			detailResponse.Code,
			detailResponse.Body,
		)
	}
	if _, err := h.repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "other environment"},
	); err != nil {
		t.Fatal(err)
	}
	partialDetail := httptest.NewRequest(
		http.MethodGet,
		"/admin/agents/agent-a",
		nil,
	)
	partialDetail.AddCookie(&http.Cookie{
		Name: "session", Value: "environment-label-admin",
	})
	partialResponse := httptest.NewRecorder()
	router.ServeHTTP(partialResponse, partialDetail)
	partialBody := partialResponse.Body.String()
	if partialResponse.Code != http.StatusOK ||
		strings.Contains(partialBody, ">agent environment</option>") ||
		!strings.Contains(partialBody, ">other environment</option>") {
		t.Fatalf(
			"partial detail status=%d body=%s",
			partialResponse.Code,
			partialResponse.Body,
		)
	}
	assertAuditActionCount(t, h, "add_agent_environment_label", 1)

	remove := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/environments/delete",
		"environment-label-admin",
		url.Values{
			"environment_id": {"1"},
			"csrf_token":     {"csrf"},
		},
	)
	removeResponse := httptest.NewRecorder()
	router.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusSeeOther {
		t.Fatalf(
			"remove status=%d body=%s",
			removeResponse.Code,
			removeResponse.Body,
		)
	}
	environmentLabels, err := h.repo.Queries.ListAgentEnvironmentLabels(
		t.Context(),
		"agent-a",
	)
	if err != nil || len(environmentLabels) != 0 {
		t.Fatalf("environment labels=%v error=%v", environmentLabels, err)
	}
	assertAuditActionCount(t, h, "delete_agent_environment_label", 1)
}
