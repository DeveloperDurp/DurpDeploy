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
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
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
	path := fmt.Sprintf("/projects/%d", project.ID)
	row := fmt.Sprintf(`[data-member-id="%d"]`, target.ID)
	for _, tc := range []struct {
		name, global, project string
		allowed               bool
	}{
		{"global-admin", "admin", "", true},
		{"project-admin", "deployer", "admin", true},
		{"project-deployer", "deployer", "deployer", false},
		{"viewer-project-admin", "viewer", "admin", false},
		{"outsider", "deployer", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := seedAPIUser(t, h.repo, tc.name+"@browser.test", tc.global)
			if tc.project != "" {
				if err := h.repo.Queries.AddProjectMember(t.Context(),
					db.AddProjectMemberParams{
						ProjectID: project.ID, UserID: user.ID, Role: tc.project,
					}); err != nil {
					t.Fatal(err)
				}
			}
			b.setBackTestSession(t, srv.URL, memberRoleSession(t, h, user.ID))
			b.call(t, "Page.navigate", map[string]string{
				"url": srv.URL + path + "/edit",
			}, &struct{}{})
			b.wait(
				t,
				`document.readyState === 'complete' && document.querySelector('h1')`,
			)
			if tc.allowed || tc.project != "" {
				b.waitBackTestURL(t, srv.URL+path+"/edit")
			}
			if !tc.allowed {
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
})()`, path+"/members/"+fmt.Sprint(target.ID)))) != "true" {
					t.Fatal("browser role update bypassed authorization")
				}
				b.screenshot(t, "members-denied-"+tc.name)
				return
			}
			if tc.global == "admin" {
				b.navigateBackTest(t, srv.URL+path)
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
				b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
					"width": width, "height": 900, "deviceScaleFactor": 1,
					"mobile": false,
				}, &struct{}{})
				b.wait(
					t,
					`!document.querySelector('.htmx-request, .htmx-settling')`,
				)
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
				b.screenshot(
					t,
					fmt.Sprintf("members-edit-%s-%d", tc.name, width),
				)
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
				for _, role := range []string{"admin", "deployer"} {
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
						t.Fatalf("closed role editor remains visible: %s",
							b.evaluate(
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
						t.Fatal(
							"role save discarded settings or overflowed the page",
						)
					}
					if tc.global == "admin" && string(b.evaluate(
						t,
						`document.querySelector('#project-edit-dialog').matches(':modal')`,
					)) != "true" {
						t.Fatal("role save closed the project dialog")
					}
					b.evaluate(t, fmt.Sprintf(
						`document.querySelector(%q).querySelector('button').click(); true`,
						row,
					))
				}
				// Invalid input must keep the editor and unsaved settings.
				b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector(%q);
 row.querySelector('select').add(new Option('invalid', 'invalid', false, true));
 row.querySelector('button[type=submit]').click();
 return true;
})()`, row))
				b.wait(t, fmt.Sprintf(
					`document.querySelector(%q).querySelector('[role=alert]')?.textContent.includes('Role must be admin or deployer') && document.querySelector(%q).querySelector('[role=alert]').getClientRects().length > 0`,
					row,
					row,
				))
				b.evaluate(t, fmt.Sprintf(
					`[...document.querySelector(%q).querySelectorAll('button')].find(b => b.textContent === 'Cancel').click(); true`,
					row,
				))
			}
			if tc.global != "admin" {
				b.evaluate(t, fmt.Sprintf(`(() => {
 const row = document.querySelector('[data-member-id="%d"]');
 row.querySelector('button').click();
 row.querySelector('select').value = 'deployer';
 row.querySelector('button[type=submit]').click();
 return true;
})()`, user.ID))
				b.wait(t, `!document.querySelector('#project-members form')`)
			}
		})
	}
}
