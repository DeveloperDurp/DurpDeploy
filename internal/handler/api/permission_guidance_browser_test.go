//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/roles"
)

func TestPermissionGuidanceBrowserE2E(t *testing.T) {
	f := newPermissionGuidanceFixture(t)
	b := startPackageBrowser(t)
	for _, pair := range [][2]string{
		{"deployer", "admin"}, {"maintainer", "viewer"},
		{"deployer", "viewer"}, {"viewer", "admin"},
		{"maintainer", "admin"}, {"admin", ""},
	} {
		actor := f.actor(t, pair[0], pair[1])
		for _, width := range []int{390, 1280} {
			t.Run(
				fmt.Sprintf("%s/%s/%d", pair[0], pair[1], width),
				func(t *testing.T) {
					b.setBackTestSession(t, f.server.URL, actor.session)
					b.call(
						t,
						"Emulation.setDeviceMetricsOverride",
						map[string]any{
							"width": width, "height": 900, "deviceScaleFactor": 1,
							"mobile": false,
						},
						&struct{}{},
					)
					checkPermissionGuidanceBrowser(t, b, f, actor, width)
				},
			)
		}
		if pair == [2]string{"maintainer", "viewer"} {
			f.request(
				t,
				"PUT",
				f.apiPath(fmt.Sprintf("/members/%d", actor.user.ID)),
				`{"role":"maintainer"}`,
				f.adminToken,
				"",
				false,
				200,
			)
			b.navigateBackTest(
				t,
				fmt.Sprintf("%s/projects/%d", f.server.URL, f.project.ID),
			)
			b.wait(
				t,
				`document.querySelector('#project-detail .page-header a[hx-get]') &&
 document.querySelector('[data-project-permissions]').innerText.includes('Effective project role: maintainer')`,
			)
			f.request(
				t,
				"PUT",
				f.apiPath(""),
				`{"name":"Recovered guidance project"}`,
				actor.token,
				"",
				false,
				200,
			)
		}
	}
}

func checkPermissionGuidanceBrowser(
	t *testing.T,
	b *packageBrowser,
	f permissionGuidanceFixture,
	actor permissionGuidanceActor,
	width int,
) {
	t.Helper()
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	effective := actor.user.Role
	if effective != "admin" &&
		roles.Rank(actor.member) < roles.Rank(effective) {
		effective = actor.member
	}
	b.navigateBackTest(t, f.server.URL+path)
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('[data-project-permissions]')?.innerText.includes(%q)`,
			"Effective project role: "+effective,
		),
	)
	if string(
		b.evaluate(t, `document.documentElement.scrollWidth <= innerWidth`),
	) != "true" {
		t.Fatal("permission summary overflows the viewport")
	}
	b.evaluate(
		t,
		`document.querySelector('button[aria-controls="project-menu-dialog"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-menu-dialog').matches(':modal')`,
	)
	if !roles.AtLeast(effective, "maintainer") {
		b.wait(
			t,
			`document.querySelector('#project-menu-dialog').innerText.includes('Settings are read-only')`,
		)
		checkReadOnlyMenuNote(t, b, actor, width)
	}
	b.evaluate(
		t,
		`document.querySelector('#project-menu-dialog button[aria-label="Close project menu"]').click(); true`,
	)
	b.wait(t, `!document.querySelector('#project-menu-dialog').open`)
	if !roles.AtLeast(actor.user.Role, "maintainer") {
		checkTokenEligibilityMenu(t, b)
	}
	if roles.AtLeast(effective, "maintainer") {
		return
	}
	checkReadOnlyGuidanceBrowser(t, b, f, actor, path, width)
}

func checkReadOnlyMenuNote(
	t *testing.T,
	b *packageBrowser,
	actor permissionGuidanceActor,
	width int,
) {
	t.Helper()
	const note = `[...document.querySelectorAll('#project-menu-dialog p')]
 .find(e => e.textContent.startsWith('Settings are read-only'))`
	b.evaluate(
		t,
		`Promise.all(document.querySelector('.project-menu-panel')
 .getAnimations().map(a => a.finished)).then(() => true)`,
	)
	var point struct{ X, Y float64 }
	raw := b.evaluate(t, `(() => {
 const note = `+note+`;
 note.scrollIntoView({block:'center'});
 const box = note.getBoundingClientRect();
 return {X:box.left + box.width / 2, Y:box.top + box.height / 2};
})()`)
	if err := json.Unmarshal(raw, &point); err != nil {
		t.Fatal(err)
	}
	b.call(t, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": point.X, "y": point.Y,
	}, &struct{}{})
	b.wait(t, `(() => {
 const note = `+note+`;
 return note.matches(':hover') && !note.closest('.menu') &&
  note.tabIndex < 0 && !note.querySelector('a, button, [tabindex]') &&
  getComputedStyle(note).backgroundColor === 'rgba(0, 0, 0, 0)' &&
  getComputedStyle(note).cursor !== 'pointer';
})()`)
	b.screenshot(t, fmt.Sprintf("read-only-menu-%s-%s-%d",
		actor.user.Role, actor.member, width))
}

func checkReadOnlyGuidanceBrowser(
	t *testing.T,
	b *packageBrowser,
	f permissionGuidanceFixture,
	actor permissionGuidanceActor,
	path string,
	width int,
) {
	t.Helper()
	for _, suffix := range []string{"/steps-page", "/releases"} {
		b.navigateBackTest(t, f.server.URL+path+suffix)
		b.wait(
			t,
			`document.querySelector('#page-content').innerText.includes('Read-only')`,
		)
		if string(
			b.evaluate(
				t,
				`document.querySelectorAll('#page-content [hx-put], #page-content [hx-post], #page-content [hx-delete], #page-content [data-step-action]').length === 0`,
			),
		) != "true" {
			t.Fatal("read-only guidance exposed authoring controls")
		}
	}
	if actor.user.Role == "maintainer" || actor.member == "viewer" {
		b.evaluate(
			t,
			fmt.Sprintf(`document.querySelector('a[href=%q]').click(); true`,
				path+"/edit#project-members"),
		)
		b.wait(
			t,
			`document.querySelector('#project-members') && !document.querySelector('[data-project-form]')`,
		)
		b.wait(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-member-id="%d"]')?.innerText.includes(%q)`,
				actor.user.ID,
				actor.member,
			),
		)
		if string(
			b.evaluate(
				t,
				`document.querySelector('#project-members').querySelectorAll('form, button').length === 0`,
			),
		) != "true" {
			t.Fatal("restricted member recovery permits role changes")
		}
	}
	if actor.user.Role == "maintainer" ||
		width == 390 && actor.member == "admin" {
		b.screenshot(t, fmt.Sprintf("permission-guidance-%s-%s-%d",
			actor.user.Role, actor.member, width))
	}
}

func checkTokenEligibilityMenu(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.evaluate(t, `(() => {
 const mobile=document.querySelector('[data-mobile-nav]');
 if(mobile?.getClientRects().length) mobile.querySelector('summary').click();
 const account=[...document.querySelectorAll('[data-account-menu]')].find(e=>e.getClientRects().length);
 account.querySelector('summary').click();
 return true;
})()`)
	b.wait(t, `(() => {
 const account=[...document.querySelectorAll('[data-account-menu][open]')].find(e=>e.getClientRects().length);
 return account?.innerText.includes('API tokens require a global Maintainer or Admin role.') &&
 !account.querySelector('a[href="/settings/tokens"]');
})()`)
}
