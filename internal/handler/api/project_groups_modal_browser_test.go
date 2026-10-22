//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestProjectGroupsModalBrowserE2E(t *testing.T) {
	f := newProjectGroupsE2E(t)
	source := createGroupE2E(t, f, "Modal source")
	destination := createGroupE2E(t, f, "Modal destination")
	first := createGroupedProjectE2E(t, f, "Modal Alpha", &source)
	createGroupedProjectE2E(t, f, "Modal Beta", &source)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 1280} {
		height := 900
		if width < 768 {
			height = 600
		}
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+"/projects?q=Modal&limit=1")
		b.evaluate(
			t,
			`document.querySelector('[data-manage-groups]').click(); true`,
		)
		waitProjectGroupModal(
			t,
			b,
			`document.querySelector('#project-edit-dialog[open] [data-group-dialog="true"]') && document.querySelector('[data-group-workspace-cancel]')`,
		)
		b.wait(
			t,
			`document.querySelector('#project-edit-dialog > .modal-box').getAnimations().every(animation => animation.playState !== 'running')`,
		)
		b.evaluate(
			t,
			`window.groupModalFrame = document.querySelector('#project-edit-dialog > .modal-box').getBoundingClientRect().toJSON(); true`,
		)
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-group-manage="%d"]').click(); true`,
				destination,
			),
		)
		waitProjectGroupModal(
			t,
			b,
			`document.querySelector('[data-active-group]').textContent === 'Modal destination' && document.querySelectorAll('[data-project-assignment]').length === 0`,
		)
		assertProjectGroupModalFrame(t, b)
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-group-manage="%d"]').click(); true`,
				source,
			),
		)
		waitProjectGroupModal(
			t,
			b,
			`document.querySelector('[data-active-group]').textContent === 'Modal source'`,
		)
		assertProjectGroupModalFrame(t, b)
		if width < 768 {
			if string(b.evaluate(t, `(() => {
 const body = document.querySelector('[data-group-workspace-body]');
 body.scrollTop = body.scrollHeight;
 return body.scrollTop > 0;
})()`)) != "true" {
				t.Fatal("long modal content does not scroll independently")
			}
			assertProjectGroupModalFrame(t, b)
			b.evaluate(
				t,
				`document.querySelector('[data-group-workspace-body]').scrollTop = 0; true`,
			)
		}
		b.evaluate(
			t,
			`var form = document.querySelector('#project-group-filters'); form.querySelector('[name=limit]').value = '1'; form.requestSubmit(); true`,
		)
		waitProjectGroupModal(
			t,
			b,
			`document.querySelectorAll('[data-project-assignment]').length === 1 && document.querySelector('[data-project-assignment]').textContent.includes('Modal Alpha')`,
		)
		b.evaluate(
			t,
			`[...document.querySelectorAll('#project-edit-content nav[aria-label="Project pagination"] a')].find(a => a.textContent === 'Next').click(); true`,
		)
		waitProjectGroupModal(
			t,
			b,
			`document.querySelector('[data-project-assignment]').textContent.includes('Modal Beta')`,
		)
		if string(
			b.evaluate(
				t,
				`location.pathname === '/projects' && location.search === '?q=Modal&limit=1' && document.querySelector('#project-edit-dialog').getAttribute('aria-label') === 'Manage Groups' && document.documentElement.scrollWidth <= innerWidth && document.querySelectorAll('#project-filters').length === 1`,
			),
		) != "true" {
			t.Fatal(
				"modal lost the background URL, label, or responsive layout",
			)
		}
		b.screenshot(t, fmt.Sprintf("project-groups-modal-%d", width))
		assertProjectGroupModalFrame(t, b)
		b.evaluate(
			t,
			`[...document.querySelectorAll('#project-edit-content a')].find(a => a.textContent === 'Clear').click(); true`,
		)
		waitProjectGroupModal(
			t,
			b,
			`document.querySelector('[data-active-group]').textContent === 'All projects' && document.querySelectorAll('[data-project-assignment]').length === 2`,
		)
		b.evaluate(
			t,
			`document.querySelector('[data-group-workspace-cancel]').click(); true`,
		)
		waitProjectGroupModal(
			t,
			b,
			`!document.querySelector('#project-edit-dialog').open && document.activeElement.matches('[data-manage-groups]')`,
		)
	}
	assertProjectGroupModalCRUD(t, b)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-group-manage="%d"]').click(); true`,
			source,
		),
	)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelector('[data-active-group]').textContent === 'Modal source' && document.querySelectorAll('[data-project-move]').length === 2`,
	)
	b.evaluate(t, clickWorkspaceSelectAll)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelectorAll('[data-project-move]:checked').length === 2`,
	)
	b.evaluate(t, `document.querySelector('[x-ref=moveButton]').click(); true`)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-project-bulk-move] input[value="%d"]').click(); true`,
			destination,
		),
	)
	waitProjectGroupModal(
		t,
		b,
		fmt.Sprintf(
			`Alpine.$data(document.querySelector('[data-group-workspace]')).destination === '%d'`,
			destination,
		),
	)
	b.evaluate(
		t,
		`document.querySelector('[data-project-bulk-move]').requestSubmit(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelector('#project-edit-dialog').open && document.querySelectorAll('[data-project-assignment]').length === 0 && !document.querySelector('[x-ref=moveDialog]')`,
	)
	b.evaluate(
		t,
		`document.querySelector('[data-group-workspace-cancel]').click(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		fmt.Sprintf(
			`!document.querySelector('#project-edit-dialog').open && document.querySelector('[data-group-project="%d"]').closest('details').querySelector('[data-project-group-name]').textContent === 'Modal destination'`,
			first,
		),
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
		t.Fatal("modal moves disagree with the public API")
	}
}

func assertProjectGroupModalFrame(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const box = document.querySelector('#project-edit-dialog > .modal-box').getBoundingClientRect();
 const initial = window.groupModalFrame;
 const body = document.querySelector('[data-group-workspace-body]').getBoundingClientRect();
 const controls = [...document.querySelectorAll('[data-group-workspace-cancel], [data-group-create-button]')];
 return ['x', 'y', 'width', 'height'].every(key => Math.abs(box[key] - initial[key]) < 1) &&
   controls.every(control => {
     const rect = control.getBoundingClientRect();
     return control.closest('.page-header') && rect.top >= box.top &&
       rect.bottom <= body.top && rect.left >= box.left && rect.right <= box.right;
   });
})()`)) != "true" {
		t.Fatal(
			"group selection resized the modal or displaced its top actions",
		)
	}
}

func assertProjectGroupModalCRUD(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.evaluate(
		t,
		`document.querySelector('[data-manage-groups]').click(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelector('[data-group-create-button]')`,
	)
	b.evaluate(
		t,
		`document.querySelector('[data-group-create-button]').click(); var form = document.querySelector('[data-group-create]'); form.querySelector('[name=name]').value = 'Modal source'; form.requestSubmit(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelector('[data-group-create] [data-group-error]').textContent.includes('already exists')`,
	)
	b.evaluate(
		t,
		`var form = document.querySelector('[data-group-create]'); form.querySelector('[name=name]').value = 'Modal created'; form.requestSubmit(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelector('[data-active-group]').textContent === 'Modal created' && document.querySelector('#project-edit-dialog').open`,
	)
	b.evaluate(
		t,
		`document.querySelector('[x-ref=groupActions] summary').click(); document.querySelector('[data-group-rename-button]').click(); var form = document.querySelector('[data-group-rename]'); form.querySelector('[name=name]').value = 'Modal renamed'; form.requestSubmit(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		`document.querySelector('[data-active-group]').textContent === 'Modal renamed' && !document.querySelector('[data-group-manage][title="Modal created"]')`,
	)
	b.evaluate(
		t,
		`document.querySelector('[x-ref=groupActions] summary').click(); document.querySelector('[data-group-delete-button]').click(); document.querySelector('[data-group-delete]').requestSubmit(); true`,
	)
	waitProjectGroupModal(
		t,
		b,
		`!document.querySelector('[data-group-manage][title="Modal renamed"]') && document.querySelector('[data-active-group]').textContent === 'All projects' && location.pathname === '/projects'`,
	)
}

func waitProjectGroupModal(t *testing.T, b *packageBrowser, condition string) {
	t.Helper()
	b.wait(
		t,
		"("+condition+") && !document.querySelector('#project-edit-content .htmx-added')",
	)
}
