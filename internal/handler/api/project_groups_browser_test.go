//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

const (
	clickWorkspaceSelectAll = `document.querySelector('[data-project-select-all]').click(); true`
	clickProjectGroupNext   = `document.querySelector('nav[aria-label="Project pagination"] a').click(); true`
)

func TestProjectGroupsWorkspaceSelectionBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	source := createGroupE2E(t, f, "Workspace source")
	destination := createGroupE2E(t, f, "Workspace destination")
	first := createGroupedProjectE2E(t, f, "Workspace Alpha", &source)
	createGroupedProjectE2E(t, f, "Workspace Beta", &source)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	base := fmt.Sprintf("%s/project-groups?group_id=%d", f.baseURL, source)
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, base)
		assertWorkspaceSelection(t, b, first)
		b.screenshot(t, fmt.Sprintf("group-workspace-selected-%d", width))
		b.navigateBackTest(t, base+"&q=Alpha&limit=1")
		b.evaluate(
			t,
			clickWorkspaceSelectAll,
		)
		b.wait(
			t,
			`document.querySelectorAll('[data-project-move]:checked').length === 1`,
		)
		b.navigateBackTest(t, base+"&limit=1&offset=1")
		b.wait(
			t,
			`document.querySelector('[data-group-workspace]')._x_dataStack`,
		)
		if string(
			b.evaluate(
				t,
				`document.querySelectorAll('[data-project-move]:checked').length === 0 && !document.querySelector('[data-project-select-all]').checked && document.documentElement.scrollWidth <= innerWidth`,
			),
		) != "true" {
			t.Fatal("selection escaped its search, page, or mobile viewport")
		}
	}
	b.navigateBackTest(t, base)
	b.evaluate(
		t,
		clickWorkspaceSelectAll,
	)
	openWorkspaceMove(t, b, destination)
	b.wait(
		t,
		`document.querySelectorAll('[data-project-assignment]').length === 0 && !document.querySelector('dialog[open]')`,
	)
	var moved struct{ Total int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/projects?group_id=%d", destination),
			nil,
			200,
		),
		&moved,
	)
	if moved.Total != 2 {
		t.Fatal("bulk browser move disagrees with public API")
	}
}

func assertWorkspaceSelection(t *testing.T, b *packageBrowser, first int64) {
	b.wait(t, `document.querySelector('[data-group-workspace]')._x_dataStack`)
	if string(
		b.evaluate(
			t,
			`getComputedStyle(document.querySelector('[data-project-selection]')).display === 'none' && !document.querySelector('[data-project-filters] select[name=group_id]') && !document.querySelector('[data-project-assignment] select') && !document.querySelector('[data-project-assignment] button')`,
		),
	) != "true" {
		t.Fatal("workspace exposes an empty banner or row move controls")
	}
	b.evaluate(
		t,
		clickWorkspaceSelectAll,
	)
	b.wait(
		t,
		`document.querySelectorAll('[data-project-move]:checked').length === 2 && document.querySelector('[data-project-select-all]').checked`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-project-move][value="%d"]').click(); true`,
			first,
		),
	)
	b.wait(
		t,
		`document.querySelector('[data-project-select-all]').indeterminate && document.querySelectorAll('[data-project-move]:checked').length === 1`,
	)
	b.evaluate(
		t,
		clickWorkspaceSelectAll,
	)
	b.wait(
		t,
		`!document.querySelector('[data-project-select-all]').indeterminate && document.querySelectorAll('[data-project-move]:checked').length === 2`,
	)
	b.evaluate(
		t,
		clickWorkspaceSelectAll,
	)
	b.wait(
		t,
		`document.querySelectorAll('[data-project-move]:checked').length === 0 && getComputedStyle(document.querySelector('[data-project-selection]')).display === 'none'`,
	)
	b.evaluate(
		t,
		clickWorkspaceSelectAll,
	)
}

func openWorkspaceMove(t *testing.T, b *packageBrowser, destination int64) {
	b.evaluate(t, fmt.Sprintf(`(() => {
 document.querySelector('[data-project-selection] .btn-primary').click();
 const form = document.querySelector('[data-project-bulk-move]');
 form.querySelector('input[value="%d"]').click();
 form.requestSubmit();
 return true;
})()`, destination))
}

func TestProjectGroupsWorkspacePartialMoveBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	source := createGroupE2E(t, f, "Partial source")
	destination := createGroupE2E(t, f, "Partial destination")
	first := createGroupedProjectE2E(t, f, "Partial Alpha", &source)
	second := createGroupedProjectE2E(t, f, "Partial Beta", &source)
	visible := createGroupedProjectE2E(
		t,
		f,
		"Destination visibility",
		&destination,
	)
	user := seedAPIUser(t, f.h.repo, "partial-mover@groups.test", "maintainer")
	for _, id := range []int64{first, second, visible} {
		if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: id, UserID: user.ID, Role: "maintainer"}); err != nil {
			t.Fatal(err)
		}
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, memberRoleSession(t, f.h, user.ID))
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/project-groups?group_id=%d&sort=name",
			f.baseURL,
			source,
		),
	)
	b.evaluate(
		t,
		clickWorkspaceSelectAll,
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: second, UserID: user.ID, Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	openWorkspaceMove(t, b, destination)
	b.wait(
		t,
		`document.querySelector('[x-ref=moveError]').textContent.includes('Moved 1 of 2 projects.')`,
	)
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('dialog[open]') !== null && document.querySelectorAll('[data-project-move]:checked').length === 1 && document.querySelector('[data-project-move]:checked').value === '%d'`,
				second,
			),
		),
	) != "true" {
		t.Fatal("partial failure lost the remaining selection")
	}
	for id, group := range map[int64]int64{first: destination, second: source} {
		var project struct {
			GroupID int64 `json:"group_id"`
		}
		decodeLifecycleTest(
			t,
			f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", id), nil, 200),
			&project,
		)
		if project.GroupID != group {
			t.Fatal("bulk move ignored current source permissions")
		}
	}
	var audited int
	var auditedProject int64
	err := f.h.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*), COALESCE(MAX(entity_id), 0) FROM audit_log WHERE user_id = ? AND action = 'move_project_group'",
		user.ID,
	).Scan(&audited, &auditedProject)
	if err != nil || audited != 1 || auditedProject != first {
		t.Fatalf(
			"partial-move audit count=%d project=%d: %v",
			audited,
			auditedProject,
			err,
		)
	}
	b.screenshot(t, "group-workspace-partial-move")
}

func TestProjectGroupsFilterControlsBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "Filter team")
	createGroupedProjectE2E(t, f, "Filter Alpha", &group)
	createGroupedProjectE2E(t, f, "Filter Beta", &group)
	createGroupedProjectE2E(t, f, "Other project", nil)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900,
			"deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		for _, path := range []string{"/projects", "/project-groups"} {
			b.navigateBackTest(t, f.baseURL+path)
			assertProjectFilterVisibility(t, b, width, false)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('[data-project-filters]');
 form.querySelector('[name=q]').value = 'Filter';
 form.querySelector('[name=group_id]').value = %q;
 form.querySelector('[name=sort]').value = 'name_desc';
 form.querySelector('[name=limit]').value = '1';
 form.querySelector('button[type=submit]').click();
 return true;
})()`, fmt.Sprint(group)))
			b.wait(
				t,
				`location.search.includes('limit=1') && document.querySelector('[data-project-total]').textContent.includes('Matching projects: 2.')`,
			)
			assertProjectFilterVisibility(t, b, width, true)
			assertProjectFilterClear(t, b, path, width)
		}
	}
}

func assertProjectFilterVisibility(
	t *testing.T, b *packageBrowser, width int, active bool,
) {
	const toggleFilters = `document.querySelector('[data-project-filter-toggle]').click(); true`
	// Pagination restores CSS classes when the HTMX swap settles.
	b.wait(
		t,
		`!document.querySelector('.htmx-request, .htmx-settling') &&
 document.querySelector('[data-project-filter-toggle]')?.parentElement?._x_dataStack`,
	)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const toggle = document.querySelector('[data-project-filter-toggle]');
 const form = document.querySelector('[data-project-filters]');
 return (getComputedStyle(form).display !== 'none') === %t &&
 (getComputedStyle(toggle).display !== 'none') === %t &&
 !!toggle.querySelector('.badge') === %t &&
 toggle.getAttribute('aria-expanded') === 'false' &&
 document.documentElement.scrollWidth <= innerWidth;
})()`, width >= 768, width < 768, active))) != "true" {
		t.Fatal("filter visibility, active indicator, or mobile layout differs")
	}
	if width < 768 {
		b.evaluate(
			t,
			toggleFilters,
		)
		b.wait(
			t,
			`document.querySelector('[data-project-filter-toggle]').getAttribute('aria-expanded') === 'true' && getComputedStyle(document.querySelector('[data-project-filters]')).display !== 'none'`,
		)
		b.screenshot(
			t,
			fmt.Sprintf("project-filters-open-%d-%t", width, active),
		)
		b.evaluate(
			t,
			toggleFilters,
		)
		b.wait(
			t,
			`document.querySelector('[data-project-filter-toggle]').getAttribute('aria-expanded') === 'false' && getComputedStyle(document.querySelector('[data-project-filters]')).display === 'none'`,
		)
		b.evaluate(
			t,
			toggleFilters,
		)
		b.wait(
			t,
			`getComputedStyle(document.querySelector('[data-project-filters]')).display !== 'none'`,
		)
	}
}

func assertProjectFilterClear(
	t *testing.T, b *packageBrowser, path string, width int,
) {
	b.evaluate(
		t,
		clickProjectGroupNext,
	)
	b.wait(
		t,
		`location.search.includes('offset=1') && [...document.querySelectorAll('nav[aria-label="Project pagination"] a')].some(a => a.textContent === 'Previous')`,
	)
	assertProjectFilterVisibility(t, b, width, true)
	b.evaluate(
		t,
		`document.querySelector('[data-project-filters] a').click(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === %q && location.search === '' && document.querySelector('[data-project-total]').textContent.includes('Matching projects: 3.')`,
			path,
		),
	)
	assertProjectFilterVisibility(t, b, width, false)
	if string(b.evaluate(t, `(() => {
 const form = document.querySelector('[data-project-filters]');
 return form.querySelector('[name=q]').value === '' &&
 form.querySelector('[name=group_id]').value === '' &&
 form.querySelector('[name=sort]').value === 'name' &&
 form.querySelector('[name=limit]').value === '100';
})()`)) != "true" {
		t.Fatal("Clear did not reset every filter and pagination")
	}
	b.screenshot(
		t,
		fmt.Sprintf(
			"project-filters-clear-%s-%d",
			strings.TrimPrefix(path, "/"),
			width,
		),
	)
}

func TestProjectGroupsUnavailableFilterBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "Private filter team")
	createGroupedProjectE2E(t, f, "Private filtered project", &group)
	viewer := seedAPIUser(t, f.h.repo, "filter-viewer@groups.test", "viewer")
	_, viewerToken := seedAPIToken(t, f.h.repo, viewer.ID)
	ownerToken, ownerSession := f.token, f.session
	b := startPackageBrowser(t)
	for _, scenario := range []string{"unknown", "inaccessible", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			groupID := fmt.Sprint(group)
			f.token, f.session = ownerToken, ownerSession
			switch scenario {
			case "unknown":
				groupID = "9223372036854775807"
			case "inaccessible":
				f.token = viewerToken
				f.session = memberRoleSession(t, f.h, viewer.ID)
			case "deleted":
				f.api(
					t,
					"DELETE",
					fmt.Sprintf("/api/v1/project-groups/%d", group),
					nil,
					204,
				)
			}
			b.setBackTestSession(t, f.baseURL, f.session)
			b.navigateBackTest(t, f.baseURL+"/projects?group_id="+groupID)
			if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const select = document.querySelector('[data-project-filters] [name=group_id]');
 return select.value === %q && select.selectedOptions[0].textContent === 'Unavailable group' &&
 new URLSearchParams(location.search).get('group_id') === %q &&
 document.querySelector('[data-project-total]').textContent.includes('Matching projects: 0.') &&
 !document.querySelector('[data-group-project]') && (%t || !document.body.textContent.includes('Private filter team'));
})()`, groupID, groupID, scenario == "unknown"))) != "true" {
				t.Fatal(
					"unavailable group filter lost its state or disclosed a name",
				)
			}
			var listed struct{ Total int64 }
			decodeStepLogTest(
				t,
				f.api(t, "GET", "/api/v1/projects?group_id="+groupID, nil, 200),
				&listed,
			)
			if listed.Total != 0 {
				t.Fatal("unavailable group API returned projects")
			}
			b.screenshot(t, "project-groups-filter-"+scenario)
		})
	}
}

func TestProjectGroupsCreateModalResetsFiltersBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "Modal group")
	createGroupedProjectE2E(t, f, "Modal Alpha", &group)
	createGroupedProjectE2E(t, f, "Modal Beta", &group)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, fmt.Sprintf(
		"%s/projects?q=Modal&group_id=%d&sort=name_desc&limit=1&offset=1",
		f.baseURL, group,
	))
	b.wait(
		t,
		`document.querySelectorAll('[data-group-project]').length === 1 && document.querySelector('[data-group-project]').textContent.includes('Modal Alpha')`,
	)
	b.evaluate(
		t,
		`document.querySelector('.page-header a[href="/projects/new"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog')?.matches(':modal') && document.querySelector('#project-edit-content input[name="name"]')`,
	)
	b.evaluate(
		t,
		`window.groupCreateSentinel = true; document.querySelector('#project-edit-content input[name="name"]').value = 'Created outside filter'; document.querySelector('#project-edit-content button[form]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#project-edit-dialog').open && location.pathname === '/projects' && location.search === '' && document.querySelectorAll('[data-group-project]').length === 3`,
	)
	if string(b.evaluate(t, `(() => {
 const filters = document.querySelector('[data-project-filters]');
 return window.groupCreateSentinel === true &&
 filters.querySelector('[name=q]').value === '' &&
 filters.querySelector('[name=group_id]').value === '' &&
 filters.querySelector('[name=sort]').value === 'name' &&
 filters.querySelector('[name=limit]').value === '100' &&
 [...document.querySelectorAll('[data-group-project]')].some(p => p.textContent.includes('Created outside filter'));
})()`)) != "true" {
		t.Fatal("modal creation left catalog filters inconsistent with the URL")
	}
	b.screenshot(t, "project-groups-create-modal-reset")
	var listed struct {
		Total int64
	}
	decodeStepLogTest(
		t,
		f.api(
			t,
			"GET",
			"/api/v1/projects?q=Created%20outside%20filter&group_id=0",
			nil,
			200,
		),
		&listed,
	)
	if listed.Total != 1 {
		t.Fatal("modal-created project missing from public API")
	}
}

func TestProjectGroupsBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "Browser team")
	first := createGroupedProjectE2E(t, f, "Browser Alpha", &group)
	createGroupedProjectE2E(t, f, "Browser Beta", &group)
	createGroupedProjectE2E(t, f, "Browser Örion Ungrouped", nil)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 1280} {
		assertProjectGroupBrowserWidth(t, f, b, group, width)
		assertProjectGroupManagementWidth(t, f, b, group, width)
	}
	// Use the actual management forms and assignment controls.
	b.navigateBackTest(t, f.baseURL+"/project-groups")
	b.evaluate(
		t,
		`document.querySelector('[data-group-create-button]').click(); const form = document.querySelector('[data-group-create]'); form.querySelector('[name=name]').value = 'Created in browser'; form.requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-active-group]').textContent === 'Created in browser'`,
	)
	b.evaluate(
		t,
		`document.querySelector('[x-ref=groupActions] summary').click(); document.querySelector('[data-group-rename-button]').click(); const form = document.querySelector('[data-group-rename]'); form.querySelector('[name=name]').value = 'Renamed in browser'; form.requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-active-group]').textContent === 'Renamed in browser' && !document.body.textContent.includes('Created in browser')`,
	)
	b.navigateBackTest(t, f.baseURL+"/project-groups")
	b.evaluate(
		t,
		fmt.Sprintf(
			`window.groupMovePending = true; document.querySelector('[data-project-assignment="%d"] [data-project-move]').click(); document.querySelector('[data-project-selection] .btn-primary').click(); const form = document.querySelector('[data-project-bulk-move]'); [...form.querySelectorAll('label')].find(label => label.textContent === 'Renamed in browser').querySelector('input').click(); form.requestSubmit(); true`,
			first,
		),
	)
	b.wait(
		t,
		`!window.groupMovePending && location.pathname === '/project-groups'`,
	)
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-group-project="%d"]').closest('details').querySelector('[data-project-group-name]').textContent === 'Renamed in browser'`,
			first,
		),
	)
	b.screenshot(t, "project-groups-project-moved")
	b.navigateBackTest(t, f.baseURL+"/project-groups")
	b.evaluate(
		t,
		`const link = [...document.querySelectorAll('[data-group-manage]')].find(link => link.title === 'Renamed in browser'); link.click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-active-group]').textContent === 'Renamed in browser'`,
	)
	b.evaluate(
		t,
		`document.querySelector('[x-ref=groupActions] summary').click(); document.querySelector('[data-group-delete-button]').click(); document.querySelector('[data-group-delete]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('[data-group-manage][title="Renamed in browser"]')`,
	)
	b.navigateBackTest(t, f.baseURL+"/projects?group_id=0")
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-group-project="%d"]') && document.querySelectorAll('[data-group-project]').length === 2`,
			first,
		),
	)
	b.screenshot(t, "project-groups-deleted-project-retained")
	// A viewer sees accessible groups without shared management or move controls.
	viewer := seedAPIUser(
		t,
		f.h.repo,
		"groups-browser-viewer@example.test",
		"viewer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: first, UserID: viewer.ID, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	b.setBackTestSession(t, f.baseURL, memberRoleSession(t, f.h, viewer.ID))
	b.navigateBackTest(t, f.baseURL+"/projects")
	if string(
		b.evaluate(
			t,
			`document.querySelectorAll('[data-group-project]').length === 1 && !document.querySelector('[data-project-move]') && !document.querySelector('a[href="/project-groups"]') && !document.body.textContent.includes('Browser team')`,
		),
	) != "true" {
		t.Fatal("viewer UI exposed writes or private group")
	}
	b.screenshot(t, "project-groups-viewer")
}

func assertProjectGroupBrowserWidth(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
	group int64,
	width int,
) {
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, f.baseURL+"/projects")
	if string(
		b.evaluate(
			t,
			`document.querySelectorAll('[data-project-group]').length === 2 && !document.querySelector('[data-project-move]') && document.documentElement.scrollWidth <= innerWidth`,
		),
	) != "true" {
		t.Fatal("groups missing or mobile page overflows")
	}
	columns := 1
	if width >= 768 {
		columns = 2
	}
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const groups = [...document.querySelectorAll('[data-project-group]')];
 return new Set(groups.map(group => getComputedStyle(group).backgroundColor)).size === 2 &&
 groups.every(group => {
   const grid = group.querySelector('[data-project-group-cards]');
   const name = group.querySelector('[data-project-group-name]');
   const project = group.querySelector('[data-resource-card] h2');
   const count = group.querySelectorAll('[data-group-project]').length;
   return getComputedStyle(grid).gridTemplateColumns.split(/\s+/).length === %d &&
     Number.parseFloat(getComputedStyle(name).fontSize) < Number.parseFloat(getComputedStyle(project).fontSize) &&
     name.nextElementSibling.textContent.trim() === count + ' shown' &&
     [...group.querySelectorAll('[data-resource-card]')].every(card => card.scrollWidth <= card.clientWidth);
 });
})()`, columns))) != "true" {
		t.Fatal(
			"group panels lose responsive cards, visual hierarchy, or page counts",
		)
	}
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-project-group="%d"] summary').focus(); true`,
			group,
		),
	)
	for _, event := range []string{"keyDown", "keyUp"} {
		text := ""
		if event == "keyDown" {
			text = "\r"
		}
		b.call(
			t,
			"Input.dispatchKeyEvent",
			map[string]any{
				"type":                  event,
				"key":                   "Enter",
				"code":                  "Enter",
				"windowsVirtualKeyCode": 13, "text": text,
			},
			&struct{}{},
		)
	}
	b.wait(
		t,
		fmt.Sprintf(
			`!document.querySelector('[data-project-group="%d"]').open`,
			group,
		),
	)
	assertProjectGroupCollapsedState(t, b)
	b.screenshot(t, fmt.Sprintf("project-groups-collapsed-%d", width))
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.evaluate(
		t,
		`const filters = document.querySelector('[data-project-filters]'); filters.querySelector('[name=q]').value = 'Browser'; filters.querySelector('[name=sort]').value = 'name'; filters.querySelector('[name=limit]').value = '1'; filters.requestSubmit(); true`,
	)
	b.wait(
		t,
		`location.search.includes('limit=1') && document.querySelectorAll('[data-group-project]').length === 1`,
	)
	if string(
		b.evaluate(
			t,
			`new URLSearchParams(location.search).has('csrf_token')`,
		),
	) != "false" {
		t.Fatal("search exposed a CSRF token in the URL")
	}
	b.wait(
		t,
		`document.querySelector('[data-group-project]').textContent.includes('Browser Alpha')`,
	)
	b.evaluate(
		t,
		clickProjectGroupNext,
	)
	b.wait(
		t,
		`location.search.includes('offset=1') && document.querySelector('[data-group-project]').textContent.includes('Browser Beta')`,
	)
	b.screenshot(t, fmt.Sprintf("project-groups-paginated-%d", width))
	b.navigateBackTest(
		t,
		f.baseURL+"/projects?q="+url.QueryEscape("Browser Örion"),
	)
	if string(
		b.evaluate(
			t,
			`document.querySelectorAll('[data-group-project]').length === 1 && document.querySelector('[data-group-project]').textContent.includes('Browser Örion Ungrouped')`,
		),
	) != "true" {
		t.Fatal("exact Unicode browser search lost project")
	}
	b.screenshot(t, fmt.Sprintf("project-groups-unicode-%d", width))
}

func assertProjectGroupManagementWidth(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
	group int64,
	width int,
) {
	b.navigateBackTest(t, f.baseURL+"/project-groups")
	b.wait(
		t,
		`location.pathname === '/project-groups' && document.querySelectorAll('[data-project-assignment]').length === 3`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`const filters = document.querySelector('[data-project-filters]'); filters.querySelector('[name=q]').value = 'Browser'; filters.querySelector('[name=group_id]').value = '%d'; filters.querySelector('[name=sort]').value = 'name'; filters.querySelector('[name=limit]').value = '1'; filters.requestSubmit(); true`,
			group,
		),
	)
	b.wait(
		t,
		`location.pathname === '/project-groups' && location.search.includes('limit=1') && document.querySelectorAll('[data-project-assignment]').length === 1 && document.querySelector('[data-project-assignment]')?.textContent.includes('Browser Alpha')`,
	)
	b.evaluate(
		t,
		clickProjectGroupNext,
	)
	b.wait(
		t,
		`location.pathname === '/project-groups' && location.search.includes('offset=1') && document.querySelector('[data-project-assignment]')?.textContent.includes('Browser Beta')`,
	)
	if string(
		b.evaluate(
			t,
			`document.documentElement.scrollWidth <= innerWidth && !new URLSearchParams(location.search).has('csrf_token') && document.querySelector('[data-project-assignment] [data-project-move]') !== null`,
		),
	) != "true" {
		t.Fatal("group management clips assignments or loses filter state")
	}
	b.screenshot(t, fmt.Sprintf("project-group-management-%d", width))
}

func assertProjectGroupCollapsedState(t *testing.T, b *packageBrowser) {
	var tree struct {
		Nodes []struct {
			Role       struct{ Value string }
			Name       struct{ Value string }
			Properties []struct {
				Name  string
				Value struct{ Value any }
			}
		}
	}
	b.call(t, "Accessibility.getFullAXTree", map[string]any{}, &tree)
	found := false
	for _, node := range tree.Nodes {
		if strings.Contains(node.Name.Value, "Browser team") {
			for _, property := range node.Properties {
				if property.Name == "expanded" &&
					property.Value.Value == false {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("collapsed group does not expose its expanded state")
	}
}
