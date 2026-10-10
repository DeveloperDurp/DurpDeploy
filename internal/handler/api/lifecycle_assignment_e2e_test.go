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
	assertLifecycleStageAccess(t, f, lifecycleID, setRole)
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/lifecycles/%d", lifecycleID),
		nil,
		403,
	)
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		nil,
		403,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/lifecycles/%d", lifecycleID),
		url.Values{"_method": {"delete"}},
		403,
	)
	f.web(
		t,
		"DELETE",
		fmt.Sprintf("/environments/%d", f.environment.ID),
		nil,
		403,
	)
	if _, err := f.h.repo.Queries.GetLifecycle(
		t.Context(),
		lifecycleID,
	); err != nil {
		t.Fatal("denied delete removed lifecycle", err)
	}
	if _, err := f.h.repo.Queries.GetEnvironment(
		t.Context(),
		f.environment.ID,
	); err != nil {
		t.Fatal("denied delete removed environment", err)
	}
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
	if !strings.Contains(page, "Remove lifecycle assignment") ||
		!strings.Contains(page, "A global admin manages lifecycle assignment") {
		t.Fatal("project admin cannot remove an existing assignment")
	}
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID,
			UserID:    user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": 0},
		403,
	)
	f.web(
		t,
		"PUT",
		fmt.Sprintf("/projects/%d", f.project.ID),
		url.Values{"name": {f.project.Name}, "lifecycle_id": {""}},
		403,
	)
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID,
			UserID:    user.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.web(
		t,
		"PUT",
		fmt.Sprintf("/projects/%d", f.project.ID),
		url.Values{"name": {f.project.Name}, "lifecycle_id": {""}},
		303,
	)
	current, err = f.h.repo.Queries.GetProject(t.Context(), f.project.ID)
	if err != nil || current.LifecycleID.Valid {
		t.Fatal("web removal retained lifecycle", err)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycleID},
		403,
	)
	setRole("admin")
	f.api(t, "PUT", path, map[string]any{
		"name": unbound.Name, "lifecycle_id": lifecycleID,
	}, 200)
	var disposable struct {
		ID int64 `json:"id"`
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "delete-shared-parent"},
			201,
		),
		&disposable,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/variables", disposable.ID),
		map[string]string{"name": "VALUE", "value": "default"},
		201,
	)
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/lifecycles/%d", disposable.ID),
		nil,
		204,
	)
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/lifecycles/%d", disposable.ID),
		nil,
		404,
	)
	setRole("deployer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: unbound.ID, UserID: user.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		path,
		map[string]any{"name": unbound.Name, "lifecycle_id": 0},
		403,
	)
	setRole("admin")
	f.api(t, "PUT", path, map[string]any{
		"name": unbound.Name, "lifecycle_id": 0,
	}, 200)
	setRole("deployer")
	f.api(t, "PUT", path, map[string]any{"name": unbound.Name}, 200)
	f.web(t, "PUT", fmt.Sprintf("/projects/%d", unbound.ID),
		url.Values{"name": {unbound.Name}, "lifecycle_id": {""}}, 303)
	f.api(
		t,
		"PUT",
		path,
		map[string]any{"name": unbound.Name, "lifecycle_id": lifecycleID},
		403,
	)
}
