//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func (f permissionGuidanceFixture) history(t *testing.T) int64 {
	t.Helper()
	env := seedEnv(t, f.h.repo)
	good := seedRelease(t, f.h.repo, f.project.ID)
	seedDeployment(t, f.h.repo, good.ID, env.ID, "succeeded")
	failed := seedRelease(t, f.h.repo, f.project.ID)
	return seedDeployment(t, f.h.repo, failed.ID, env.ID, "failed").ID
}

func TestPermissionGuidanceDeploymentAPIWebE2E(t *testing.T) {
	f := newPermissionGuidanceFixture(t)
	id := f.history(t)
	project := fmt.Sprintf("/projects/%d", f.project.ID)
	for _, pair := range [][2]string{{"deployer", "viewer"}, {"viewer", "deployer"}} {
		actor := f.actor(t, pair[0], pair[1])
		page := f.request(
			t,
			"GET",
			project+"/deploy",
			"",
			"",
			actor.session,
			false,
			200,
		)
		assertRecoveryLinks(
			t,
			page,
			project,
			pair[0] == "viewer",
			pair[1] == "viewer",
		)
	}
	for _, member := range []string{"viewer", "deployer"} {
		actor := f.actor(t, "maintainer", member)
		path := fmt.Sprintf("/deployments/%d/rollback", id)
		page := f.request(t, "GET", path, "", "", actor.session, false, 200)
		assertRecoveryLinks(t, page, project, false, true)
		f.request(t, "POST", "/api/v1"+path, `{}`,
			actor.token, "", false, 403)
		assertGuidanceDeploymentDenial(t, f, actor, path, project)
		if member == "viewer" {
			path = fmt.Sprintf("/deployments/%d/redeploy", id)
			f.request(t, "POST", "/api/v1"+path, `{}`,
				actor.token, "", false, 403)
			assertGuidanceDeploymentDenial(t, f, actor, path, project)
		}
	}
	var deployments struct{ Total int }
	decodeStepLogTest(t, f.request(t, "GET", f.apiPath("/deployments"), "",
		f.adminToken, "", false, 200), &deployments)
	if deployments.Total != 2 {
		t.Fatal("denied deployment action created an execution")
	}
}

func assertRecoveryLinks(
	t *testing.T,
	page []byte,
	project string,
	global, member bool,
) {
	t.Helper()
	doc := dashboardDocument(t, string(page))
	if (len(dashboardElements(doc, "href", "/admin/users")) > 0) != global ||
		(len(dashboardElements(doc, "href", project+"/edit#project-members")) > 0) != member {
		t.Fatal("recovery identifies the wrong permission boundary")
	}
}

func assertGuidanceDeploymentDenial(
	t *testing.T,
	f permissionGuidanceFixture,
	actor permissionGuidanceActor,
	path, project string,
) {
	t.Helper()
	for _, hx := range []bool{false, true} {
		req, err := http.NewRequestWithContext(t.Context(), "POST",
			f.server.URL+path, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: actor.session})
		req.Header.Set("X-CSRF-Token", "member-role-csrf")
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		response, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !hx {
			if response.StatusCode != 403 {
				t.Fatalf("denial status = %d", response.StatusCode)
			}
			assertRecoveryLinks(t, body, project, false, true)
			continue
		}
		assertGuidanceHTMXDenial(t, response)
	}
}

func assertGuidanceHTMXDenial(t *testing.T, response *http.Response) {
	t.Helper()
	var event struct {
		MakeToast struct{ Level, Message string }
	}
	if err := json.Unmarshal([]byte(response.Header.Get("HX-Trigger")), &event); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || event.MakeToast.Level != "danger" ||
		!strings.Contains(event.MakeToast.Message, "project role") ||
		strings.Contains(event.MakeToast.Message, "global role") ||
		response.Header.Get("HX-Reswap") != "none" {
		t.Fatal(
			"HTMX deployment denial lost its project recovery or status contract",
		)
	}
}
