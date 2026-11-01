//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"durpdeploy/internal/db"
)

type projectCreationResult struct {
	ID                int64
	Name, Description string
	GroupID           *int64 `json:"group_id"`
	LifecycleID       *int64 `json:"lifecycle_id"`
}

func TestProjectGroupsCreateAPIWebE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "Creation group")
	for _, mode := range []string{"grouped", "null", "omitted"} {
		t.Run(mode, func(t *testing.T) {
			body := map[string]any{"name": "API " + mode}
			var want *int64
			if mode == "grouped" {
				want = &group
				body["group_id"] = group
			} else if mode == "null" {
				body["group_id"] = nil
			}
			var created, stored projectCreationResult
			decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/projects",
				body, 201), &created)
			require.Equal(t, want, created.GroupID)
			decodeLifecycleTest(t, f.api(
				t,
				"GET",
				fmt.Sprintf(
					"/api/v1/projects/%d",
					created.ID,
				),
				nil,
				200,
			), &stored)
			require.Equal(t, created, stored)
			members, err := f.h.repo.Queries.ListProjectMembers(
				t.Context(),
				created.ID,
			)
			require.NoError(t, err)
			require.Len(t, members, 1)
			require.Equal(t, "admin", members[0].Role)
		})
	}
	for _, raw := range []string{"", fmt.Sprint(group)} {
		name := "Web group " + raw
		f.web(t, "POST", "/projects", url.Values{
			"name": {name}, "group_id": {raw},
		}, 303)
		var page struct{ Items []projectCreationResult }
		decodeLifecycleTest(t, f.api(t, "GET",
			"/api/v1/projects?q="+url.QueryEscape(name), nil, 200), &page)
		require.Len(t, page.Items, 1)
		if raw == "" {
			require.Nil(t, page.Items[0].GroupID)
		} else {
			require.Equal(t, &group, page.Items[0].GroupID)
		}
	}
}

func TestProjectGroupsCreateValidationE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "Retained selection")
	f.api(t, "POST", "/api/v1/projects", map[string]string{
		"name": "Existing project",
	}, 201)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Retained lifecycle"}, 201), &lifecycle)
	for _, name := range []string{"", "Existing project"} {
		body := f.web(t, "POST", "/projects", url.Values{
			"name": {name}, "description": {"Retained description"},
			"group_id":     {fmt.Sprint(group)},
			"lifecycle_id": {fmt.Sprint(lifecycle.ID)},
		}, 422)
		require.Contains(t, body, "Retained description")
		require.Contains(t, body,
			fmt.Sprintf(`value="%d" selected>Retained selection`, group))
		require.Contains(t, body,
			fmt.Sprintf(`value="%d" selected>Retained lifecycle`, lifecycle.ID))
	}
	for i, invalid := range []any{0, -1, "1", 1.5, true, []int{1}, 1e30, 999999} {
		status := 400
		if i < 2 {
			status = 422
		} else if i == 7 {
			status = 404
		}
		f.api(t, "POST", "/api/v1/projects", map[string]any{
			"name": "Rejected API group", "group_id": invalid,
		}, status)
	}
	for _, invalid := range []string{"0", "-1", "bad", "1.5", "999999", "9223372036854775808"} {
		f.web(t, "POST", "/projects", url.Values{
			"name": {"Rejected web group"}, "group_id": {invalid},
		}, 422)
	}
	var page struct{ Total int64 }
	decodeLifecycleTest(t, f.api(t, "GET",
		"/api/v1/projects?q=Rejected", nil, 200), &page)
	require.Zero(t, page.Total)
}

func TestProjectGroupsCreateAuthorizationE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	owner := f.token
	visible := createGroupE2E(t, f, "Visible creation group")
	private := createGroupE2E(t, f, "Private creation group")
	empty := createGroupE2E(t, f, "Empty creation group")
	first := createGroupedProjectE2E(t, f, "Accessible project", &visible)
	hidden := createGroupedProjectE2E(t, f, "Hidden sibling", &visible)
	createGroupedProjectE2E(t, f, "Private project", &private)
	user := seedAPIUser(t, f.h.repo, "creator@groups.test", "maintainer")
	require.NoError(t, f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: first,
			UserID:    user.ID,
			Role:      "viewer",
		},
	))
	_, f.token = seedAPIToken(t, f.h.repo, user.ID)
	f.session = memberRoleSession(t, f.h, user.ID)
	form := f.web(t, "GET", "/projects/new", nil, 200)
	require.Contains(t, form, "Visible creation group")
	require.NotContains(t, form, "Private creation group")
	require.NotContains(t, form, "Empty creation group")
	for _, id := range []int64{private, empty, 999999} {
		f.api(t, "POST", "/api/v1/projects", map[string]any{
			"name": "Forbidden creation", "group_id": id,
		}, 404)
		f.web(t, "POST", "/projects", url.Values{
			"name": {"Forbidden creation"}, "group_id": {fmt.Sprint(id)},
		}, 422)
	}
	var created projectCreationResult
	decodeLifecycleTest(t, f.api(
		t,
		"POST",
		"/api/v1/projects",
		map[string]any{
			"name":     "Maintainer creation",
			"group_id": visible,
		},
		201,
	), &created)
	require.Equal(t, &visible, created.GroupID)
	membership, err := f.h.repo.Queries.GetProjectMember(t.Context(),
		db.GetProjectMemberParams{ProjectID: created.ID, UserID: user.ID})
	require.NoError(t, err)
	require.Equal(t, "admin", membership.Role)
	f.web(t, "POST", "/projects", url.Values{
		"name": {"Maintainer web creation"}, "group_id": {fmt.Sprint(visible)},
	}, 303)
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", hidden), nil, 403)
	for _, role := range []string{"viewer", "deployer"} {
		user := seedAPIUser(t, f.h.repo, role+"-creator@groups.test", role)
		_, f.token = seedAPIToken(t, f.h.repo, user.ID)
		f.session = memberRoleSession(t, f.h, user.ID)
		f.api(t, "POST", "/api/v1/projects", map[string]any{
			"name": "Blocked " + role, "group_id": visible,
		}, http.StatusForbidden)
		f.web(t, "POST", "/projects", url.Values{
			"name": {"Blocked " + role}, "group_id": {fmt.Sprint(visible)},
		}, http.StatusForbidden)
		if role == "viewer" {
			require.NotContains(t, f.web(t, "GET", "/projects/new", nil, 200),
				`name="group_id"`)
		}
	}
	f.token = owner
	token := mintProjectToken(t, f, "admin", first)
	f.token = token.ID
	f.api(t, "POST", "/api/v1/projects", map[string]any{
		"name": "Scoped creation", "group_id": visible,
	}, http.StatusForbidden)
}

func TestProjectGroupsCreateRollbackE2E(t *testing.T) {
	for _, target := range []string{"membership", "group"} {
		t.Run(target, func(t *testing.T) {
			f := newProjectGroupsE2E(t)
			group := createGroupE2E(t, f, "Atomic group")
			event := "INSERT ON project_members"
			if target == "group" {
				event = "UPDATE OF group_id ON projects"
			}
			_, err := f.h.repo.DB.ExecContext(t.Context(),
				"CREATE TRIGGER reject_group_creation BEFORE "+event+
					" BEGIN SELECT RAISE(ABORT, 'creation failed'); END")
			require.NoError(t, err)
			f.api(t, "POST", "/api/v1/projects", map[string]any{
				"name": "Atomic API project", "group_id": group,
			}, 500)
			f.web(t, "POST", "/projects", url.Values{
				"name": {"Atomic web project"}, "group_id": {fmt.Sprint(group)},
			}, 500)
			for _, table := range []string{"projects", "project_members"} {
				var count int
				err := f.h.repo.DB.QueryRowContext(t.Context(),
					"SELECT COUNT(*) FROM "+table).Scan(&count)
				require.NoError(t, err)
				require.Zero(t, count, "partial creation survived in %s", table)
			}
			_, err = f.h.repo.DB.ExecContext(t.Context(),
				"DROP TRIGGER reject_group_creation")
			require.NoError(t, err)
			var created projectCreationResult
			decodeLifecycleTest(
				t,
				f.api(t, "POST", "/api/v1/projects", map[string]any{
					"name": "Atomic API project", "group_id": group,
				}, 201),
				&created,
			)
			require.Equal(t, &group, created.GroupID)
		})
	}
}
