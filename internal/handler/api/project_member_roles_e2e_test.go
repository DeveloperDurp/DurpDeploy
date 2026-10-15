package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func memberRoleSession(t *testing.T, h *harness, userID int64) string {
	t.Helper()
	session := fmt.Sprintf("member-role-%d", userID)
	if _, err := h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: session, UserID: userID, CsrfToken: "member-role-csrf",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestProjectMemberRolesAPIWebE2E(t *testing.T) {
	h := newAPIHarness(t)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Month|cron.Dom|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	project, other := seedProject(t, h.repo), seedProject(t, h.repo)
	target := seedAPIUser(t, h.repo, "target@member.test", "deployer")
	add := func(projectID, userID int64, role string) {
		t.Helper()
		if err := h.repo.Queries.AddProjectMember(t.Context(),
			db.AddProjectMemberParams{
				ProjectID: projectID, UserID: userID, Role: role,
			}); err != nil {
			t.Fatal(err)
		}
	}
	add(project.ID, target.ID, "deployer")
	add(other.ID, target.ID, "admin")
	member := func(projectID int64) db.ProjectMember {
		t.Helper()
		m, err := h.repo.Queries.GetProjectMember(t.Context(),
			db.GetProjectMemberParams{ProjectID: projectID, UserID: target.ID})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	original, otherOriginal := member(project.ID), member(other.ID)
	client := &http.Client{CheckRedirect: func(
		*http.Request, []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}}
	request := func(t *testing.T, path, body, token, session string,
		hx, csrf bool,
		want int,
	) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "PUT",
			srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", "member-role-csrf")
		}
		if hx {
			req.Header.Set("HX-Request", "true")
			req.Header.Set("X-Form-Dialog", "true")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("PUT %s: status=%d body=%s error=%v",
				path, resp.StatusCode, data, err)
		}
		return resp, string(data)
	}
	path := fmt.Sprintf("/projects/%d/members/%d", project.ID, target.ID)
	for _, tc := range []struct {
		name, global, project string
		allowed               bool
	}{
		{"global-admin", "admin", "", true},
		{"project-admin", "deployer", "admin", true},
		{"project-deployer", "deployer", "deployer", false},
		{"viewer-project-admin", "viewer", "admin", false},
		{"other-project-admin", "deployer", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := seedAPIUser(t, h.repo, tc.name+"@member.test", tc.global)
			add(other.ID, user.ID, "admin")
			if tc.project != "" {
				add(project.ID, user.ID, tc.project)
			}
			_, token := seedAPIToken(t, h.repo, user.ID)
			session := memberRoleSession(t, h, user.ID)
			for _, role := range []string{"admin", "deployer"} {
				before := member(project.ID)
				apiStatus, webStatus, hxStatus := 403, 403, 200
				if tc.allowed {
					apiStatus, webStatus = 200, 303
				}
				request(t, "/api/v1"+path, `{"role":"`+role+`"}`,
					token, "", false, false, apiStatus)
				if tc.allowed {
					if member(project.ID).Role != role {
						t.Fatal("API did not change the role")
					}
					add(project.ID, target.ID, before.Role)
				}
				request(t, path, "role="+role, "", session,
					false, true, webStatus)
				if tc.allowed {
					if member(project.ID).Role != role {
						t.Fatal("web request did not change the role")
					}
					add(project.ID, target.ID, before.Role)
				}
				resp, body := request(t, path, "role="+role, "", session,
					true, true, hxStatus)
				if tc.allowed {
					if !strings.Contains(body, `id="project-members"`) ||
						!strings.Contains(body, "data-member-role>"+role) ||
						!strings.Contains(resp.Header.Get("HX-Trigger"),
							"Member role updated") ||
						resp.Header.Get("HX-Redirect") != "" {
						t.Fatal("role save did not return a success fragment")
					}
					if got := member(project.ID); got.Role != role ||
						got.CreatedAt != original.CreatedAt {
						t.Fatalf("role update replaced membership: %+v", got)
					}
				} else if member(project.ID) != before {
					t.Fatal("denied role update changed membership")
				}
			}
			if !tc.allowed {
				return
			}
			for _, role := range []string{"", "viewer", "owner"} {
				request(t, "/api/v1"+path, `{"role":"`+role+`"}`,
					token, "", false, false, 422)
				request(t, path, "role="+role, "", session, true, true, 422)
			}
			request(t, path, "role=admin", "", session, false, false, 403)
			invalid := fmt.Sprintf("/projects/%d/members/invalid", project.ID)
			request(t, "/api/v1"+invalid, `{"role":"admin"}`,
				token, "", false, false, 400)
			request(t, invalid, "role=admin", "", session, true, true, 400)
			for _, suffix := range []string{"999999", fmt.Sprint(user.ID)} {
				// Global admins need no membership in the managed project.
				if suffix == fmt.Sprint(user.ID) && tc.project != "" {
					continue
				}
				missing := fmt.Sprintf("/projects/%d/members/%s",
					project.ID, suffix)
				request(t, "/api/v1"+missing, `{"role":"admin"}`,
					token, "", false, false, 404)
				request(t, missing, "role=admin", "", session, true, true, 404)
			}
			if member(project.ID).Role != "deployer" {
				t.Fatal("invalid request changed the role")
			}
			var audited int
			err := h.repo.DB.QueryRowContext(t.Context(),
				`SELECT COUNT(*) FROM audit_log
				 WHERE user_id = ? AND action = 'update_project_member'`,
				user.ID,
			).Scan(&audited)
			if err != nil || audited != 6 {
				t.Fatalf("successful role changes audited=%d, error=%v",
					audited, err)
			}
		})
	}
	request(t, "/api/v1"+path, `{"role":"admin"}`, "", "",
		false, false, 401)
	request(t, path, "role=admin", "", "", false, false, 303)
	if member(other.ID) != otherOriginal {
		t.Fatal("role update changed another project membership")
	}
	user, err := h.repo.Queries.GetUserByID(t.Context(), target.ID)
	if err != nil || user.Role != "deployer" {
		t.Fatalf("role update changed global role: %+v, %v", user, err)
	}
}
