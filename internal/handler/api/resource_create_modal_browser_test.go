//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestResourceCreateModalBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, resource := range []string{"projects", "environments", "lifecycles", "templates"} {
		t.Run(resource, func(t *testing.T) {
			api := "/api/v1/" + resource
			payload := map[string]string{"name": "duplicate-" + resource}
			if resource == "templates" {
				payload["container_image"] = "alpine:3.20"
				payload["script_body"] = "echo original"
				payload["interpreter"] = "bash"
			}
			f.api(t, "POST", api, payload, 201)
			b.navigateBackTest(t, f.baseURL+"/"+resource)
			open := func() {
				b.wait(
					t,
					`!document.querySelector('.htmx-settling, .htmx-request')`,
				)
				b.evaluate(
					t,
					fmt.Sprintf(
						`document.querySelector('.page-header a[href=%q]').click(); true`,
						"/"+resource+"/new",
					),
				)
				b.wait(
					t,
					`document.querySelector('#project-edit-dialog')?.matches(':modal') && document.querySelector('#project-edit-content input[name="name"]')`,
				)
			}
			open()
			b.captureNavigation(t, resource+"-create-modal", func() {
				b.wait(
					t,
					`document.getAnimations().every(a => a.playState !== 'running')`,
				)
				if string(b.evaluate(t, `(() => {
 const dialog = document.querySelector('#project-edit-dialog');
 const box = dialog.querySelector('.modal-box');
 box.scrollTop = box.scrollHeight;
 const save = dialog.querySelector('button[form]').getBoundingClientRect();
 return box.scrollWidth <= box.clientWidth && save.top >= box.getBoundingClientRect().top &&
 save.bottom <= innerHeight && (innerWidth >= 768 || save.height >= 44) && !dialog.querySelector('[hx-delete]');
})()`)) != "true" {
					t.Fatal("creation modal clips fields or hides Save")
				}
			})
			b.evaluate(
				t,
				`document.querySelector('#project-edit-content input[name="name"]').value = 'discard'; document.querySelector('#project-edit-content a[x-data="backNavigation"]').click(); true`,
			)
			b.wait(
				t,
				`!document.querySelector('#project-edit-dialog').open && document.activeElement?.hasAttribute('hx-get')`,
			)
			open()
			for _, kind := range []string{"mousePressed", "mouseReleased"} {
				b.call(
					t,
					"Input.dispatchMouseEvent",
					map[string]any{
						"type":       kind,
						"x":          1,
						"y":          100,
						"button":     "left",
						"clickCount": 1,
					},
					&struct{}{},
				)
			}
			b.wait(t, `!document.querySelector('#project-edit-dialog').open`)
			open()
			b.wait(
				t,
				`!document.querySelector('.htmx-settling, .htmx-request')`,
			)
			b.evaluate(
				t,
				fmt.Sprintf(
					`const form = document.querySelector('#project-edit-content'); form.querySelector('[name="name"]').value = %q; const desc = form.querySelector('[name="description"]'); if (desc) desc.value = 'retained'; const image = form.querySelector('[name="container_image"]'); if (image) image.value = 'alpine:3.20'; const script = form.querySelector('[name="script_body"]'); if (script) script.value = 'echo retained'; form.querySelector('button[form]').click(); true`,
					payload["name"],
				),
			)
			b.wait(
				t,
				`document.querySelector('#project-edit-dialog').open && document.querySelector('#project-edit-content .text-error')?.textContent.includes('already exists')`,
			)
			if string(
				b.evaluate(
					t,
					`(() => { const form = document.querySelector('#project-edit-content'); const field = form.querySelector('[name="description"], [name="script_body"]'); return field.value.includes('retained'); })()`,
				),
			) != "true" {
				t.Fatal("validation discarded submitted fields")
			}
			b.wait(
				t,
				`!document.querySelector('.htmx-settling, .htmx-request')`,
			)
			name := "modal-created-" + resource
			b.evaluate(
				t,
				fmt.Sprintf(
					`window.createModalSentinel = true; document.querySelector('#project-edit-content input[name="name"]').value = %q; document.querySelector('#project-edit-content button[form]').click(); true`,
					name,
				),
			)
			b.wait(
				t,
				fmt.Sprintf(
					`!document.querySelector('#project-edit-dialog').open && document.querySelector('main').textContent.includes(%q)`,
					name,
				),
			)
			if string(
				b.evaluate(
					t,
					fmt.Sprintf(
						`location.pathname === %q && window.createModalSentinel === true`,
						"/"+resource,
					),
				),
			) != "true" {
				t.Fatal("creating navigated or reloaded the document")
			}
			var result struct {
				Items []struct {
					ID   int64
					Name string
				}
			}
			decodeStepLogTest(t, f.api(t, "GET", api, nil, 200), &result)
			var id int64
			for _, item := range result.Items {
				if item.Name == "discard" {
					t.Fatal("canceling created a resource")
				}
				if item.Name == name {
					id = item.ID
				}
			}
			if id == 0 {
				t.Fatal("creation missing from API list")
			}
			f.api(t, "GET", fmt.Sprintf("%s/%d", api, id), nil, 200)
		})
	}
}
