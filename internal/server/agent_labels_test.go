package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminEnvironmentAgentRoutesRemoved(t *testing.T) {
	// Given
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "labels-only-admin")
	environmentID := seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			request := browserFormRequest(
				method,
				"/admin/environments/1/agent",
				"labels-only-admin",
				url.Values{"agent_id": {"agent-a"}, "csrf_token": {"csrf"}},
			)
			response := httptest.NewRecorder()

			// When
			router.ServeHTTP(response, request)

			// Then
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
		t.Run("API "+method, func(t *testing.T) {
			request := httptest.NewRequest(
				method,
				"/api/v1/admin/environments/1/agent",
				strings.NewReader(`{"agent_id":"agent-a"}`),
			)
			request.Header.Set(
				"Authorization",
				"Bearer ddp_pat_labels-only-admin",
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			// When
			router.ServeHTTP(response, request)

			// Then
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}

	if environmentID == 0 {
		t.Fatal("environment fixture was not created")
	}
	list := httptest.NewRequest(http.MethodGet, "/environments", nil)
	list.AddCookie(&http.Cookie{Name: "session", Value: "labels-only-admin"})
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK ||
		strings.Contains(listResponse.Body.String(), `name="agent_id"`) ||
		strings.Contains(listResponse.Body.String(), ">Agent</th>") {
		t.Fatalf(
			"environment list still exposes agent assignment: %s",
			listResponse.Body,
		)
	}
}

func TestAdminAgentLabelsCanBeAddedAndRemoved(t *testing.T) {
	// Given
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "label-admin")
	seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)

	add := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/labels",
		"label-admin",
		url.Values{"label": {"linux"}, "csrf_token": {"csrf"}},
	)
	addResponse := httptest.NewRecorder()

	// When
	router.ServeHTTP(addResponse, add)

	// Then
	if addResponse.Code != http.StatusSeeOther {
		t.Fatalf("add status=%d body=%s", addResponse.Code, addResponse.Body)
	}
	labels, err := h.repo.Queries.ListAgentLabels(t.Context(), "agent-a")
	if err != nil || len(labels) != 1 || labels[0] != "linux" {
		t.Fatalf("labels=%v err=%v", labels, err)
	}
	assertAuditActionCount(t, h, "add_agent_label", 1)

	detail := httptest.NewRequest(http.MethodGet, "/admin/agents/agent-a", nil)
	detail.AddCookie(&http.Cookie{Name: "session", Value: "label-admin"})
	detailResponse := httptest.NewRecorder()
	router.ServeHTTP(detailResponse, detail)
	if detailResponse.Code != http.StatusOK ||
		!strings.Contains(detailResponse.Body.String(), ">linux<") ||
		strings.Contains(detailResponse.Body.String(), "Environment assignments") {
		t.Fatalf("detail status=%d body=%s", detailResponse.Code, detailResponse.Body)
	}

	remove := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/labels/delete",
		"label-admin",
		url.Values{"label": {"linux"}, "csrf_token": {"csrf"}},
	)
	removeResponse := httptest.NewRecorder()

	// When
	router.ServeHTTP(removeResponse, remove)

	// Then
	if removeResponse.Code != http.StatusSeeOther {
		t.Fatalf("delete status=%d body=%s", removeResponse.Code, removeResponse.Body)
	}
	labels, err = h.repo.Queries.ListAgentLabels(t.Context(), "agent-a")
	if err != nil || len(labels) != 0 {
		t.Fatalf("labels=%v err=%v", labels, err)
	}
	assertAuditActionCount(t, h, "delete_agent_label", 1)
}

func TestAdminAgentLabelsAPI(t *testing.T) {
	// Given
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "label-api-admin")
	seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/agents/agent-a/labels",
		strings.NewReader(`{"label":"arm64"}`),
	)
	request.Header.Set("Authorization", "Bearer ddp_pat_label-api-admin")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	detail := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/agents/agent-a",
		nil,
	)
	detail.Header.Set("Authorization", "Bearer ddp_pat_label-api-admin")
	detailResponse := httptest.NewRecorder()
	router.ServeHTTP(detailResponse, detail)
	var body struct {
		Labels []string `json:"labels"`
	}
	if err := json.NewDecoder(detailResponse.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if detailResponse.Code != http.StatusOK ||
		len(body.Labels) != 1 || body.Labels[0] != "arm64" {
		t.Fatalf("status=%d labels=%v", detailResponse.Code, body.Labels)
	}

	remove := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/admin/agents/agent-a/labels",
		strings.NewReader(`{"label":"arm64"}`),
	)
	remove.Header.Set("Authorization", "Bearer ddp_pat_label-api-admin")
	remove.Header.Set("Content-Type", "application/json")
	removeResponse := httptest.NewRecorder()
	router.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", removeResponse.Code, removeResponse.Body)
	}
}
