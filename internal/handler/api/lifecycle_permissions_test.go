package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestLifecycleVariableAPI_Permissions(t *testing.T) {
	f := newLifecycleAPI(t)
	path := fmt.Sprintf("/api/v1/lifecycles/%d/variables", f.lifecycle.ID)
	f.request(t, "POST", path, `{"name":"SHARED","value":"default"}`, 201)
	user := seedAPIUser(t, f.h.repo, "member@example.test", "viewer")
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	f.token = token
	f.request(t, "GET", path, "", 403)
	f.request(t, "POST", path, `{"name":"ATTACK"}`, 403)
	inherited := fmt.Sprintf(
		"/api/v1/projects/%d/variables/inherited",
		f.project.ID,
	)
	f.request(t, "GET", inherited, "", 403)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: f.project.ID, UserID: user.ID, Role: "deployer"}); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", inherited, "", 200)
	local := fmt.Sprintf("/api/v1/projects/%d/variables", f.project.ID)
	f.request(t, "POST", local, `{"name":"SHARED","value":"override"}`, 403)
	if err := f.h.repo.Queries.UpdateUser(t.Context(), db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "deployer"}); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", local, `{"name":"SHARED","value":"override"}`, 201)
	f.request(t, "GET", inherited, "", 200)
	other := seedProject(t, f.h.repo)
	f.request(
		t,
		"GET",
		fmt.Sprintf("/api/v1/projects/%d/variables/inherited", other.ID),
		"",
		403,
	)
}
