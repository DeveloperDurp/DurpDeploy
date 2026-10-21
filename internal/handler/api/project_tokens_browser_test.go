//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProjectTokensBrowserE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	session := os.Getenv("DURPDEPLOY_LIVE_SESSION")
	var projects []string
	if base == "" {
		f := newArtifactE2E(t)
		other := seedProject(t, f.h.repo)
		for range 18 {
			seedProject(t, f.h.repo)
		}
		base, session = f.baseURL, f.session
		projects = []string{strconv.FormatInt(f.project.ID, 10),
			strconv.FormatInt(other.ID, 10)}
	} else {
		projects = strings.Split(
			os.Getenv("DURPDEPLOY_LIVE_TOKEN_PROJECTS"),
			",",
		)
	}
	if len(projects) != 2 || projects[0] == "" || session == "" {
		t.Fatal(
			"token browser suite requires two retained projects and a session",
		)
	}
	b := startPackageBrowser(t, true)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		b.evaluate(
			t,
			`document.querySelectorAll('.alert code').forEach(code=>code.textContent='[redacted]'); true`,
		)
		t.Logf(
			"token UI state: %s",
			b.evaluate(
				t,
				`({path:location.pathname, errors:[...document.querySelectorAll('.alert-error')].map(e=>e.textContent), rows:[...document.querySelectorAll('tbody tr')].map(row=>({name:row.cells[0].textContent,access:row.cells[1].textContent,scope:row.querySelector('[name=scope]')?.value,role:row.querySelector('[name=role]')?.value,projects:[...row.querySelectorAll('input[name=project_ids]:checked')].map(o=>o.value)}))})`,
			),
		)
		b.screenshot(t, "project-token-failure")
		t.Logf(
			"picker state: %s",
			b.evaluate(
				t,
				`({active:document.activeElement.id,labels:[...document.querySelectorAll('[data-token-project-dialog][open] label[data-project-name]')].map(el=>({name:el.dataset.projectName,style:el.getAttribute('style'),display:getComputedStyle(el).display,query:Alpine.$data(el).query,matches:Alpine.$data(el).matches(el.dataset.projectName)}))})`,
			),
		)
	})
	b.call(
		t,
		"Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true},
		&struct{}{},
	)
	b.setBackTestSession(t, base, session)
	b.navigateBackTest(t, base+"/settings/tokens")
	b.wait(
		t,
		`document.querySelector('#new-token-role') && !document.querySelector('.htmx-request')`,
	)
	if string(
		b.evaluate(
			t,
			`[...document.querySelector('#new-token-role').options].map(option=>option.value).join(',') === 'viewer,deployer,maintainer,admin'`,
		),
	) != "true" {
		t.Fatal("token selector omits a permission level")
	}
	if string(
		b.evaluate(
			t,
			`document.querySelector('#new-token-scope').value === 'global' && document.querySelector('#new-token-role').value === 'viewer' && document.querySelectorAll('form[action="/settings/tokens"] input[name=project_ids]:checked').length === 0`,
		),
	) != "true" {
		t.Fatal(
			"new token form did not default to all accessible projects",
		)
	}
	name := "scope-browser-" + uuid.NewString()
	// All-project tokens retain a visible permission selector.
	b.wait(t, `(() => {
 const role = document.querySelector('#new-token-role');
 const projects = document.querySelector('#new-token-projects');
 return !role.disabled && role.getBoundingClientRect().width && projects.disabled && !projects.getBoundingClientRect().width;
})()`)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value = %q; form.requestSubmit(); return true;
})()`, "global-"+name))
	b.wait(t, `document.querySelector('.alert code')`)
	b.evaluate(
		t,
		`document.querySelector('.alert a[href="/settings/tokens"]').click(); true`,
	)
	b.wait(
		t,
		`location.search === '' && !document.querySelector('.alert code')`,
	)
	globalRow := fmt.Sprintf(
		`[...document.querySelectorAll('[data-token-record]')].find(row=>row.getBoundingClientRect().width && row.querySelector('[data-token-name]').textContent.trim()===%q)`,
		"global-"+name,
	)
	b.wait(t, fmt.Sprintf(
		`(%s)?.querySelector('[data-token-access]').textContent.includes('Global · viewer')`,
		globalRow,
	))
	b.waitTokenSettingsReady(t)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const row=%s; row.querySelector('summary').click();
 const form=row.querySelector('form'); form.elements.role.value='deployer';
 form.requestSubmit(); return true;
})()`, globalRow))
	b.wait(
		t,
		fmt.Sprintf(
			`(%s)?.querySelector('[data-token-access]').textContent.includes('Global · deployer')`,
			globalRow,
		),
	)
	b.waitTokenSettingsReady(t)
	b.evaluate(t, `(() => {
 const scope = document.querySelector('#new-token-scope');
 scope.value = 'scoped'; scope.dispatchEvent(new Event('change', {bubbles:true}));
 return true;
})()`)
	b.wait(t, `!document.querySelector('#new-token-projects').disabled`)
	// Choose both projects through the searchable checkbox picker.
	b.chooseTokenProjects(t,
		`document.querySelector('form[action="/settings/tokens"]')`,
		projects...)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value = %q;
 form.elements.scope.value = 'scoped';
 form.elements.role.value = 'deployer';
 form.requestSubmit(); return true;
})()`, name))
	b.wait(
		t,
		`location.pathname === '/settings/tokens' && location.search.includes('flash=') && document.querySelector('.alert code')`,
	)
	if string(
		b.evaluate(
			t,
			`/^ddp_pat_[0-9a-f]{64}$/.test(document.querySelector('.alert code').textContent.trim())`,
		),
	) != "true" {
		t.Fatal("new token was not shown once")
	}
	// Do not capture the bearer credential in screenshots.
	b.evaluate(
		t,
		`document.querySelector('.alert a[href="/settings/tokens"]').click(); true`,
	)
	b.wait(
		t,
		`location.search === '' && !document.querySelector('.alert code')`,
	)
	row := fmt.Sprintf(
		`[...document.querySelectorAll('[data-token-record]')].find(row=>row.getBoundingClientRect().width && row.querySelector('[data-token-name]').textContent.trim()===%q)`,
		name,
	)
	b.wait(t, fmt.Sprintf(
		`(() => {const row=%s; return !!row && row.querySelector('[data-token-access]').textContent.includes('deployer') && row.querySelector('[name=scope]').value === 'scoped' && row.querySelectorAll('input[name=project_ids]:checked').length === 2;})()`,
		row,
	))
	b.waitTokenSettingsReady(t)
	b.evaluate(t, `(() => {
 const scope=document.querySelector('#new-token-scope');
 scope.value='scoped'; scope.dispatchEvent(new Event('change',{bubbles:true})); return true;
})()`)
	b.wait(t, `!document.querySelector('#new-token-projects').disabled`)
	b.checkTokenSettingsLayouts(t, row)
	// Exercise the card's edit form at phone width.
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 375, "height": 900, "deviceScaleFactor": 1, "mobile": true,
	}, &struct{}{})
	// Empty scoped selections return a validation message and retain input.
	b.evaluate(t, `(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value = 'empty-selection'; form.elements.scope.value = 'scoped';
 form.elements.scope.dispatchEvent(new Event('change', {bubbles:true}));
 return true;
})()`)
	b.wait(t, `!document.querySelector('#new-token-projects').disabled`)
	b.chooseTokenProjects(t,
		`document.querySelector('form[action="/settings/tokens"]')`)
	b.evaluate(t, `(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 form.requestSubmit(); return true;
})()`)
	b.assertTokenProjectError(t,
		`document.querySelector('form[action="/settings/tokens"]')`, "viewer")
	b.waitTokenSettingsReady(t)
	if string(b.evaluate(
		t,
		`document.querySelector('#token-name').value === 'empty-selection'`,
	)) != "true" {
		t.Fatal("missing selection discarded the token name")
	}
	b.captureNavigation(t, "project-token-empty-selection")
	// Replace the scope through the owner session without redisplaying the secret.
	b.evaluate(
		t,
		fmt.Sprintf(`(() => {const row=%s; row.querySelector('summary').click();
 row.querySelector('form[data-token-scope-edit]').scrollIntoView({block:'center'});
 return true;})()`, row),
	)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const form=(%s).querySelector('form[data-token-scope-edit]');
 const bounds=form.getBoundingClientRect();
 return bounds.width > 250 && bounds.left >= 0 && bounds.right <= innerWidth
   && [...form.querySelectorAll('select,button')].filter(field=>!field.closest('dialog')).every(field=>field.getBoundingClientRect().width >= bounds.width - 26);
})()`, row))) != "true" {
		t.Fatal("scope edit controls are clipped or squeezed on mobile")
	}
	b.screenshot(t, "project-token-mobile-edit")
	editForm := fmt.Sprintf(
		`(%s).querySelector('form[data-token-scope-edit]')`, row)
	b.chooseTokenProjects(t, editForm)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form=%s; form.elements.role.value='maintainer';
 form.requestSubmit(); return true;
})()`, editForm))
	b.assertTokenProjectError(t, editForm, "maintainer")
	b.waitTokenSettingsReady(t)
	if string(b.evaluate(t,
		`!document.querySelector('#new-token-projects-error')`)) != "true" {
		t.Fatal("scope edit marked the create form invalid")
	}
	b.chooseTokenProjects(t,
		fmt.Sprintf(`(%s).querySelector('form[data-token-scope-edit]')`, row),
		projects[1])
	b.evaluate(
		t,
		fmt.Sprintf(
			`(() => {const form=(%s).querySelector('form[data-token-scope-edit]'); form.elements.role.value='viewer';
 form.requestSubmit(); return true;})()`,
			row,
		),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`(() => {const row=%s; return location.pathname==='/settings/tokens' && !document.querySelector('.alert-error') && row?.querySelector('[data-token-access]').textContent.includes('viewer') && row.querySelectorAll('input[name=project_ids]:checked').length===1;})()`,
			row,
		),
	)
	if string(
		b.evaluate(t, `!document.querySelector('.alert code')`),
	) != "true" {
		t.Fatal("editing scope redisplayed a bearer credential")
	}
	b.captureNavigation(t, "project-token-edited-scope")
}

func (b *packageBrowser) checkTokenSettingsLayouts(t *testing.T, row string) {
	t.Helper()
	for _, width := range []int{375, 430, 540, 768, 980, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width":             width,
			"height":            900,
			"deviceScaleFactor": 1,
			"mobile":            width < 768,
		}, &struct{}{})
		if string(
			b.evaluate(t, `document.documentElement.scrollWidth <= innerWidth`),
		) != "true" {
			t.Fatalf("token page overflows at width %d", width)
		}
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 const fields = [...form.querySelectorAll('#token-name,#new-token-scope,#new-token-role,#new-token-projects')].map(field=>field.getBoundingClientRect()).filter(field=>field.width);
 const bounds = form.getBoundingClientRect();
 const record = %s;
 return fields.every((field,index)=>field.width >= bounds.width - 2 && field.left >= 0 && field.right <= innerWidth && (!index || field.top >= fields[index-1].bottom))
   && record.tagName === (innerWidth < 1280 ? 'ARTICLE' : 'TR');
})()`, row))) != "true" {
			t.Fatalf(
				"token controls are squeezed or overlap at width %d",
				width,
			)
		}
		b.screenshot(t, fmt.Sprintf("project-token-scope-%d", width))
		b.checkTokenPickerLayout(t, width)
	}
}

func (b *packageBrowser) assertTokenProjectError(
	t *testing.T, form, role string,
) {
	t.Helper()
	b.wait(t, fmt.Sprintf(`(() => {
 const form=%s; if (!form) return false;
 const button=form.querySelector('[aria-haspopup=dialog]');
 const error=form.querySelector('[role=alert]');
 return error?.textContent==='Select at least one project'
   && error.getBoundingClientRect().height > 0
   && button.getAttribute('aria-invalid')==='true'
   && button.getAttribute('aria-describedby').split(' ').includes(error.id)
   && form.elements.scope.value==='scoped' && form.elements.role.value===%q
   && !form.querySelector('input[name=project_ids]:checked')
   && !document.querySelector('.alert code');
})()`, form, role))
}

func (b *packageBrowser) waitTokenSettingsReady(t *testing.T) {
	t.Helper()
	b.wait(
		t,
		`document.readyState==='complete' && window.Alpine && !document.querySelector('.htmx-settling, .htmx-request')`,
	)
}

func (b *packageBrowser) chooseTokenProjects(
	t *testing.T, form string, ids ...string,
) {
	t.Helper()
	b.evaluate(
		t,
		fmt.Sprintf(
			`(%s).querySelector('[aria-haspopup=dialog]').click(); true`,
			form,
		),
	)
	b.wait(t, `document.querySelector('[data-token-project-dialog][open]')`)
	b.evaluate(t, `(() => {
 const dialog=document.querySelector('[data-token-project-dialog][open]');
 [...dialog.querySelectorAll('button')].find(button=>button.textContent.trim()==='Clear selection').click(); return true;
})()`)
	b.wait(
		t,
		`!document.querySelector('[data-token-project-dialog][open] input[name=project_ids]:checked')`,
	)
	for _, id := range ids {
		b.evaluate(t, fmt.Sprintf(`(() => {
 const dialog=document.querySelector('[data-token-project-dialog][open]');
 const checkbox=dialog.querySelector('input[value=%q]');
 const search=dialog.querySelector('input[type=search]');
 search.value=checkbox.dataset.projectName.toUpperCase(); search.dispatchEvent(new Event('input',{bubbles:true})); return true;
})()`, id))
		b.wait(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-token-project-dialog][open] input[value=%q]').closest('label').getBoundingClientRect().height >= 48`,
				id,
			),
		)
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-token-project-dialog][open] input[value=%q]').closest('label').click(); true`,
				id,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-token-project-dialog][open] input[value=%q]').checked`,
				id,
			),
		)
	}
	b.evaluate(t, `(() => {
 const search=document.querySelector('[data-token-project-dialog][open] input[type=search]');
 search.value='no-project-matches-this-search'; search.dispatchEvent(new Event('input',{bubbles:true})); search.focus(); return true;
})()`)
	b.wait(
		t,
		`![...document.querySelectorAll('[data-token-project-dialog][open] label[data-project-name]')].some(row=>row.getBoundingClientRect().height)`,
	)
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": "Enter", "code": "Enter",
		"windowsVirtualKeyCode": 13,
	}, &struct{}{})
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": "Enter", "code": "Enter",
		"windowsVirtualKeyCode": 13,
	}, &struct{}{})
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(`new Promise(resolve=>requestAnimationFrame(()=>{
 const dialog=document.querySelector('[data-token-project-dialog][open]');
 resolve(!!dialog && dialog.querySelectorAll('input[name=project_ids]:checked').length===%d && dialog.textContent.includes('No projects match your search.'));
}))`, len(ids)),
		),
	) != "true" {
		t.Fatal("project search submitted the form or lost filtered selections")
	}
	b.evaluate(
		t,
		`document.querySelector('[data-token-project-dialog][open] [data-token-project-done]').click(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`!document.querySelector('[data-token-project-dialog][open]') && document.activeElement===(%s).querySelector('[aria-haspopup=dialog]')`,
			form,
		),
	)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const form=%s;
 return document.activeElement===form.querySelector('[aria-haspopup=dialog]')
   && new FormData(form).getAll('project_ids').length===%d;
})()`, form, len(ids)))) != "true" {
		t.Fatal("closed project picker lost focus or form values")
	}
}

func (b *packageBrowser) checkTokenPickerLayout(t *testing.T, width int) {
	t.Helper()
	b.evaluate(t, `document.querySelector('#new-token-projects').click(); true`)
	b.wait(t, `document.querySelector('[data-token-project-dialog][open]')`)
	b.wait(t, `(() => {
 const panel=document.querySelector('[data-token-project-dialog][open] .modal-box');
 return getComputedStyle(panel).scale==='1' && getComputedStyle(panel).opacity==='1';
})()`)
	if string(b.evaluate(t, `(() => {
 const dialog=document.querySelector('[data-token-project-dialog][open]');
 const panel=dialog.querySelector('.modal-box').getBoundingClientRect();
 const done=dialog.querySelector('[data-token-project-done]').getBoundingClientRect();
 const list=dialog.querySelector('[data-token-project-list]');
 const bounds=list.getBoundingClientRect();
 const footer=dialog.querySelector('[data-token-project-done]').parentElement.parentElement.getBoundingClientRect();
 const initial=list.scrollTop;
 list.scrollTop=list.scrollHeight;
 const scrolls=list.scrollHeight<=list.clientHeight || list.scrollTop>initial;
 list.scrollTop=0;
 return panel.left>=0 && panel.right<=innerWidth && panel.top>=0 && panel.bottom<=innerHeight
   && done.height>=48 && done.top>=0 && done.bottom<=innerHeight
   && bounds.bottom<=footer.top && getComputedStyle(list).overflowY==='auto' && scrolls
   && (innerWidth>=640 || panel.width>=innerWidth-2 && panel.height>=innerHeight-2);
})()`)) != "true" {
		t.Fatalf("project picker or Done button is clipped at %dpx", width)
	}
	b.screenshot(t, fmt.Sprintf("project-token-picker-%d", width))
	assertDialogKeyboardBounds(t, b, "[data-token-project-dialog][open]")
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": "Escape", "code": "Escape",
		"windowsVirtualKeyCode": 27,
	}, &struct{}{})
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": "Escape", "code": "Escape",
		"windowsVirtualKeyCode": 27,
	}, &struct{}{})
	b.wait(
		t,
		`!document.querySelector('[data-token-project-dialog][open]') && document.activeElement===document.querySelector('#new-token-projects')`,
	)
	if string(
		b.evaluate(
			t,
			`document.activeElement===document.querySelector('#new-token-projects')`,
		),
	) != "true" {
		t.Fatal("closing the picker did not restore focus")
	}
}
