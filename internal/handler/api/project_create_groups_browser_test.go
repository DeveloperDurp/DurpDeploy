//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectGroupsCreateBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	group := createGroupE2E(t, f, "New project group")
	f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Duplicate creation"}, 201)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Creation lifecycle"}, 201), &lifecycle)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 900,
				"deviceScaleFactor": 1, "mobile": false,
			}, &struct{}{})
			b.navigateBackTest(t, f.baseURL+"/projects")
			open := func() {
				t.Helper()
				b.wait(
					t,
					`!document.querySelector('.htmx-request, .htmx-settling')`,
				)
				b.evaluate(
					t,
					`document.querySelector('a[href="/projects/new"]').click(); true`,
				)
				b.wait(
					t,
					`document.querySelector('#project-edit-dialog')?.matches(':modal') &&
document.querySelector('#project-edit-content select[name=group_id]')`,
				)
				require.Equal(t, "true", string(b.evaluate(t, `(() => {
 const select = document.querySelector('#project-edit-content [name=group_id]');
 return select.value === '' && select.labels[0].textContent.trim() === 'Group';
})()`)))
			}
			open()
			assertDialogKeyboardBounds(t, b, "#project-edit-dialog")
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#project-form-new');
 form.querySelector('[name=name]').value = 'Duplicate creation';
 form.querySelector('[name=description]').value = 'Keep creation description';
 form.querySelector('[name=group_id]').value = '%d';
 form.querySelector('[name=lifecycle_id]').value = '%d';
 form.requestSubmit(); return true;
})()`, group, lifecycle.ID))
			b.wait(
				t,
				`document.querySelector('#project-edit-content [data-form-error]')
?.textContent.includes('already exists')`,
			)
			require.Equal(t, "true", string(b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#project-form-new');
 const select = form.querySelector('[name=group_id]');
 const box = document.querySelector('#project-edit-dialog .modal-box');
 return select.value === '%d' &&
 form.querySelector('[name=lifecycle_id]').value === '%d' &&
 form.querySelector('[name=description]').value === 'Keep creation description' &&
 box.scrollWidth <= box.clientWidth &&
 document.documentElement.scrollWidth <= innerWidth;
})()`, group, lifecycle.ID))))
			b.screenshot(
				t,
				fmt.Sprintf("project-create-group-validation-%d", width),
			)
			name := fmt.Sprintf("Grouped browser %d", width)
			b.wait(
				t,
				`!document.querySelector('.htmx-request, .htmx-settling')`,
			)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#project-form-new');
 form.querySelector('[name=name]').value = %q;
 form.requestSubmit(); return true;
})()`, name))
			b.wait(t, `!document.querySelector('#project-edit-dialog').open`)
			created := projectCreatedByName(t, f, name)
			require.Equal(t, &group, created.GroupID)
			require.Equal(t, &lifecycle.ID, created.LifecycleID)
			require.Equal(t, "Keep creation description", created.Description)
			b.screenshot(t, fmt.Sprintf("project-create-group-saved-%d", width))

			open()
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#project-form-new');
 form.querySelector('[name=name]').value = 'Cancelled creation';
 form.querySelector('[name=group_id]').value = '%d';
 document.querySelector('#project-edit-content a[href="/projects"]').click();
 return true;
})()`, group))
			b.wait(t, `!document.querySelector('#project-edit-dialog').open &&
!document.querySelector('#project-edit-content [data-project-form]')`)
			var cancelled struct{ Total int64 }
			decodeLifecycleTest(t, f.api(
				t,
				"GET",
				"/api/v1/projects?q=Cancelled%20creation",
				nil,
				200,
			), &cancelled)
			require.Zero(t, cancelled.Total)

			open()
			ungrouped := fmt.Sprintf("Ungrouped browser %d", width)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#project-form-new');
 form.querySelector('[name=name]').value = %q;
 form.requestSubmit(); return true;
})()`, ungrouped))
			b.wait(t, `!document.querySelector('#project-edit-dialog').open`)
			require.Nil(t, projectCreatedByName(t, f, ungrouped).GroupID)

			b.navigateBackTest(t, f.baseURL+"/projects/new")
			b.wait(
				t,
				`document.querySelector('[name=group_id]') && window.htmx`,
			)
			standalone := fmt.Sprintf("Standalone browser %d", width)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#project-form-new');
 form.querySelector('[name=name]').value = %q;
 form.querySelector('[name=group_id]').value = '%d';
 form.requestSubmit(); return true;
})()`, standalone, group))
			b.waitBackTestURL(t, f.baseURL+"/projects")
			require.Equal(
				t,
				&group,
				projectCreatedByName(t, f, standalone).GroupID,
			)
		})
	}
}

func projectCreatedByName(
	t *testing.T,
	f *artifactE2E,
	name string,
) projectCreationResult {
	t.Helper()
	var page struct{ Items []projectCreationResult }
	decodeLifecycleTest(t, f.api(t, "GET",
		"/api/v1/projects?q="+url.QueryEscape(name), nil, 200), &page)
	require.Len(t, page.Items, 1)
	require.Equal(t, name, page.Items[0].Name)
	return page.Items[0]
}
