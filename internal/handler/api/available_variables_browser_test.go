//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestStepVariablesBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	seedVariableViewer(t, f)
	var step struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "saved-viewer-step", "script_body": "echo original",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201), &step)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, size := range []struct{ width, height int }{
		{375, 844}, {375, 420}, {1280, 900},
	} {
		t.Run(
			fmt.Sprintf("%dx%d", size.width, size.height),
			func(t *testing.T) {
				b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
					"width": size.width, "height": size.height,
					"deviceScaleFactor": 1, "mobile": false,
				}, &struct{}{})
				b.navigateBackTest(
					t,
					fmt.Sprintf(
						"%s/projects/%d/steps-page",
						f.baseURL,
						f.project.ID,
					),
				)
				b.evaluate(
					t,
					`document.querySelector('[data-show-step-variables]').click(); true`,
				)
				waitVariableViewer(t, b)
				b.wait(t, `(() => {
 const text = document.querySelector('[data-variable-dialog]').textContent;
 return text.includes('DURPDEPLOY_ENVIRONMENT_NAME') && text.includes('DURPDEPLOY_ENVIRONMENT_ID') && text.includes('bypass variable_names');
})()`)
				b.wait(
					t,
					`document.querySelector('[data-variable-dialog]').textContent.includes('Select an environment')`,
				)
				b.evaluate(
					t,
					`document.querySelector('[aria-label="Close variable viewer"]').click(); true`,
				)
				b.wait(
					t,
					`!document.querySelector('[data-variable-dialog]').open`,
				)
				for _, opener := range []string{"[x-ref=addStepButton]", "[data-step-action=edit]"} {
					b.evaluate(
						t,
						fmt.Sprintf(
							`Array.from(document.querySelectorAll(%q)).find(button=>button.getClientRects().length).click(); true`,
							opener,
						),
					)
					b.wait(
						t,
						`document.querySelector('#step-edit-dialog').matches(':modal') && document.querySelector('#step-edit-content form') && !document.querySelector('.htmx-request,.htmx-settling')`,
					)
					for _, fullscreen := range []bool{false, true} {
						prepareViewerDraft(t, b, fullscreen)
						b.evaluate(t, `window.viewerOpener.click(); true`)
						waitVariableViewer(t, b)
						b.evaluate(t, fmt.Sprintf(`(() => {
 const select = document.querySelector('[data-variable-environment]'); select.value = %q;
 select.dispatchEvent(new Event('change', {bubbles:true})); return true;
})()`, fmt.Sprint(f.environment.ID)))
						waitVariableViewer(t, b)
						b.wait(
							t,
							`document.querySelector('[data-variable-dialog]').textContent.includes('shared-env')`,
						)
						checkViewerCopyAndSearch(t, b)
						b.screenshot(
							t,
							fmt.Sprintf(
								"step-variable-viewer-%dx%d-fullscreen-%t",
								size.width,
								size.height,
								fullscreen,
							),
						)
						b.call(t, "Input.dispatchKeyEvent", map[string]any{
							"type":                  "keyDown",
							"key":                   "Escape",
							"code":                  "Escape",
							"windowsVirtualKeyCode": 27,
						}, &struct{}{})
						b.call(t, "Input.dispatchKeyEvent", map[string]any{
							"type": "keyUp", "key": "Escape", "code": "Escape",
						}, &struct{}{})
						b.wait(
							t,
							`!document.querySelector('[data-variable-dialog]').open && Alpine.$data(document.querySelector('[data-step-variable-viewer]')).opener === null`,
						)
						checkViewerDraft(t, b)
						if fullscreen {
							b.evaluate(
								t,
								`document.querySelector('#step-edit-content [data-fullscreen-dialog][open]').close(); true`,
							)
						}
					}
					b.evaluate(
						t,
						`[...document.querySelectorAll('#step-edit-content .page-header button')].find(button=>button.textContent==='Cancel').click(); true`,
					)
					b.wait(
						t,
						`!document.querySelector('#step-edit-dialog').open && !document.querySelector('#step-edit-content form')`,
					)
				}
			},
		)
	}
	checkViewerFailedLoad(t, b)
}

func waitVariableViewer(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.wait(
		t,
		`(() => { const root = document.querySelector('[data-step-variable-viewer]'); return root && root.querySelector('dialog').matches(':modal') && Alpine.$data(root).catalog && !Alpine.$data(root).loading && !Alpine.$data(root).error; })()`,
	)
}

func prepareViewerDraft(t *testing.T, b *packageBrowser, fullscreen bool) {
	t.Helper()
	b.evaluate(t, `(() => {
 const form = document.querySelector('#step-edit-content form'); window.viewerForm = form;
 for (const [name,value] of Object.entries({name:'unsaved viewer draft',timeout_seconds:'61',max_retries:'2',container_image:'draft-image',variable_names:'REGION, SECRET',interpreter:'python3'})) {
  const field=form.elements.namedItem(name); field.value=value; field.dispatchEvent(new Event('change',{bubbles:true}));
 }
 const script=form.querySelector('[name=script_body]'); script.value=Array.from({length:80},(_,i)=>'print("draft '+i+'")').join('\n'); script.dispatchEvent(new Event('input',{bubbles:true}));
 return true;
})()`)
	if fullscreen {
		b.evaluate(
			t,
			`[...document.querySelectorAll('#step-edit-content button')].find(button=>button.textContent==='Fullscreen').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#step-edit-content [data-fullscreen-dialog]').matches(':modal')`,
		)
	}
	b.evaluate(t, `(() => {
 const form=window.viewerForm, dialog=form.querySelector('[data-fullscreen-dialog][open]');
 window.viewerTextarea=dialog?.querySelector('textarea') || form.querySelector('[name=script_body]');
 window.viewerOpener=dialog?.querySelector('[data-show-step-variables]') || form.querySelector('[data-show-step-variables]');
 window.viewerTextarea.style.height='8rem'; window.viewerTextarea.style.fieldSizing='fixed';
 window.viewerTextarea.setSelectionRange(7,19); window.viewerTextarea.scrollTop=80;
 window.viewerPosition=window.viewerTextarea.scrollTop;
 window.viewerScrollArea=form.querySelector('.overflow-y-auto'); window.viewerScrollArea.scrollTop=90;
 window.viewerScrollPosition=window.viewerScrollArea.scrollTop;
 window.viewerSnapshot=JSON.stringify([...new FormData(form)]);
 return true;
})()`)
}

func checkViewerCopyAndSearch(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.evaluate(
		t,
		`(() => { const input=document.querySelector('[data-variable-search]'); input.value='secret'; input.dispatchEvent(new Event('input',{bubbles:true})); return true; })()`,
	)
	b.wait(
		t,
		`document.querySelector('[data-variable-dialog] article').textContent.includes('SECRET') && document.querySelectorAll('[data-variable-dialog] article').length === 1`,
	)
	if string(b.evaluate(t, `(() => {
 const dialog=document.querySelector('[data-variable-dialog]'), row=dialog.querySelector('article');
 return !dialog.textContent.includes('viewer-secret-sentinel') && row.textContent.includes('masked') &&
 row.querySelector('input').value === 'os.environ.get("SECRET")' &&
 dialog.querySelector('[role=status][aria-live=polite]') && document.documentElement.scrollWidth <= innerWidth;
})()`)) != "true" {
		t.Fatal(
			"search, reference, secret masking, announcements, or phone overflow failed",
		)
	}
	// Grant clipboard access only to this test's isolated browser context.
	var target struct {
		Info struct {
			Context string `json:"browserContextId"`
		} `json:"targetInfo"`
	}
	b.call(t, "Target.getTargetInfo", struct{}{}, &target)
	if err := b.wire.call("Browser.grantPermissions", "", map[string]any{
		"browserContextId": target.Info.Context,
		"permissions": []string{
			"clipboardReadWrite",
			"clipboardSanitizedWrite",
		},
	}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	b.evaluate(
		t,
		`document.querySelector('[data-copy=reference]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-variable-dialog]').textContent.includes('Copied os.environ.get')`,
	)
	if got := string(
		b.evaluate(t, `navigator.clipboard.readText()`),
	); got != `"os.environ.get(\"SECRET\")"` {
		t.Fatalf("copied unexpected text: %s", got)
	}
	b.evaluate(
		t,
		`(() => { const input=document.querySelector('[data-variable-search]'); input.value='definitely_missing'; input.dispatchEvent(new Event('input',{bubbles:true})); return true; })()`,
	)
	b.wait(
		t,
		`document.querySelectorAll('[data-variable-dialog] article').length === 0`,
	)
	b.evaluate(
		t,
		`(() => { const input=document.querySelector('[data-variable-search]'); input.value=''; input.dispatchEvent(new Event('input',{bubbles:true})); return true; })()`,
	)
	b.wait(
		t,
		`document.querySelectorAll('[data-variable-dialog] article').length > 3`,
	)
}

func checkViewerDraft(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const input=window.viewerTextarea;
 return window.viewerForm === document.querySelector('#step-edit-content form') &&
 JSON.stringify([...new FormData(window.viewerForm)]) === window.viewerSnapshot &&
 window.viewerPosition > 0 &&
 input.selectionStart === 7 && input.selectionEnd === 19 && input.scrollTop === window.viewerPosition &&
 window.viewerScrollArea.scrollTop === window.viewerScrollPosition &&
 document.activeElement === window.viewerOpener && document.querySelector('#step-edit-dialog').open;
})()`)) != "true" {
		t.Log(
			string(
				b.evaluate(
					t,
					`({formSame:window.viewerForm===document.querySelector('#step-edit-content form'),forms:[...document.querySelectorAll('#step-edit-content form')].map(x=>({same:x===window.viewerForm,html:x.outerHTML.slice(0,200)})),connected:window.viewerForm.isConnected,draftSame:window.viewerSnapshot===JSON.stringify([...new FormData(window.viewerForm)]),start:window.viewerTextarea.selectionStart,end:window.viewerTextarea.selectionEnd,top:window.viewerTextarea.scrollTop,beforeTop:window.viewerPosition,formTop:window.viewerScrollArea.scrollTop,beforeFormTop:window.viewerScrollPosition,focusSame:document.activeElement===window.viewerOpener,parentOpen:document.querySelector('#step-edit-dialog').open})`,
				),
			),
		)
		t.Fatal(
			"variable viewer changed draft, caret, scroll, focus, or parent dialog",
		)
	}
}

func checkViewerFailedLoad(t *testing.T, b *packageBrowser) {
	t.Helper()
	// Exercise the form insertion before the modal's after-settle handler runs.
	b.evaluate(
		t,
		`htmx.config.defaultSettleDelay = 1000; document.querySelector('[x-ref=addStepButton]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-dialog').matches(':modal') && document.querySelector('#step-edit-content form') && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	prepareViewerDraft(t, b, false)
	b.evaluate(
		t,
		`window.viewerState=Alpine.$data(document.querySelector('[data-step-variable-viewer]')); window.viewerURL=window.viewerState.url; window.viewerState.url='/login'; window.viewerOpener.click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-variable-dialog]').textContent.includes('session may have expired')`,
	)
	b.evaluate(
		t,
		`window.viewerState.url=window.viewerURL; [...document.querySelectorAll('[data-variable-dialog] button')].find(e=>e.textContent==='Retry').click(); true`,
	)
	waitVariableViewer(t, b)
	b.evaluate(t, `window.viewerClipboardWrite = navigator.clipboard.writeText;
navigator.clipboard.writeText = async () => { throw new Error('Clipboard denied'); };
document.querySelector('[data-variable-dialog] [data-copy=name]').click(); true`)
	b.wait(
		t,
		`document.querySelector('[data-variable-dialog]').textContent.includes('Copy unavailable. Select the name or reference and copy it manually.')`,
	)
	b.evaluate(
		t,
		`navigator.clipboard.writeText = window.viewerClipboardWrite; true`,
	)
	b.evaluate(
		t,
		`document.querySelector('[aria-label="Close variable viewer"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('[data-variable-dialog]').open && Alpine.$data(document.querySelector('[data-step-variable-viewer]')).opener === null`,
	)
	checkViewerDraft(t, b)
	if body := string(
		b.evaluate(t, `document.body.textContent`),
	); strings.Contains(
		body,
		"viewer-secret-sentinel",
	) {
		t.Fatal("secret appeared in the web response")
	}
}
