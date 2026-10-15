//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestProjectMemberRolesBrowserE2E(t *testing.T) {
	h := newAPIHarness(t)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Month|cron.Dom|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	project := seedProject(t, h.repo)
	target := seedAPIUser(t, h.repo, "target@member-browser.test", "deployer")
	if err := h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID, UserID: target.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	for _, actor := range []memberRoleActor{
		{"global-admin", "admin", "", true},
		{"project-admin", "maintainer", "admin", true},
		{"project-deployer", "deployer", "deployer", false},
		{"viewer-project-admin", "viewer", "admin", false},
		{"outsider", "deployer", "", false},
	} {
		t.Run(actor.name, func(t *testing.T) {
			checkMemberRoleBrowserActor(t, b, h, srv.URL,
				project.ID, target.ID, actor)
		})
	}
}

func checkMemberRoleBrowserActor(
	t *testing.T, b *packageBrowser, h *harness, base string,
	projectID, targetID int64, actor memberRoleActor,
) {
	t.Helper()
	path := fmt.Sprintf("/projects/%d", projectID)
	row := fmt.Sprintf(`[data-member-id="%d"]`, targetID)
	user := seedAPIUser(t, h.repo, actor.name+"@browser.test", actor.global)
	if actor.project != "" {
		if err := h.repo.Queries.AddProjectMember(t.Context(),
			db.AddProjectMemberParams{
				ProjectID: projectID, UserID: user.ID, Role: actor.project,
			}); err != nil {
			t.Fatal(err)
		}
	}
	b.setBackTestSession(t, base, memberRoleSession(t, h, user.ID))
	b.call(t, "Page.navigate", map[string]string{
		"url": base + path + "/edit",
	}, &struct{}{})
	b.wait(
		t,
		`document.readyState === 'complete' && document.querySelector('h1')`,
	)
	if actor.allowed || actor.project != "" {
		b.waitBackTestURL(t, base+path+"/edit")
	}
	if !actor.allowed {
		b.assertDeniedMemberRole(t, path, targetID, actor.name)
		return
	}
	if actor.global == "admin" {
		b.navigateBackTest(t, base+path)
		b.evaluate(
			t,
			`document.querySelector('#project-detail .page-header a[hx-get]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#project-edit-dialog')?.matches(':modal') && document.querySelector('#project-members')`,
		)
	}
	b.evaluate(
		t,
		`document.querySelector('[data-project-form] input[name=name]').value = 'unsaved project'; true`,
	)
	for _, width := range []int{1280, 375} {
		b.checkMemberRoleWidth(
			t,
			row,
			actor.name,
			width,
			actor.global == "admin",
		)
	}
	if actor.global != "admin" {
		b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector('[data-member-id="%d"]');
 row.querySelector('button').click();
 row.querySelector('select').value = 'deployer';
 row.querySelector('button[type=submit]').click();
 return true;
})()`, user.ID))
		b.wait(t, `!document.querySelector('#project-members form')`)
	}
}

func (b *packageBrowser) assertDeniedMemberRole(
	t *testing.T, path string, targetID int64, name string,
) {
	t.Helper()
	if string(b.evaluate(t,
		`!document.querySelector('#project-members form[hx-put]')`,
	)) != "true" {
		t.Fatal("unauthorized user sees role edit controls")
	}
	// A forged request from the browser must also be denied.
	if string(b.evaluate(t, fmt.Sprintf(`(async () => {
 const response = await fetch(%q, {method:'PUT', headers:{
 'Content-Type':'application/x-www-form-urlencoded',
 'X-CSRF-Token':'member-role-csrf'}, body:'role=admin'});
 return response.status === 403;
})()`, path+"/members/"+fmt.Sprint(targetID)))) != "true" {
		t.Fatal("browser role update bypassed authorization")
	}
	b.screenshot(t, "members-denied-"+name)
}

func (b *packageBrowser) checkMemberRoleWidth(
	t *testing.T, row, name string, width int, modal bool,
) {
	t.Helper()
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.wait(t, `!document.querySelector('.htmx-request, .htmx-settling')`)
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('button').click(); true`,
		row,
	))
	b.wait(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('select').getClientRects().length > 0`,
		row,
	))
	if string(b.evaluate(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('select').value === 'deployer'`,
		row,
	))) != "true" {
		t.Fatal("editor did not select the current role")
	}
	b.screenshot(t, fmt.Sprintf("members-edit-%s-%d", name, width))
	b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector(%q);
 row.querySelector('select').value = 'admin';
 [...row.querySelectorAll('button')].find(b => b.textContent === 'Cancel').click();
 row.querySelector('button').click();
 return true;
})()`, row))
	if string(b.evaluate(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('select').value === 'deployer'`,
		row,
	))) != "true" {
		t.Fatal("Cancel retained the discarded selection")
	}
	for _, role := range []string{"viewer", "maintainer", "admin", "deployer"} {
		b.saveMemberRole(t, row, role, modal)
	}
	// Invalid input must keep the editor and unsaved settings.
	b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector(%q);
 row.querySelector('select').add(new Option('invalid', 'invalid', false, true));
 row.querySelector('button[type=submit]').click();
 return true;
})()`, row))
	b.wait(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('[role=alert]')?.textContent.includes('Role must be viewer, deployer, maintainer, or admin') && document.querySelector(%q).querySelector('[role=alert]').getClientRects().length > 0`,
		row,
		row,
	))
	b.evaluate(t, fmt.Sprintf(
		`[...document.querySelector(%q).querySelectorAll('button')].find(b => b.textContent === 'Cancel').click(); true`,
		row,
	))
}

func (b *packageBrowser) saveMemberRole(
	t *testing.T, row, role string, modal bool,
) {
	t.Helper()
	b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector(%q);
 row.querySelector('select').value = %q;
 row.querySelector('button[type=submit]').click();
 return true;
})()`, row, role))
	b.wait(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('[data-member-role]').textContent === %q && !document.querySelector('.htmx-request, .htmx-settling')`,
		row,
		role,
	))
	if string(b.evaluate(
		t,
		`[...document.querySelectorAll('#project-members tbody select')].every(s => !s.getClientRects().length)`,
	)) != "true" {
		t.Fatalf("closed role editor remains visible: %s", b.evaluate(
			t,
			`[...document.querySelectorAll('#project-members tbody select')].map(s => ({html:s.parentElement.outerHTML, display:getComputedStyle(s.parentElement).display}))`,
		))
	}
	b.wait(
		t,
		`document.querySelector('#project-members [role=status]')?.textContent === 'Member role updated'`,
	)
	if string(b.evaluate(
		t,
		`document.querySelector('[data-project-form] input[name=name]').value === 'unsaved project' && document.documentElement.scrollWidth <= innerWidth && [...document.querySelectorAll('#project-members .overflow-x-auto')].every(e => e.scrollWidth <= e.clientWidth)`,
	)) != "true" {
		t.Fatal("role save discarded settings or overflowed the page")
	}
	if modal && string(b.evaluate(t,
		`document.querySelector('#project-edit-dialog').matches(':modal')`,
	)) != "true" {
		t.Fatal("role save closed the project dialog")
	}
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector(%q).querySelector('button').click(); true`,
		row,
	))
}
