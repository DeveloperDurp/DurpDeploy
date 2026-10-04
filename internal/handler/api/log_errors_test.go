package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/handler/api"
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
