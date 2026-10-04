//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestMainPagePatternBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var template, lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/templates",
		map[string]string{
			"name": "consistent-template", "script_body": "echo hi",
			"interpreter": "bash", "container_image": "alpine:3.20",
			"execution_target": "local",
		}, 201), &template)
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "consistent-lifecycle"}, 201), &lifecycle)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	paths := []string{
		"/projects", "/environments", "/lifecycles", "/templates",
		"/deployments", "/admin/agents",
		"/environments/new",
		fmt.Sprintf("/environments/%d/edit", f.environment.ID),
		fmt.Sprintf("/lifecycles/%d", lifecycle.ID),
		fmt.Sprintf("/templates/%d/edit", template.ID),
	}
	for _, suffix := range []string{
		"", "/edit", "/steps-page", "/variables", "/releases", "/schedules",
		"/runbooks", "/package-repository",
	} {
		paths = append(
			paths,
			fmt.Sprintf("/projects/%d%s", f.project.ID, suffix),
		)
	}
	for i, path := range paths {
		b.navigateBackTest(t, f.baseURL+path)
		b.wait(t, `document.querySelector('main .page-header h1') !== null`)
		b.captureNavigation(t, fmt.Sprintf("main-page-%d", i), func() {
			b.wait(
				t,
				`document.getAnimations().every(a => a.playState !== 'running')`,
			)
			if string(b.evaluate(t, `(() => {
 const header = document.querySelector('main .page-header');
 const title = header.querySelector('h1');
 const controls = [...header.querySelectorAll('.btn')];
 const titleBox = title.getBoundingClientRect();
 const headerBox = header.getBoundingClientRect();
 const actionBox = header.lastElementChild.getBoundingClientRect();
 const form = document.querySelector('#environment-settings-form, #template-settings-form');
 const sections = [...document.querySelectorAll('nav[aria-label="Project sections"] a')];
 const projectDelete = [...document.querySelectorAll('main button[hx-delete]')].find(el => /^\/projects\/\d+$/.test(el.getAttribute('hx-delete')));
 const deleteSection = projectDelete?.parentElement;
 return (!form || Math.abs(form.getBoundingClientRect().width - document.querySelector('#form-container').getBoundingClientRect().width) <= 1) &&
 (!deleteSection || deleteSection === deleteSection.parentElement.lastElementChild) &&
 sections.every(el => ['btn-primary', 'btn-secondary', 'btn-accent'].some(cls => el.classList.contains(cls))) &&
 sections.every((el, i) => i === 0 || getComputedStyle(el).backgroundColor !== getComputedStyle(sections[i - 1]).backgroundColor) &&
 title.scrollWidth <= title.clientWidth &&
 (!controls.length || (Math.abs(actionBox.right - headerBox.right) <= 1 &&
 (innerWidth >= 768 ? Math.abs(actionBox.top - headerBox.top) <= 1 : actionBox.top >= titleBox.bottom))) &&
 controls.every(el => el.getBoundingClientRect().right <= innerWidth &&
 (innerWidth >= 768 || el.getBoundingClientRect().height >= 44));
})()`)) != "true" {
				t.Fatalf(
					"header clips text or phone controls on %s: %s",
					path,
					b.evaluate(
						t,
						`JSON.stringify([...document.querySelector('main .page-header').children].map(el => ({tag: el.tagName, box: el.getBoundingClientRect().toJSON()})))`,
					),
				)
			}
		})
	}
	for _, card := range []struct{ list, destination string }{
		{"/projects", fmt.Sprintf("/projects/%d", f.project.ID)},
		{"/environments", fmt.Sprintf("/environments/%d/edit", f.environment.ID)},
		{"/lifecycles", fmt.Sprintf("/lifecycles/%d", lifecycle.ID)},
		{"/templates", fmt.Sprintf("/templates/%d/edit", template.ID)},
	} {
		b.navigateBackTest(t, f.baseURL+card.list)
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const link = document.querySelector('main [data-resource-card] a[href=%q]');
 if (!link) return false;
 const card = link.closest('[data-resource-card]');
 const box = card.getBoundingClientRect();
 link.focus();
 const style = getComputedStyle(link);
 return !document.querySelector('main table') && box.height >= 44 &&
 style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) >= 2 &&
 document.elementFromPoint(box.left + 8, box.bottom - 8) === link &&
 ![...document.querySelectorAll('main a, main button')].some(el => el.textContent === 'Edit');
})()`, card.destination))) != "true" {
			t.Fatal(
				"resource card lacks full-card navigation or keyboard focus",
			)
		}
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type":                  "keyDown",
			"key":                   "Enter",
			"code":                  "Enter",
			"windowsVirtualKeyCode": 13,
		}, &struct{}{})
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type":                  "keyUp",
			"key":                   "Enter",
			"code":                  "Enter",
			"windowsVirtualKeyCode": 13,
		}, &struct{}{})
		if card.list == "/projects" {
			b.wait(t, fmt.Sprintf(`location.pathname === %q`, card.destination))
		} else {
			b.wait(
				t,
				fmt.Sprintf(
					`location.pathname === %q && document.querySelector('#project-edit-dialog')?.matches(':modal')`,
					card.list,
				),
			)
		}
	}
	// Header Save controls submit the real edit form and persist through the API.
	for _, edit := range []struct {
		path, api, name string
	}{
		{fmt.Sprintf("/projects/%d/edit", f.project.ID),
			fmt.Sprintf("/api/v1/projects/%d", f.project.ID), "saved-project"},
		{fmt.Sprintf("/templates/%d/edit", template.ID),
			fmt.Sprintf("/api/v1/templates/%d", template.ID), "saved-template"},
	} {
		b.navigateBackTest(t, f.baseURL+edit.path)
		b.evaluate(
			t,
			fmt.Sprintf(
				`window.savedForm = document.querySelector('main input[name="name"]').form; document.querySelector('main input[name="name"]').value = %q; document.querySelector('.page-header button[type="submit"]').click(); true`,
				edit.name,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`!window.savedForm?.isConnected && (document.querySelector('main input[name="name"]')?.value === %q || document.querySelector('main').textContent.includes(%q))`,
				edit.name,
				edit.name,
			),
		)
		var saved struct{ Name string }
		decodeStepLogTest(t, f.api(t, "GET", edit.api, nil, 200), &saved)
		if saved.Name != edit.name {
			t.Fatalf("header Save did not persist %s", edit.api)
		}
	}
	testTemplateDeleteOnEdit(t, f, b, template.ID)
	testNewEnvironmentHeaderSave(t, f, b)
}

func testTemplateDeleteOnEdit(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
	id int64,
) {
	t.Helper()
	b.navigateBackTest(t, f.baseURL+"/templates")
	if string(
		b.evaluate(
			t,
			`!document.querySelector('main [data-template-action="delete"]')`,
		),
	) != "true" {
		t.Fatal("template list still exposes Delete")
	}
	b.navigateBackTest(t, fmt.Sprintf("%s/templates/%d/edit", f.baseURL, id))
	b.captureNavigation(t, "template-edit-delete", func() {
		if string(b.evaluate(t, `(() => {
 const button = document.querySelector('[data-template-action="delete"]');
 return button.type === 'button' && !button.closest('form') && button.getBoundingClientRect().height >= 44;
})()`)) != "true" {
			t.Fatal("template Delete is not separate from Save")
		}
	})
	b.evaluate(
		t,
		`window.confirm = () => false; document.querySelector('[data-template-action="delete"]').click(); true`,
	)
	f.api(t, "GET", fmt.Sprintf("/api/v1/templates/%d", id), nil, 200)
	b.evaluate(
		t,
		`window.confirm = () => true; document.querySelector('[data-template-action="delete"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/templates' && document.querySelector('main h1')?.textContent === 'Templates' && !document.querySelector('[data-template-action="delete"]')`,
	)
	f.api(t, "GET", fmt.Sprintf("/api/v1/templates/%d", id), nil, 404)
	b.navigateBackTest(t, f.baseURL+"/templates/new")
	if string(
		b.evaluate(
			t,
			`!document.querySelector('main [data-template-action="delete"]')`,
		),
	) != "true" {
		t.Fatal("new template exposes Delete")
	}
}

func testNewEnvironmentHeaderSave(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
) {
	t.Helper()
	b.navigateBackTest(t, f.baseURL+"/environments/new")
	if string(
		b.evaluate(
			t,
			`document.querySelector('.page-header button')?.textContent === 'Save' && document.querySelector('.page-header a')?.textContent === 'Back' && !document.querySelector('main form button[type="submit"]')`,
		),
	) != "true" {
		t.Fatal("new environment does not use header Save/Back")
	}
	b.evaluate(
		t,
		`document.querySelector('input[name="name"]').value = 'header-created-environment'; document.querySelector('.page-header button[type="submit"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/environments' && document.querySelector('main').textContent.includes('header-created-environment')`,
	)
	var result struct{ Items []struct{ Name string } }
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/environments", nil, 200),
		&result,
	)
	for _, item := range result.Items {
		if item.Name == "header-created-environment" {
			return
		}
	}
	t.Fatal(
		"header Save did not create the environment through the public contract",
	)
}
