package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/handler/api"
	"github.com/go-chi/chi/v5"
)

func TestStructuredStreamStartupErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		brokenDB bool
		status   int
	}{
		{"missing deployment", false, http.StatusNotFound},
		{"database failure", true, http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given: a missing deployment or an unavailable database.
			h := newAPIHarness(t)
			if test.brokenDB {
				if err := h.repo.DB.Close(); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodGet,
				"/api/v1/deployments/1/logs/stream?format=structured", nil)
			req = withAPIURLParam(req, "id", "1")
			rec := httptest.NewRecorder()

			// When: the structured stream cannot start.
			api.NewLogHandler(h.broker, h.repo).StreamLogs(rec, req)

			// Then: the API returns its JSON error contract before SSE starts.
			if rec.Code != test.status {
				t.Fatalf("status=%d, want=%d", rec.Code, test.status)
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"),
				"application/json") {
				t.Fatalf("content type=%s", rec.Header().Get("Content-Type"))
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["error"] == "" {
				t.Fatal("missing JSON error")
			}
		})
	}
}

func TestStructuredStreamNonMemberWithHTMXHeader(t *testing.T) {
	// Given: a non-member calling the API with an HTMX header.
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "non-member@example.com", "deployer")
	p := seedProject(t, h.repo)
	e := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, p.ID)
	d := seedDeployment(t, h.repo, release.ID, e.ID, "succeeded")
	router := chi.NewRouter()
	router.With(auth.RequireDeploymentProjectAccess(h.repo)).Get(
		"/api/v1/deployments/{id}/logs/stream",
		api.NewLogHandler(h.broker, h.repo).StreamLogs)
	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/v1/deployments/%d/logs/stream?format=structured",
			d.ID,
		),
		nil,
	)
	req = auth.SetUser(req, u)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	// When: authorization denies the stream.
	router.ServeHTTP(rec, req)

	// Then: API errors remain JSON 403 rather than a web toast response.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" || rec.Header().Get("HX-Trigger") != "" {
		t.Fatal("API denial lost JSON error contract")
	}
}
