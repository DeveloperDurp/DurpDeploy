//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/roles"
)

func TestRolePermissionsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "approved execution", "script_body": "printf permission-run-ok",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "permission-browser")
	b := startPackageBrowser(t)
	base := fmt.Sprintf("/projects/%d", f.project.ID)
	for _, role := range roles.All() {
		t.Run(role, func(t *testing.T) {
			user := seedAPIUser(t, f.h.repo, role+"@browser-roles.test", role)
			if err := f.h.repo.Queries.AddProjectMember(t.Context(),
				db.AddProjectMemberParams{ProjectID: f.project.ID,
					UserID: user.ID, Role: role}); err != nil {
				t.Fatal(err)
			}
			b.setBackTestSession(
				t,
				f.baseURL,
				memberRoleSession(t, f.h, user.ID),
			)
			for _, width := range []int{375, 1280} {
				b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
					"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
				}, &struct{}{})
				b.navigateBackTest(t, f.baseURL+base)
				wantAuthor := role == "maintainer" || role == "admin"
				if string(b.evaluate(t, fmt.Sprintf(`(() => {
const main = document.querySelector('#project-detail');
return !!main.querySelector('a[href$="/deploy"]') === %t &&
!!main.querySelector('a[href$="/edit"]') === %t &&
document.documentElement.scrollWidth <= innerWidth;
})()`, role != "viewer", wantAuthor))) != "true" {
					t.Fatal("role controls or layout incorrect")
				}
				b.screenshot(t, fmt.Sprintf("roles-%s-%d", role, width))
			}
			if role == "viewer" || role == "deployer" {
				b.navigateBackTest(t, f.baseURL+base+"/edit")
				if string(
					b.evaluate(
						t,
						`!document.querySelector('[data-project-form]')`,
					),
				) != "true" {
					t.Fatal("restricted user sees edit form")
				}
				if string(b.evaluate(t, fmt.Sprintf(`(async () => {
const response = await fetch(%q, {method:'PUT', headers:{
'Content-Type':'application/x-www-form-urlencoded',
'X-CSRF-Token':'member-role-csrf', 'HX-Request':'true'},body:'name=forged'});
return response.status === 200 && response.headers.get('HX-Trigger').includes('makeToast');
})()`, base))) != "true" {
					t.Fatal("forged browser write was not blocked")
				}
				b.screenshot(t, "roles-forbidden-"+role)
			}
			if role == "maintainer" {
				b.navigateBackTest(t, f.baseURL+base+"/edit")
				b.evaluate(t, `(() => {
const form = document.querySelector('[data-project-form]');
form.querySelector('[name=name]').value = 'Maintainer authored project';
form.querySelector('button[type=submit]').click();
return true;
})()`)
				b.wait(
					t,
					`document.querySelector('#project-detail h1')?.textContent === 'Maintainer authored project'`,
				)
				b.screenshot(t, "roles-maintainer-authored")
			}
			if role == "admin" {
				b.navigateBackTest(t, f.baseURL+"/admin/users/new")
				if string(
					b.evaluate(
						t,
						`[...document.querySelector('[name=role]').options].map(option=>option.value).sort().join(',') === 'admin,deployer,maintainer,viewer'`,
					),
				) != "true" {
					t.Fatal("user selector omits a role")
				}
			}
			if role != "deployer" {
				return
			}
			b.navigateBackTest(
				t,
				fmt.Sprintf(
					"%s%s/deploy?release_id=%d",
					f.baseURL,
					base,
					release.ID,
				),
			)
			b.wait(
				t,
				`document.querySelector('[name=environment_id]') && window.htmx`,
			)
			b.evaluate(t, fmt.Sprintf(`(() => {
const environment = document.querySelector('[name=environment_id]');
environment.value = %q;
environment.dispatchEvent(new Event('change', {bubbles:true}));
environment.form.querySelector('button[type=submit]').click();
return true;
})()`, fmt.Sprint(f.environment.ID)))
			b.wait(
				t,
				`location.pathname.startsWith('/deployments/') && document.querySelector('[data-deployment-status="succeeded"]')`,
			)
			if string(
				b.evaluate(
					t,
					`!!document.querySelector('button[hx-post$="/redeploy"]') && !document.querySelector('a[href$="/rollback"]') && !document.querySelector('button[hx-post$="/approve"]')`,
				),
			) != "true" {
				t.Fatal("deployer execution controls incorrect")
			}
			b.screenshot(t, "roles-deployer-executed")
		})
	}
}
