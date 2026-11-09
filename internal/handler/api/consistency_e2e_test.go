//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"

	"github.com/robfig/cron/v3"
)

func TestAPIConsistencyE2E(t *testing.T) {
	h := newAPIHarness(t)
	admin := seedAPIUser(t, h.repo, "consistency-admin@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, admin.ID)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	f := &artifactE2E{
		h:       h,
		baseURL: srv.URL,
		client:  srv.Client(),
		token:   token,
	}
	project := seedProject(t, h.repo)
	paths := []string{"variables", "steps", "runbooks", "project-templates"}
	for _, role := range []string{"admin", "maintainer", "deployer", "viewer"} {
		user := seedAPIUser(t, h.repo, role+"@consistency.test", role)
		_, f.token = seedAPIToken(t, h.repo, user.ID)
		for _, path := range paths {
			f.api(t, "GET", "/api/v1/projects/999999/"+path, nil, 404)
			status := 403
			if role == "admin" {
				status = 200
			}
			f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d/%s",
				project.ID, path), nil, status)
		}
	}
	f.token = token
	var scoped projectToken
	if err := json.Unmarshal(f.api(t, "POST", "/api/v1/tokens",
		map[string]any{"name": "consistency-scope", "scope": "scoped",
			"role": "viewer", "project_ids": []int64{project.ID}}, 201),
		&scoped); err != nil {
		t.Fatal(err)
	}
	f.token = scoped.ID
	for _, path := range paths {
		f.api(t, "GET", "/api/v1/projects/999999/"+path, nil, 403)
	}
	f.token = token
	checkAPIConsistencyMediaTypes(t, f)
}

func checkAPIConsistencyMediaTypes(t *testing.T, f *artifactE2E) {
	t.Helper()
	for index, media := range []string{
		"", "text/plain", "application/x-www-form-urlencoded",
		"application/json; charset", "application/json; charset=utf-8",
	} {
		name := fmt.Sprintf("content-type-%d", index)
		req, err := http.NewRequestWithContext(
			t.Context(),
			"POST",
			f.baseURL+"/api/v1/projects",
			strings.NewReader(`{"name":"`+name+`"}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", media)
		response, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		testutil.Close(t, response.Body)
		status, count := 415, 0
		if index == 4 {
			status, count = 201, 1
		}
		if err != nil || response.StatusCode != status {
			t.Fatalf("media=%q status=%d body=%s err=%v",
				media, response.StatusCode, body, err)
		}
		var stored int
		if err := f.h.repo.DB.QueryRowContext(t.Context(),
			"SELECT count(*) FROM projects WHERE name = ?", name,
		).Scan(&stored); err != nil || stored != count {
			t.Fatalf(
				"rejected write mutated data: count=%d err=%v",
				stored,
				err,
			)
		}
	}
}
