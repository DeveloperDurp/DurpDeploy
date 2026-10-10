//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func assertLifecycleAssignmentAccess(
	t *testing.T,
	f *artifactE2E,
	lifecycleID int64,
) {
	t.Helper()
	user, err := f.h.repo.Queries.GetUserByEmail(
		t.Context(), "artifact-e2e@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: user.ID, Role: "admin",
		}); err != nil {
		t.Fatal(err)
	}
	setRole := func(role string) {
		t.Helper()
		if err := f.h.repo.Queries.UpdateUser(
			t.Context(),
			db.UpdateUserParams{
				ID:   user.ID,
				Name: user.Name,
				Role: role,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	setRole("deployer")
	defer setRole("admin")
	count, err := f.h.repo.Queries.CountProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", "/api/v1/projects", map[string]any{
		"name": "unauthorized-api", "lifecycle_id": lifecycleID,
	}, 403)
	f.web(t, "POST", "/projects", url.Values{
		"name":         {"unauthorized-web"},
		"lifecycle_id": {fmt.Sprint(lifecycleID)},
	}, 403)
	if after, err := f.h.repo.Queries.CountProjects(
		t.Context(),
	); err != nil ||
		after != count {
		t.Fatalf(
			"denied create mutated projects: %d -> %d, %v",
			count,
			after,
			err,
		)
	}
	var unbound struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "assignment-unbound"}, 201), &unbound)
	path := fmt.Sprintf("/api/v1/projects/%d", unbound.ID)
	f.api(t, "PUT", path, map[string]any{
		"name": "unauthorized-rename", "lifecycle_id": lifecycleID,
	}, 403)
	f.web(t, "PUT", fmt.Sprintf("/projects/%d", unbound.ID), url.Values{
		"name":         {"unauthorized-web-rename"},
		"lifecycle_id": {fmt.Sprint(lifecycleID)},
	}, 403)
	current, err := f.h.repo.Queries.GetProject(t.Context(), unbound.ID)
	if err != nil || current.Name != unbound.Name || current.LifecycleID.Valid {
		t.Fatalf("denied assignment mutated project: %+v, %v", current, err)
	}
	// A previously granted lifecycle can be retained during ordinary edits.
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycleID,
	}, 200)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/projects/%d/edit", f.project.ID),
		nil,
		200,
	)
	if strings.Contains(page, `<select name="lifecycle_id"`) ||
		!strings.Contains(page, "A global admin manages lifecycle assignment") {
		t.Fatal("nonadmin has lifecycle assignment affordance")
	}
	setRole("admin")
	f.api(t, "PUT", path, map[string]any{
		"name": unbound.Name, "lifecycle_id": lifecycleID,
	}, 200)
}
