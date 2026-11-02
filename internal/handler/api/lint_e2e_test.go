//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
)

func lintE2EBody(script string) string {
	body, _ := json.Marshal(map[string]string{
		"profile": handler.BearerLintProfile, "script": script,
	})
	return string(body)
}

func lintE2ERequest(
	t *testing.T,
	f permissionFixture,
	method, path, body, token string,
	want int,
) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method,
		f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32_769))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want || !json.Valid(data) || len(data) > 32_768 {
		t.Fatalf(
			"%s %s: status%d want%d body%s",
			method,
			path,
			response.StatusCode,
			want,
			data,
		)
	}
	if method == "POST" && path == "/api/v1/lint" &&
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("lint response can be cached")
	}
	return data
}

func lintResourceCounts(t *testing.T, h *harness) []int64 {
	t.Helper()
	var counts []int64
	for _, table := range []string{"projects", "environments", "steps", "variables", "releases", "deployments", "deployment_steps", "audit_log"} {
		var count int64
		if err := h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, count)
	}
	return counts
}

func TestBearerLintRoleAndTokenCeilingsE2E(t *testing.T) {
	f := newPermissionFixture(t)
	project := seedProject(t, f.h.repo)
	for _, ownerRole := range roles.All() {
		owner := seedAPIUser(t, f.h.repo, ownerRole+"@lint.test", ownerRole)
		if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: project.ID, UserID: owner.ID, Role: "maintainer"}); err != nil {
			t.Fatal(err)
		}
		for _, ceiling := range append([]string{""}, roles.All()...) {
			for _, scope := range []string{"global", "scoped"} {
				if scope == "scoped" && ceiling == "" {
					continue
				}
				t.Run(ownerRole+"/"+scope+"/"+ceiling, func(t *testing.T) {
					assertLintTokenCase(t, f, project.ID, owner.ID,
						ownerRole, ceiling, scope)
				})
			}
		}
	}
}

func assertLintTokenCase(
	t *testing.T, f permissionFixture, projectID, ownerID int64,
	ownerRole, ceiling, scope string,
) {
	t.Helper()
	id, token := seedAPIToken(t, f.h.repo, ownerID)
	grant := repository.TokenScope{Scope: scope, Role: ceiling}
	if scope == "scoped" {
		grant.ProjectIDs = []int64{projectID}
	}
	if _, err := f.h.repo.ReplaceTokenScope(t.Context(), id, grant); err != nil {
		t.Fatal(err)
	}
	before := lintResourceCounts(t, f.h)
	body := lintE2ERequest(t, f, "POST", "/api/v1/lint",
		lintE2EBody("#!/bin/sh\necho $LINT_SECRET_CANARY"), token, 200)
	assertLintTokenWarnings(t, body)
	if !reflect.DeepEqual(before, lintResourceCounts(t, f.h)) {
		t.Fatal("analysis mutated application resources or audit")
	}
	if ceiling != "" {
		lintE2ERequest(t, f, "POST", "/api/v1/tokens",
			`{"name":"must-deny"}`, token, 403)
	}
	canAuthor := roles.AtLeast(ownerRole, "maintainer") &&
		(ceiling == "" || roles.AtLeast(ceiling, "maintainer"))
	if !canAuthor {
		lintE2ERequest(t, f, "POST",
			fmt.Sprintf("/api/v1/projects/%d/variables", projectID),
			`{"name":"MUST_DENY","value":"x"}`, token, 403)
		for _, path := range []string{"/api/v1/lint/", "/api/v1/lint/other", "/api/v1/lints"} {
			lintE2ERequest(t, f, "POST", path, lintE2EBody("#!"), token, 403)
		}
	}
}

func assertLintTokenWarnings(t *testing.T, body []byte) {
	t.Helper()
	var result struct {
		Profile     string                   `json:"profile"`
		Diagnostics []handler.LintDiagnostic `json:"diagnostics"`
		Truncated   bool                     `json:"truncated"`
	}
	if json.Unmarshal(body, &result) != nil ||
		result.Profile != handler.BearerLintProfile ||
		len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != 2086 ||
		result.Diagnostics[0].Line != 2 || result.Truncated ||
		strings.Contains(string(body), "LINT_SECRET_CANARY") {
		t.Fatalf("unexpected warnings: %s", body)
	}
}

func TestBearerLintCredentialFailuresAndDemotionE2E(t *testing.T) {
	f := newPermissionFixture(t)
	owner := seedAPIUser(t, f.h.repo, "credentials@lint.test", "admin")
	id, valid := seedAPIToken(t, f.h.repo, owner.ID)
	_, revoked := seedAPIToken(t, f.h.repo, owner.ID)
	row, err := f.h.repo.Queries.GetApiTokenByHash(
		t.Context(),
		db.GetApiTokenByHashParams{
			TokenHash: auth.HashApiToken(revoked),
			ExpiresAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.RevokeApiToken(t.Context(), row.ID); err != nil {
		t.Fatal(err)
	}
	expired, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{ID: "expired-lint", UserID: owner.ID, Name: "expired", TokenPrefix: prefix, TokenHash: hash, Scope: "global", ExpiresAt: sql.NullInt64{Int64: time.Now().Add(-time.Hour).Unix(), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	deleted := seedAPIUser(t, f.h.repo, "deleted@lint.test", "viewer")
	_, deletedToken := seedAPIToken(t, f.h.repo, deleted.ID)
	if err := f.h.repo.Queries.DeleteUser(t.Context(), deleted.ID); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "malformed", revoked, expired, deletedToken} {
		lintE2ERequest(t, f, "POST", "/api/v1/lint", "not-json", token, 401)
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		f.server.URL+"/api/v1/lint",
		strings.NewReader(lintE2EBody("#!")),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(
		&http.Cookie{
			Name:  "session",
			Value: memberRoleSession(t, f.h, owner.ID),
		},
	)
	req.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 || response.Header.Get("Location") != "" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"cookie-only: status%d headers%v",
			response.StatusCode,
			response.Header,
		)
	}
	if _, err := f.h.repo.ReplaceTokenScope(t.Context(), id, repository.TokenScope{Scope: "global", Role: "maintainer"}); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.UpdateUser(t.Context(), db.UpdateUserParams{ID: owner.ID, Name: owner.Name, Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	lintE2ERequest(t, f, "POST", "/api/v1/lint", lintE2EBody("#!"), valid, 200)
	lintE2ERequest(
		t,
		f,
		"POST",
		"/api/v1/environments",
		`{"name":"must-deny"}`,
		valid,
		403,
	)
}

func TestBearerLintNoExecutionOrStoredResourceAccessE2E(t *testing.T) {
	f := newPermissionFixture(t)
	owner := seedAPIUser(t, f.h.repo, "canaries@lint.test", "viewer")
	_, token := seedAPIToken(t, f.h.repo, owner.ID)
	var callbacks atomic.Int32
	callback := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { callbacks.Add(1); w.WriteHeader(204) },
		),
	)
	t.Cleanup(callback.Close)
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	created := filepath.Join(dir, "must-not-exist")
	if err := os.WriteFile(existing, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(
		"#!/bin/sh\n# $PRIVATE_LINT_CANARY\ntouch '%s'\nprintf changed > '%s'\ncurl '%s'\n`curl '%s'`\n$(curl '%s')\n. '%s'\n",
		created,
		existing,
		callback.URL,
		callback.URL,
		callback.URL,
		existing,
	)
	before := lintResourceCounts(t, f.h)
	body := lintE2ERequest(
		t,
		f,
		"POST",
		"/api/v1/lint",
		lintE2EBody(script),
		token,
		200,
	)
	if callbacks.Load() != 0 ||
		strings.Contains(string(body), "PRIVATE_LINT_CANARY") ||
		!reflect.DeepEqual(before, lintResourceCounts(t, f.h)) {
		t.Fatal(
			"analysis made a callback, disclosed source, or mutated resources",
		)
	}
	if data, err := os.ReadFile(existing); err != nil ||
		string(data) != "unchanged" {
		t.Fatal("existing canary changed")
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("execution canary exists")
	}
	for _, body := range []string{
		`{"profile":"builtin-shell-warnings-v1","script":"#!","project_id":1}`,
		`{"profile":"builtin-shell-warnings-v1","script":"#!","path":"/private"}`,
	} {
		lintE2ERequest(t, f, "POST", "/api/v1/lint", body, token, 400)
	}
	lintE2ERequest(
		t,
		f,
		"POST",
		"/api/v1/lint?project_id=1",
		lintE2EBody("#!"),
		token,
		400,
	)
}
