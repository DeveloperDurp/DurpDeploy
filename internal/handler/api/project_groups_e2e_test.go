//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type groupPage struct {
	Items []struct {
		ID           int64
		Name         string
		ProjectCount int64 `json:"project_count"`
	}
	Total int64
}

func newProjectGroupsE2E(t *testing.T) *artifactE2E {
	t.Helper()
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "groups-admin@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, u.ID)
	srv := httptest.NewServer(server.NewRouter(
		h.repo,
		h.runner,
		cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		),
		handler.NewAuthHandler(h.repo),
	))
	t.Cleanup(srv.Close)
	return &artifactE2E{
		h:       h,
		baseURL: srv.URL,
		token:   token,
		csrf:    "member-role-csrf",
		session: memberRoleSession(
			t,
			h,
			u.ID,
		),
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func createGroupE2E(t *testing.T, f *artifactE2E, name string) int64 {
	t.Helper()
	var group struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/project-groups",
			map[string]string{"name": name},
			201,
		),
		&group,
	)
	return group.ID
}

func createGroupedProjectE2E(
	t *testing.T,
	f *artifactE2E,
	name string,
	groupID *int64,
) int64 {
	t.Helper()
	var project struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/projects",
			map[string]string{"name": name},
			201,
		),
		&project,
	)
	if groupID != nil {
		f.api(
			t,
			"PUT",
			fmt.Sprintf("/api/v1/projects/%d/group", project.ID),
			map[string]any{"group_id": groupID},
			200,
		)
	}
	return project.ID
}

func TestProjectGroupsDefaultSortE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	alpha := createGroupedProjectE2E(t, f, "Default Alpha", nil)
	zulu := createGroupedProjectE2E(t, f, "Default Zulu", nil)
	for _, test := range []struct {
		query string
		want  int64
	}{{"", alpha}, {"&sort=newest", zulu}, {"&sort=name_desc", zulu}} {
		var page struct {
			Items []struct{ ID int64 }
			Total int64
		}
		decodeLifecycleTest(t, f.api(t, "GET",
			"/api/v1/projects?q=Default&limit=1"+test.query, nil, 200), &page)
		if page.Total != 2 || len(page.Items) != 1 ||
			page.Items[0].ID != test.want {
			t.Fatalf("sort %q: %+v", test.query, page)
		}
	}
	for _, path := range []string{"/projects", "/project-groups"} {
		body := f.web(t, "GET", path+"?q=Default&limit=1", nil, 200)
		if !strings.Contains(body, "Default Alpha") ||
			strings.Contains(body, "Default Zulu") {
			t.Fatalf("%s did not default to A-Z", path)
		}
	}
}

func TestProjectGroupsAPIWebE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	team, other := createGroupE2E(t, f, "Team"), createGroupE2E(t, f, "Other")
	first := createGroupedProjectE2E(t, f, "Group Alpha", &team)
	second := createGroupedProjectE2E(t, f, "Group Beta", &team)
	third := createGroupedProjectE2E(t, f, "Group Gamma", nil)
	move := fmt.Sprintf("/api/v1/projects/%d/group", first)
	for _, name := range []string{"", "Ungrouped", "bad\nname", strings.Repeat("x", 101)} {
		f.api(
			t,
			"POST",
			"/api/v1/project-groups",
			map[string]string{"name": name},
			422,
		)
	}
	f.api(
		t,
		"POST",
		"/api/v1/project-groups",
		map[string]string{"name": " team "},
		409,
	)
	for _, body := range []any{map[string]any{}, map[string]any{"group_id": 0}, map[string]any{"group_id": -1}, map[string]any{"group_id": "1"}, map[string]any{"group_id": 1.5}} {
		f.api(t, "PUT", move, body, 422)
	}
	f.api(t, "PUT", move, map[string]int64{"group_id": 999999}, 404)
	for _, query := range []string{"sort=bad", "group_id=-1", "offset=-1", "limit=0", "limit=1001", "offset=9223372036854775808"} {
		f.api(t, "GET", "/api/v1/projects?"+query, nil, 400)
		f.web(t, "GET", "/projects?"+query, nil, 400)
	}
	var page struct {
		Items []struct{ ID int64 }
		Total int64
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf(
				"/api/v1/projects?q=Group&sort=name&group_id=%d&limit=1&offset=1",
				team,
			),
			nil,
			200,
		),
		&page,
	)
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != second {
		t.Fatalf("filtered page: %+v", page)
	}
	decodeLifecycleTest(
		t,
		f.api(t, "GET", "/api/v1/projects?group_id=0", nil, 200),
		&page,
	)
	if page.Total != 1 || page.Items[0].ID != third {
		t.Fatalf("Ungrouped: %+v", page)
	}
	web := f.web(
		t,
		"GET",
		fmt.Sprintf("/projects?group_id=%d&sort=name&limit=1", team),
		nil,
		200,
	)
	if !strings.Contains(web, "Group Alpha") ||
		strings.Contains(web, "Group Beta") ||
		!strings.Contains(web, "Next") {
		t.Fatal("web pagination differs from API")
	}
	f.api(t, "PUT", move, map[string]int64{"group_id": other}, 200)
	f.api(t, "PUT", move, map[string]any{"group_id": nil}, 200)
	f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/project-groups/%d", team),
		map[string]string{"name": "Renamed"},
		200,
	)
	f.api(t, "DELETE", fmt.Sprintf("/api/v1/project-groups/%d", team), nil, 204)
	f.api(t, "DELETE", fmt.Sprintf("/api/v1/project-groups/%d", team), nil, 404)
	for _, id := range []int64{first, second, third} {
		var project struct {
			ID      int64
			GroupID *int64 `json:"group_id"`
		}
		decodeLifecycleTest(
			t,
			f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", id), nil, 200),
			&project,
		)
		if project.ID != id || project.GroupID != nil {
			t.Fatal("deletion did not preserve Ungrouped projects")
		}
	}
	var audited int
	err := f.h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_log WHERE action IN ('create_project_group', 'update_project_group', 'delete_project_group', 'move_project_group')").
		Scan(&audited)
	if err != nil || audited != 8 {
		t.Fatalf("successful group audit count=%d %v", audited, err)
	}
}

func TestProjectGroupsAuthorizationE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	owner := f.token
	visible, private, empty := createGroupE2E(
		t,
		f,
		"Visible",
	), createGroupE2E(
		t,
		f,
		"Private",
	), createGroupE2E(
		t,
		f,
		"Empty",
	)
	first := createGroupedProjectE2E(t, f, "Visible project", &visible)
	createGroupedProjectE2E(t, f, "Hidden sibling", &visible)
	hidden := createGroupedProjectE2E(t, f, "Private project", &private)
	for _, global := range []string{"viewer", "deployer", "maintainer"} {
		for _, membership := range []string{"viewer", "deployer", "maintainer", "admin"} {
			t.Run(global+"/"+membership, func(t *testing.T) {
				user := seedAPIUser(
					t,
					f.h.repo,
					global+membership+"@groups.test",
					global,
				)
				if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: first, UserID: user.ID, Role: membership}); err != nil {
					t.Fatal(err)
				}
				_, f.token = seedAPIToken(t, f.h.repo, user.ID)
				f.session = memberRoleSession(t, f.h, user.ID)
				allowed := global == "maintainer" &&
					slices.Contains([]string{"maintainer", "admin"}, membership)
				assertProjectGroupManagementAccess(
					t, f, allowed, global == "maintainer",
				)
				assertProjectGroupMemberPermissions(
					t,
					f,
					first,
					visible,
					hidden,
					allowed,
					[]int64{private, empty, 999999},
				)
			})
		}
	}
	f.token = owner
	for _, ceiling := range []string{"viewer", "deployer", "maintainer", "admin"} {
		token := mintProjectToken(t, f, ceiling, first)
		f.token = token.ID
		var groups groupPage
		decodeLifecycleTest(
			t,
			f.api(t, "GET", "/api/v1/project-groups?limit=1", nil, 200),
			&groups,
		)
		if groups.Total != 1 || groups.Items[0].ProjectCount != 1 {
			t.Fatal("scoped token leaked group counts")
		}
		f.api(
			t,
			"POST",
			"/api/v1/project-groups",
			map[string]string{"name": "Forbidden"},
			403,
		)
		status := 403
		if ceiling == "maintainer" || ceiling == "admin" {
			status = 200
		}
		f.api(
			t,
			"PUT",
			fmt.Sprintf("/api/v1/projects/%d/group", first),
			map[string]int64{"group_id": visible},
			status,
		)
		f.token = owner
	}
}

func TestProjectGroupsWebWritesE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	// A form token must match the retained session.
	session, err := f.h.repo.Queries.GetSession(
		t.Context(),
		db.GetSessionParams{ID: f.session},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.csrf = session.CsrfToken
	f.web(t, "POST", "/project-groups", url.Values{"name": {"Web team"}}, 303)
	var groups groupPage
	decodeLifecycleTest(
		t,
		f.api(t, "GET", "/api/v1/project-groups", nil, 200),
		&groups,
	)
	id := groups.Items[0].ID
	f.web(
		t,
		"POST",
		"/project-groups",
		url.Values{"id": {fmt.Sprint(id)}, "name": {"Web renamed"}},
		303,
	)
	project := createGroupedProjectE2E(t, f, "Web project", nil)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/group", project),
		url.Values{"group_id": {fmt.Sprint(id)}},
		303,
	)
	f.web(t, "POST", fmt.Sprintf("/project-groups/%d/delete", id), nil, 303)
	user := seedAPIUser(t, f.h.repo, "viewer-web@groups.test", "viewer")
	f.session = memberRoleSession(t, f.h, user.ID)
	session, err = f.h.repo.Queries.GetSession(
		t.Context(),
		db.GetSessionParams{ID: f.session},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.csrf = session.CsrfToken
	f.web(t, "POST", "/project-groups", url.Values{"name": {"Denied"}}, 403)
	f.web(t, "POST", fmt.Sprintf("/projects/%d/group", project), nil, 403)
}

func TestProjectGroupsCatalogBoundariesAPIWebE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	var project struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", "/api/v1/projects", map[string]string{
			"name": "Örion", "description": "Äccent description",
		}, 201),
		&project,
	)
	for _, search := range []string{"Örion", "Äccent description"} {
		query := "?q=" + url.QueryEscape(search)
		var page struct {
			Items []struct{ ID int64 }
			Total int64
		}
		decodeLifecycleTest(
			t,
			f.api(t, "GET", "/api/v1/projects"+query, nil, 200),
			&page,
		)
		if page.Total != 1 || len(page.Items) != 1 ||
			page.Items[0].ID != project.ID {
			t.Fatalf("exact Unicode search: %+v", page)
		}
		if !strings.Contains(
			f.web(t, "GET", "/projects"+query, nil, 200),
			"Örion",
		) {
			t.Fatal("web exact Unicode search lost project")
		}
	}
	for _, groupID := range []string{"2147483648", "9223372036854775807"} {
		var page struct{ Total int64 }
		decodeLifecycleTest(
			t,
			f.api(t, "GET", "/api/v1/projects?group_id="+groupID, nil, 200),
			&page,
		)
		if page.Total != 0 {
			t.Fatal("unknown group filter returned projects")
		}
		f.web(t, "GET", "/projects?group_id="+groupID, nil, 200)
	}
}

func assertProjectGroupManagementAccess(
	t *testing.T,
	f *artifactE2E,
	allowed, canManage bool,
) {
	web := f.web(t, "GET", "/projects", nil, 200)
	if strings.Contains(web, "Manage Groups") != canManage {
		t.Fatal("management link ignores global role")
	}
	if !canManage {
		f.web(t, "GET", "/project-groups", nil, http.StatusForbidden)
		return
	}
	management := f.web(t, "GET", "/project-groups", nil, 200)
	if strings.Contains(management, "data-project-move") != allowed {
		t.Fatal("management assignments ignore effective project role")
	}
	for _, hidden := range []string{"Hidden sibling", ">Private<", ">Empty<", "data-group-create", "data-group-rename", "data-group-delete"} {
		if strings.Contains(management, hidden) {
			t.Fatalf(
				"management page disclosed unauthorized content: %s",
				hidden,
			)
		}
	}
}

func assertProjectGroupMemberPermissions(
	t *testing.T,
	f *artifactE2E,
	first, visible, hidden int64,
	allowed bool,
	unavailable []int64,
) {
	var groups groupPage
	decodeLifecycleTest(
		t,
		f.api(t, "GET", "/api/v1/project-groups", nil, 200),
		&groups,
	)
	if groups.Total != 1 || len(groups.Items) != 1 ||
		groups.Items[0].ID != visible ||
		groups.Items[0].ProjectCount != 1 {
		t.Fatalf("group disclosure: %+v", groups)
	}
	web := f.web(t, "GET", "/projects", nil, 200)
	if strings.Contains(web, "Hidden sibling") ||
		strings.Contains(web, ">Private<") ||
		strings.Contains(web, ">Empty<") {
		t.Fatal("web disclosed inaccessible groups or controls")
	}
	if strings.Contains(web, "data-project-move") {
		t.Fatal("project browsing exposed assignment controls")
	}
	status := 403
	if allowed {
		status = 200
	}
	path := fmt.Sprintf("/api/v1/projects/%d/group", first)
	f.api(
		t,
		"PUT",
		path,
		map[string]int64{"group_id": visible},
		status,
	)
	webStatus := http.StatusForbidden
	if allowed {
		webStatus = http.StatusSeeOther
	}
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/group", first),
		url.Values{"group_id": {fmt.Sprint(visible)}},
		webStatus,
	)
	f.web(
		t,
		"POST",
		"/project-groups",
		url.Values{"name": {"Forbidden"}},
		http.StatusForbidden,
	)
	f.api(
		t,
		"POST",
		"/api/v1/project-groups",
		map[string]string{"name": "Forbidden"},
		403,
	)
	f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/project-groups/%d", visible),
		map[string]string{"name": "Forbidden"},
		403,
	)
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/project-groups/%d", visible),
		nil,
		403,
	)
	if allowed {
		for _, destination := range unavailable {
			f.api(
				t,
				"PUT",
				path,
				map[string]int64{"group_id": destination},
				404,
			)
		}
	}
	f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/projects/%d/group", hidden),
		map[string]int64{"group_id": visible},
		403,
	)
}
