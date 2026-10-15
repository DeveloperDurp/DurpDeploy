package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type permissionFixture struct {
	h      *harness
	server *httptest.Server
}

func newPermissionFixture(t *testing.T) permissionFixture {
	t.Helper()
	h := newAPIHarness(t)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Month|cron.Dom|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	return permissionFixture{h: h, server: srv}
}

func (f permissionFixture) request(t *testing.T,
	method, path, body, token, session string, hx bool, want int,
) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method,
		f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.AddCookie(&http.Cookie{Name: "session", Value: session})
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", "member-role-csrf")
	}
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path,
			response.StatusCode, want, data)
	}
	if hx && want == 200 && response.Header.Get("HX-Trigger") == "" {
		t.Fatal("blocked HTMX write did not return a toast")
	}
	return data
}

func TestPermissionRolesAPIProjectTokenMatrixE2E(t *testing.T) {
	f := newPermissionFixture(t)
	p := seedProject(t, f.h.repo)
	foreign := seedProject(t, f.h.repo)
	target := seedAPIUser(t, f.h.repo, "role-target@example.test", "viewer")
	for _, ownerRole := range roles.All() {
		owner := seedAPIUser(t, f.h.repo, ownerRole+"@roles.test", ownerRole)
		for _, memberRole := range roles.All() {
			if err := f.h.repo.Queries.AddProjectMember(t.Context(),
				db.AddProjectMemberParams{ProjectID: p.ID,
					UserID: owner.ID, Role: memberRole}); err != nil {
				t.Fatal(err)
			}
			for _, ceiling := range append([]string{""}, roles.All()...) {
				for _, scope := range []string{"global", "scoped"} {
					if scope == "scoped" && ceiling == "" {
						continue
					}
					name := ownerRole + "/" + memberRole + "/" + scope + "/" + ceiling
					t.Run(name, func(t *testing.T) {
						row, token := seedAPIToken(t, f.h.repo, owner.ID)
						grant := repository.TokenScope{
							Scope: scope,
							Role:  ceiling,
						}
						if scope == "scoped" {
							grant.ProjectIDs = []int64{p.ID}
						}
						if _, err := f.h.repo.ReplaceTokenScope(t.Context(),
							row, grant); err != nil {
							t.Fatal(err)
						}
						base := fmt.Sprintf("/api/v1/projects/%d", p.ID)
						f.request(t, "GET", base, "", token, "", false, 200)
						want := 403
						if ownerRole == "admin" && scope == "global" {
							want = 200
						}
						f.request(t, "GET", fmt.Sprintf("/api/v1/projects/%d",
							foreign.ID), "", token, "", false, want)
						author := (ownerRole == "maintainer" || ownerRole == "admin") &&
							(ownerRole == "admin" || memberRole == "maintainer" || memberRole == "admin") &&
							(ceiling == "" || ceiling == "maintainer" || ceiling == "admin")
						want = 403
						if author {
							want = 201
						}
						data := f.request(t, "POST", base+"/variables",
							`{"name":"ROLE_MATRIX","value":"original"}`,
							token, "", false, want)
						if author {
							var variable struct {
								ID int64 `json:"id"`
							}
							if err := json.Unmarshal(data, &variable); err != nil {
								t.Fatal(err)
							}
							path := fmt.Sprintf(
								"%s/variables/%d",
								base,
								variable.ID,
							)
							f.request(t, "PUT", path,
								`{"name":"ROLE_MATRIX","value":"updated"}`,
								token, "", false, 200)
							f.request(
								t,
								"DELETE",
								path,
								"",
								token,
								"",
								false,
								204,
							)
						}
						manage := author &&
							(ceiling == "" || ceiling == "admin") &&
							(ownerRole == "admin" || memberRole == "admin")
						want = 403
						if manage {
							want = 201
						}
						f.request(t, "POST", base+"/members", fmt.Sprintf(
							`{"user_id":%d,"role":"viewer"}`, target.ID),
							token, "", false, want)
						if ceiling != "" {
							for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
								path := "/api/v1/tokens"
								if method == "PATCH" || method == "DELETE" {
									path += "/" + row
								}
								f.request(
									t,
									method,
									path,
									`{}`,
									token,
									"",
									false,
									403,
								)
							}
						}
					})
				}
			}
		}
	}
}

func TestPermissionRolesForgedBrowserAndAPIWritesE2E(t *testing.T) {
	f := newPermissionFixture(t)
	p := seedProject(t, f.h.repo)
	base := fmt.Sprintf("/projects/%d", p.ID)
	for _, role := range []string{"viewer", "deployer"} {
		user := seedAPIUser(t, f.h.repo, role+"@forged.test", role)
		if err := f.h.repo.Queries.AddProjectMember(t.Context(),
			db.AddProjectMemberParams{ProjectID: p.ID, UserID: user.ID,
				Role: "admin"}); err != nil {
			t.Fatal(err)
		}
		_, token := seedAPIToken(t, f.h.repo, user.ID)
		session := memberRoleSession(t, f.h, user.ID)
		for _, action := range []struct{ method, path string }{
			{"POST", "/projects"}, {"PUT", base}, {"DELETE", base},
			{"POST", base + "/steps"}, {"PUT", base + "/steps/1"},
			{"DELETE", base + "/steps/1"}, {"PATCH", base + "/steps/reorder"},
			{"POST", base + "/variables"}, {"PUT", base + "/variables/1"},
			{"DELETE", base + "/variables/1"},
			{"POST", base + "/releases"}, {"DELETE", base + "/releases/1"},
			{"POST", base + "/releases/1/refresh"},
			{"POST", base + "/runbooks"}, {"PUT", base + "/runbooks/1"},
			{"DELETE", base + "/runbooks/1"},
			{"POST", base + "/runbooks/1/executions"},
			{"POST", base + "/runbook-executions/1/retry"},
			{"POST", base + "/runbook-executions/1/cancel"},
			{"POST", base + "/schedules"}, {"PUT", base + "/schedules/1"},
			{"DELETE", base + "/schedules/1"}, {"POST", base + "/schedules/1/toggle"},
			{"POST", base + "/members"}, {"PUT", base + "/members/1"},
			{"DELETE", base + "/members/1"},
			{"PUT", base + "/package-repository"}, {"DELETE", base + "/package-repository"},
			{"PUT", base + "/notifications"},
			{"POST", "/templates"}, {"PUT", "/templates/1"}, {"DELETE", "/templates/1"},
			{"POST", "/environments"}, {"POST", "/lifecycles"},
			{"POST", "/deployments/1/rollback"}, {"POST", "/deployments/1/approve"},
			{"POST", "/admin/users"}, {"PUT", "/admin/users/1"},
		} {
			f.request(t, action.method, "/api/v1"+action.path, `{}`,
				token, "", false, 403)
			webMethod, webPath := action.method, action.path
			switch {
			case strings.Contains(webPath, "/runbook-executions/"):
				webPath = strings.Replace(
					webPath,
					"/runbook-executions/",
					"/runbooks/executions/",
					1,
				)
			case webPath == base+"/runbooks/1/executions":
				webPath = base + "/runbooks/1/execute"
			case webPath == base+"/runbooks/1":
				webMethod = "POST"
				if action.method == "PUT" {
					webPath += "/save"
				} else {
					webPath += "/delete"
				}
			case webPath == base+"/package-repository":
				webMethod = "POST"
				if action.method == "DELETE" {
					webPath += "/delete"
				}
			case webPath == base+"/notifications":
				webMethod = "POST"
			}
			for _, hx := range []bool{false, true} {
				want := 403
				if hx {
					want = 200
				}
				f.request(t, webMethod, webPath, "name=forged",
					"", session, hx, want)
			}
		}
	}
	current, err := f.h.repo.Queries.GetProject(t.Context(), p.ID)
	if err != nil || current.Name != p.Name {
		t.Fatal("denied write changed project")
	}
	for _, table := range []string{"steps", "variables", "releases", "runbooks", "scheduled_deployments", "step_templates", "deployments"} {
		var count int
		if err := f.h.repo.DB.QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("denied writes changed %s: %d rows", table, count)
		}
	}
}
