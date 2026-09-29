package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

// Issue #95: runbook API submissions validate the mandatory
// server-container image and variable allowlist at the boundary, and
// the container fields survive the immutable version snapshot.

func newRunbookContainerRouter(t *testing.T) (
	func(method, path, body string) *httptest.ResponseRecorder,
	int64,
) {
	t.Helper()
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "runbook-container@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	return request, project.ID
}

func TestRunbookAPI_LocalStepRequiresContainerImage(t *testing.T) {
	request, projectID := newRunbookContainerRouter(t)
	base := fmt.Sprintf("/api/v1/projects/%d", projectID)
	created := request(http.MethodPost, base+"/runbooks",
		`{"name":"maintenance","steps":[{"name":"check",
		"script_body":"echo hi"}]}`)
	if created.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(),
		"container image is required for local steps") {
		t.Fatalf("body missing required-image message: %s",
			created.Body.String())
	}
}

func TestRunbookAPI_AgentStepRejectsContainerImage(t *testing.T) {
	request, projectID := newRunbookContainerRouter(t)
	base := fmt.Sprintf("/api/v1/projects/%d", projectID)
	created := request(http.MethodPost, base+"/runbooks",
		`{"name":"maintenance","steps":[{"name":"check",
		"script_body":"hostname","execution_target":"agent",
		"container_image":"alpine:3.20"}]}`)
	if created.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(),
		"container image is only valid for local steps") {
		t.Fatalf("body missing image-rejection message: %s",
			created.Body.String())
	}
}

func TestRunbookAPI_ContainerFieldsSurviveVersions(t *testing.T) {
	request, projectID := newRunbookContainerRouter(t)
	base := fmt.Sprintf("/api/v1/projects/%d", projectID)
	created := request(http.MethodPost, base+"/runbooks",
		`{"name":"maintenance","steps":[{"name":"check",
		"script_body":"echo hi","container_image":"alpine:3.20",
		"variable_names":["DEPLOY_ENV","API_KEY"]}]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", created.Code, created.Body.String())
	}
	var saved struct {
		Runbook struct {
			ID int64 `json:"id"`
		} `json:"runbook"`
		Version struct {
			ID int64 `json:"id"`
		} `json:"version"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	detail := request(http.MethodGet,
		fmt.Sprintf("%s/runbooks/%d/versions/%d", base,
			saved.Runbook.ID, saved.Version.ID), "")
	if detail.Code != http.StatusOK {
		t.Fatalf("version status=%d body=%s", detail.Code,
			detail.Body.String())
	}
	var versioned struct {
		Steps []struct {
			ExecutionTarget string   `json:"execution_target"`
			ContainerImage  string   `json:"container_image"`
			VariableNames   []string `json:"variable_names"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &versioned); err != nil {
		t.Fatal(err)
	}
	if len(versioned.Steps) != 1 ||
		versioned.Steps[0].ExecutionTarget != "local" ||
		versioned.Steps[0].ContainerImage != "alpine:3.20" ||
		strings.Join(versioned.Steps[0].VariableNames, ",") !=
			"DEPLOY_ENV,API_KEY" {
		t.Fatalf("version steps=%+v", versioned.Steps)
	}
	// Every new version re-validates: an image-less local step cannot
	// enter the version history.
	updated := request(http.MethodPut,
		fmt.Sprintf("%s/runbooks/%d", base, saved.Runbook.ID),
		`{"steps":[{"name":"check","script_body":"echo hi"}]}`)
	if updated.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update status=%d body=%s", updated.Code,
			updated.Body.String())
	}
	if !strings.Contains(updated.Body.String(),
		"container image is required for local steps") {
		t.Fatalf("update body missing required-image message: %s",
			updated.Body.String())
	}
	updated = request(http.MethodPut,
		fmt.Sprintf("%s/runbooks/%d", base, saved.Runbook.ID),
		`{"steps":[{"name":"check","script_body":"echo hi two",
		"container_image":"alpine:3.20",
		"variable_names":["DEPLOY_ENV"]}]}`)
	if updated.Code != http.StatusCreated {
		t.Fatalf("valid update status=%d body=%s", updated.Code,
			updated.Body.String())
	}
}

func TestRunbookAPI_RejectsInvalidVariableNames(t *testing.T) {
	cases := []struct {
		name    string
		names   string
		message string
	}{
		{name: "non-identifier", names: `["9BAD"]`,
			message: "variable names must be identifiers"},
		{name: "duplicates", names: `["ALPHA","ALPHA"]`,
			message: "variable names contain duplicates"},
		{name: "reserved", names: `["SSH_AUTH_SOCK"]`,
			message: "variable name is reserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, projectID := newRunbookContainerRouter(t)
			base := fmt.Sprintf("/api/v1/projects/%d", projectID)
			created := request(http.MethodPost, base+"/runbooks",
				fmt.Sprintf(`{"name":"maintenance","steps":[{"name":"check",
				"script_body":"echo hi","container_image":"alpine:3.20",
				"variable_names":%s}]}`, tc.names))
			if created.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", created.Code,
					created.Body.String())
			}
			if !strings.Contains(created.Body.String(), tc.message) {
				t.Fatalf("body missing %q: %s", tc.message,
					created.Body.String())
			}
		})
	}
}
