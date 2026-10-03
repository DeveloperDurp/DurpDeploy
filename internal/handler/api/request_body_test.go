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

func TestAPI_RequestBodyRejectsInvalidInputBeforeMutation(t *testing.T) {
	// Given: an authenticated client and the production router.
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "body@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, u.ID)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	const limit = 4 << 20
	for _, path := range []string{"/api/v1/environments", "/api/v1/tokens"} {
		for _, tc := range []struct {
			name   string
			body   string
			status int
		}{
			{"unknown field", `{"name":"rejected","extra":true}`, 400},
			{"multiple documents", `{"name":"rejected"} {}`, 400},
			{"trailing junk", `{"name":"rejected"} junk`, 400},
			{"null", `null`, 400},
			{"empty", ``, 400},
			{"oversized", `{"name":"` + strings.Repeat("a", limit) + `"}`, 413},
			{"oversized whitespace", `{"name":"rejected"}` + strings.Repeat(" ", limit), 413},
		} {
			for _, unknownLength := range []bool{false, true} {
				t.Run(
					fmt.Sprintf(
						"%s/%s/chunked=%t",
						path,
						tc.name,
						unknownLength,
					),
					func(t *testing.T) {
						var before int
						table := "environments"
						if path == "/api/v1/tokens" {
							table = "api_tokens"
						}
						if err := h.repo.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&before); err != nil {
							t.Fatal(err)
						}
						req := httptest.NewRequest(
							http.MethodPost,
							path,
							strings.NewReader(tc.body),
						)
						req.Header.Set("Authorization", "Bearer "+token)
						if unknownLength {
							req.ContentLength = -1
						}
						rec := httptest.NewRecorder()
						// When
						router.ServeHTTP(rec, req)
						// Then
						if rec.Code != tc.status {
							t.Fatalf(
								"status=%d body=%.200s",
								rec.Code,
								rec.Body.String(),
							)
						}
						var after int
						if err := h.repo.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&after); err != nil {
							t.Fatal(err)
						}
						if after != before {
							t.Fatalf("mutation: count %d -> %d", before, after)
						}
						message := "Invalid JSON body"
						if tc.status == 413 {
							message = "Request body too large"
						}
						want := fmt.Sprintf(`{"error":%q}`, message)
						if strings.TrimSpace(rec.Body.String()) != want {
							t.Fatalf(
								"unstable error: %.200s",
								rec.Body.String(),
							)
						}
						count, err := h.repo.Queries.CountAuditLogs(t.Context())
						if err != nil || count != 0 {
							t.Fatalf(
								"rejected request audited: count=%d err=%v",
								count,
								err,
							)
						}
					},
				)
			}
		}
	}
}

func TestAPI_RequestBodyScriptBoundaryAndRejectedUpdate(t *testing.T) {
	// Given: a script payload whose complete JSON envelope is at the limit.
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "script-body@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, u.ID)
	project := seedProject(t, h.repo)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	path := fmt.Sprintf("/api/v1/projects/%d/steps", project.ID)
	const prefix = `{"name":"large","container_image":"alpine:3.20","script_body":"`
	const suffix = `"}`
	script := strings.Repeat("a", (4<<20)-len(prefix)-len(suffix))
	for _, delta := range []int{1, 0} {
		req := httptest.NewRequest("POST", path,
			strings.NewReader(prefix+script+strings.Repeat("a", delta)+suffix))
		req.ContentLength = -1
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		// When
		router.ServeHTTP(rec, req)
		// Then
		want := 201
		if delta == 1 {
			want = 413
		}
		if rec.Code != want {
			t.Fatalf(
				"delta=%d status=%d body=%.200s",
				delta,
				rec.Code,
				rec.Body.String(),
			)
		}
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 || steps[0].ScriptBody != script {
		t.Fatalf("large script not preserved: steps=%d err=%v", len(steps), err)
	}
	before, err := h.repo.Queries.CountAuditLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"name":"changed"} {}`, 400},
		{prefix + script + "a" + suffix, 413},
	} {
		req := httptest.NewRequest(
			"PUT",
			fmt.Sprintf("%s/%d", path, steps[0].ID),
			strings.NewReader(tc.body),
		)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("invalid update status=%d want=%d", rec.Code, tc.status)
		}
	}
	after, err := h.repo.Queries.CountAuditLogs(t.Context())
	if err != nil || after != before {
		t.Fatalf("rejected update audited: %d -> %d err=%v", before, after, err)
	}
	steps, err = h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 || steps[0].Name != "large" ||
		steps[0].ScriptBody != script {
		t.Fatal("rejected update mutated the stored script")
	}
}

func TestAPI_RequestBodyNestedAndBodylessContracts(t *testing.T) {
	// Given
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "nested-body@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, u.ID)
	project := seedProject(t, h.repo)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", fmt.Sprintf("/api/v1/projects/%d/runbooks", project.ID),
			`{"name":"nested","steps":[{"name":"step","script_body":"echo hi","extra":true}]}`, 400},
		{"DELETE", fmt.Sprintf("/api/v1/projects/%d", project.ID), `{"extra":true}`, 400},
		{"DELETE", fmt.Sprintf("/api/v1/projects/%d", project.ID), `{} {}`, 400},
		{"POST", "/api/v1/admin/maintenance", "", 200},
		{"POST", "/api/v1/admin/maintenance", "{} \n\t", 200},
		{"POST", "/api/v1/admin/maintenance", "null", 400},
		{"POST", "/api/v1/admin/maintenance", strings.Repeat(" ", (4<<20)+1), 413},
		{"POST", "/api/v1/admin/agents/missing/drain", `{"extra":true}`, 400},
		{"POST", "/api/v1/admin/agents/missing/resume", `{} {}`, 400},
		{"POST", "/api/v1/admin/agents/missing/drain", strings.Repeat(" ", (4<<20)+1), 413},
		{"POST", "/api/v1/admin/agents/missing/resume", strings.Repeat(" ", (4<<20)+1), 413},
		{"GET", "/api/v1/users/me", `{"ignored":true}`, 400},
		{"GET", "/api/v1/healthz", strings.Repeat(" ", (4<<20)+1), 413},
		{"POST", "/api/v1/environments", `{"name":"whitespace"}` + " \n\t", 201},
	} {
		req := httptest.NewRequest(
			tc.method,
			tc.path,
			strings.NewReader(tc.body),
		)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		// When
		router.ServeHTTP(rec, req)
		// Then
		if rec.Code != tc.status {
			t.Fatalf(
				"%s %s status=%d body=%.200s",
				tc.method,
				tc.path,
				rec.Code,
				rec.Body.String(),
			)
		}
		if !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("non-JSON response: %.200s", rec.Body.String())
		}
	}
	if _, err := h.repo.Queries.GetProject(t.Context(), project.ID); err != nil {
		t.Fatal("rejected delete removed project")
	}
	books, err := h.repo.Queries.ListRunbooks(t.Context(), project.ID)
	if err != nil || len(books) != 0 {
		t.Fatalf(
			"rejected nested input created runbook: %d err=%v",
			len(books),
			err,
		)
	}
}
