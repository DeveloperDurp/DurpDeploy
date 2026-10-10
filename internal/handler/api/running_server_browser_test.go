//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// make e2e-test supplies an existing server; CI uses an isolated fixture.
func TestRunningServerBrowserE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	session := os.Getenv("DURPDEPLOY_LIVE_SESSION")
	token := os.Getenv("DURPDEPLOY_LIVE_API_TOKEN")
	if base == "" {
		f := newArtifactE2E(t)
		base, session, token = f.baseURL, f.session, f.token
		release := seedRelease(t, f.h.repo, f.project.ID)
		seedDeployment(t, f.h.repo, release.ID,
			f.environment.ID, "succeeded")
		seedDeployment(t, f.h.repo, release.ID,
			f.environment.ID, "running")
	}
	if session == "" || token == "" {
		t.Fatal("running browser suite requires its test session and API token")
	}
	b := startPackageBrowser(t)
	b.call(t, "Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true}, &struct{}{})
	b.setBackTestSession(t, base, session)
	b.navigateBackTest(t, base+"/")
	if lifecycle := os.Getenv("DURPDEPLOY_LIVE_LIFECYCLE"); lifecycle != "" {
		b.navigateBackTest(t, base+lifecycle)
		b.wait(t, `!!document.querySelector('[data-shared-variable="REGION"]')`)
		b.captureNavigation(t, "live-lifecycle-shared")
		b.navigateBackTest(t, base+os.Getenv("DURPDEPLOY_LIVE_INHERITED"))
		b.wait(
			t,
			`document.querySelector('[data-inherited-variables]')?.textContent.includes('Project override')`,
		)
		b.captureNavigation(t, "live-project-inherited")
	}
	api := func(t *testing.T, path string, status int) json.RawMessage {
		t.Helper()
		var response struct {
			Status int             `json:"status"`
			Body   json.RawMessage `json:"body"`
		}
		result := b.evaluate(t, fmt.Sprintf(`(async () => {
 const response = await fetch(%q, {headers:{Authorization: %q}});
 return {status:response.status,body:await response.json()};
})()`, "/api/v1"+path, "Bearer "+token))
		if err := json.Unmarshal(result, &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != status {
			t.Fatalf("GET %s: %d, want %d", path, response.Status, status)
		}
		return response.Body
	}
	if editor := os.Getenv("DURPDEPLOY_LIVE_RUNBOOK_EDITOR"); editor != "" {
		b.navigateBackTest(t, base+editor)
		b.wait(
			t,
			`document.querySelector('#runbook-version-form [name=step_name]')`,
		)
		b.evaluate(
			t,
			`document.querySelector('#runbook-version-form button.btn-secondary').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('dialog').matches(':modal') && document.querySelector('dialog [name=step_name]')`,
		)
		b.evaluate(
			t,
			`document.querySelector('dialog button.btn-ghost').click(); true`,
		)
		b.wait(t, `!document.querySelector('dialog').open`)
		b.evaluate(
			t,
			`document.querySelector('button[form="runbook-version-form"]').click(); true`,
		)
		b.waitBackTestURL(t, base+strings.TrimSuffix(editor, "/edit"))
		var detail struct {
			Versions []json.RawMessage `json:"versions"`
		}
		if err := json.Unmarshal(api(t, strings.TrimSuffix(editor, "/edit"), 200), &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail.Versions) != 3 {
			t.Fatalf(
				"runbook versions after Create new version = %d, want 3",
				len(detail.Versions),
			)
		}
	}
	for _, resource := range []string{
		"projects", "environments", "lifecycles", "templates",
	} {
		t.Run(resource, func(t *testing.T) {
			name := "live-modal-" + uuid.NewString()
			b.navigateBackTest(t, base+"/"+resource)
			b.wait(
				t,
				`!document.querySelector('.htmx-settling, .htmx-request')`,
			)
			openCreate := func() {
				b.evaluate(t, fmt.Sprintf(
					`document.querySelector('.page-header a[href=%q]').click(); true`,
					"/"+resource+"/new",
				))
				b.wait(
					t,
					`document.querySelector('#project-edit-dialog')?.matches(':modal') && document.querySelector('#project-edit-content input[name="name"]')`,
				)
			}
			openCreate()
			// Outside closes without creating anything; the same opener works again.
			for _, kind := range []string{"mousePressed", "mouseReleased"} {
				b.call(t, "Input.dispatchMouseEvent", map[string]any{
					"type": kind, "x": 1, "y": 100,
					"button": "left", "clickCount": 1,
				}, &struct{}{})
			}
			b.wait(t, `!document.querySelector('#project-edit-dialog').open`)
			openCreate()
			b.wait(
				t,
				`!document.querySelector('.htmx-settling, .htmx-request')`,
			)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const root = document.querySelector('#project-edit-content');
 root.querySelector('[name=name]').value = %q;
 const image = root.querySelector('[name=container_image]');
 const script = root.querySelector('[name=script_body]');
 if (image) image.value = 'docker.io/library/bash:5.2';
 if (script) script.value = 'echo live-template';
 window.liveModalSentinel = true;
 root.querySelector('button[form]').click(); return true;
})()`, name))
			b.wait(
				t,
				fmt.Sprintf(
					`!document.querySelector('#project-edit-dialog').open && document.querySelector('main').textContent.includes(%q)`,
					name,
				),
			)
			if string(
				b.evaluate(t, `window.liveModalSentinel === true`),
			) != "true" {
				t.Fatal("modal Save replaced the document")
			}
			// Resolve only the resource created by this test through the public API.
			var list struct {
				Items []struct {
					ID   int64  `json:"id"`
					Name string `json:"name"`
				} `json:"items"`
			}
			body := api(t, "/"+resource+"?limit=1000", 200)
			if err := json.Unmarshal(body, &list); err != nil {
				t.Fatal(err)
			}
			var id int64
			for _, item := range list.Items {
				if item.Name == name {
					id = item.ID
				}
			}
			if id == 0 {
				t.Fatalf("created %s is absent from the API", resource)
			}
			path := fmt.Sprintf("/%s/%d", resource, id)
			editPath := path + "/edit"
			if resource == "lifecycles" {
				editPath = path
			}
			deleteSelector := fmt.Sprintf(
				"#project-edit-content button[hx-delete=%q]", path)
			if resource == "lifecycles" {
				deleteSelector = `main form:has(input[name="_method"][value="delete"]) button`
			}
			if resource == "projects" {
				b.navigateBackTest(t, base+path+"/steps-page")
				b.wait(
					t,
					`!document.querySelector('.htmx-settling, .htmx-request')`,
				)
				b.evaluate(
					t,
					`document.querySelector('[x-ref="addStepButton"]').click(); true`,
				)
				b.wait(
					t,
					`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content form')`,
				)
				b.wait(
					t,
					`!document.querySelector('.htmx-settling, .htmx-request')`,
				)
				b.evaluate(t, `(() => {
 const form = document.querySelector('#step-edit-content form');
 form.querySelector('[name=name]').value = 'live-step';
 form.querySelector('[name=script_body]').value = 'echo live-step';
 form.querySelector('[name=container_image]').value = 'docker.io/library/bash:5.2';
 form.querySelector('[type=submit]').click(); return true;
})()`)
				b.wait(
					t,
					`!document.querySelector('#step-edit-dialog').open && document.querySelector('#step-edit-content').children.length === 0 && document.querySelector('[data-step-action=edit]')`,
				)
				var steps struct{ Items []struct{ ID int64 } }
				if err := json.Unmarshal(
					api(t, path+"/steps", 200),
					&steps,
				); err != nil {
					t.Fatal(err)
				}
				if len(steps.Items) != 1 {
					t.Fatal("Add Step did not persist exactly one step")
				}
				b.wait(
					t,
					`!document.querySelector('.htmx-settling, .htmx-request')`,
				)
				b.evaluate(
					t,
					`document.querySelector('[data-step-action=edit]').click(); true`,
				)
				b.wait(
					t,
					`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content input[name=name]')`,
				)
				b.wait(
					t,
					`!document.querySelector('.htmx-settling, .htmx-request')`,
				)
				b.evaluate(
					t,
					`window.confirm = () => true; document.querySelector('#step-edit-content button[hx-delete]').click(); true`,
				)
				b.wait(
					t,
					`!document.querySelector('#step-edit-dialog').open && !document.querySelector('[data-step-action=edit]')`,
				)
				api(t, fmt.Sprintf("%s/steps/%d", path, steps.Items[0].ID), 404)
			}
			openEdit := func() {
				if resource == "projects" {
					b.navigateBackTest(t, base+path)
					b.wait(
						t,
						`!document.querySelector('.htmx-settling, .htmx-request')`,
					)
					b.evaluate(
						t,
						`document.querySelector('#project-detail .page-header a[hx-get]').click(); true`,
					)
				} else {
					b.wait(
						t,
						`!document.querySelector('.htmx-settling, .htmx-request')`,
					)
					b.evaluate(t, fmt.Sprintf(
						`document.querySelector('a[href=%q]').click(); true`,
						editPath))
				}
				b.wait(
					t,
					`document.querySelector('#project-edit-dialog')?.matches(':modal') || (!!document.querySelector('#lifecycle-settings-form') && !!document.querySelector('#shared-variables'))`,
				)
			}
			openEdit()
			b.captureNavigation(t, "live-"+resource+"-edit")
			b.evaluate(
				t,
				fmt.Sprintf(
					`document.querySelector('#lifecycle-name, #project-edit-content input[name=name]').value = %q; document.querySelector('button[form="lifecycle-settings-form"], #project-edit-content button[form]').click(); true`,
					name+"-edited",
				),
			)
			b.wait(
				t,
				fmt.Sprintf(
					`!document.querySelector('#project-edit-dialog')?.open && document.querySelector('main').textContent.includes(%q)`,
					name+"-edited",
				),
			)
			var saved struct {
				Name      string
				Lifecycle struct{ Name string }
			}
			if err := json.Unmarshal(api(t, path, 200), &saved); err != nil {
				t.Fatal(err)
			}
			if resource == "lifecycles" {
				saved.Name = saved.Lifecycle.Name
			}
			if saved.Name != name+"-edited" {
				t.Fatal("modal Save did not persist through the API")
			}
			openEdit()
			b.wait(
				t,
				`!document.querySelector('.htmx-settling, .htmx-request')`,
			)
			b.evaluate(
				t,
				fmt.Sprintf(
					`window.confirm = () => false; document.querySelector(%q).click(); true`,
					deleteSelector,
				),
			)
			api(t, path, 200)
			b.evaluate(
				t,
				fmt.Sprintf(
					`window.confirm = () => true; document.querySelector(%q).click(); true`,
					deleteSelector,
				),
			)
			b.wait(
				t,
				`!document.querySelector('#project-edit-dialog')?.open && !document.querySelector('.htmx-settling, .htmx-request')`,
			)
			api(t, path, 404)
		})
	}
	for _, path := range []string{
		"/", "/projects", "/environments", "/lifecycles", "/templates",
		"/deployments",
	} {
		b.navigateBackTest(t, base+path)
		if path == "/" {
			b.wait(
				t,
				`window.DashboardChart && Object.keys(DashboardChart.instances).length === 2`,
			)
		}
		b.captureNavigation(
			t,
			"live-main-"+strings.ReplaceAll(path, "/", ""),
			func() {
				b.wait(t, `document.documentElement.scrollWidth <= innerWidth`)
			},
		)
	}
}
