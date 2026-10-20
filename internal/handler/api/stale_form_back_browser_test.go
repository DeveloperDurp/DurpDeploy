//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestStaleFormBackBrowserE2E(t *testing.T) {
	b := startPackageBrowser(t)
	for _, scope := range []string{"project", "global"} {
		t.Run(scope, func(t *testing.T) {
			f := newStaleProjectSaveFixture(t)
			path := fmt.Sprintf("/projects/%d", f.project.ID)
			f.request(
				t,
				"POST",
				"/api/v1"+path+"/steps",
				`{"name":"Denied execution","script_body":"echo unexpected","container_image":"harbor.durp.info/dockerhub/library/bash:5.2"}`,
				f.adminToken,
				"",
				false,
				201,
			)
			var release db.Release
			decodeStepLogTest(t, f.request(t, "POST",
				"/api/v1"+path+"/releases", `{"version":"stale-v1"}`,
				f.adminToken, "", false, 201), &release)
			env := seedEnv(t, f.h.repo)
			b.setBackTestSession(t, f.server.URL, f.session)
			b.navigateBackTest(t, f.server.URL+path)
			b.evaluate(t, `document.querySelector(
'#project-detail .page-header a[hx-get]').click(); true`)
			b.wait(t, `document.querySelector('#project-edit-dialog')
 ?.matches(':modal') && document.querySelector('[data-project-form] form')`)
			b.evaluate(t, `(() => {
window.staleForm = document.querySelector('[data-project-form] form');
staleForm.querySelector('[name=description]').value = 'Unsaved V02 draft';
return true;
})()`)

			// A second target in the same context shares the first tab's session.
			deploy := staleFormSecondTab(t, b)
			deploy.navigateBackTest(t, f.server.URL+path)
			deploy.evaluate(t, fmt.Sprintf(
				`document.querySelector('a[href=%q]').click(); true`,
				path+"/deploy"))
			deploy.waitBackTestURL(t, f.server.URL+path+"/deploy")
			deploy.wait(t, `document.querySelector('[name=release_id]')`)
			deploy.evaluate(t, fmt.Sprintf(`(() => {
const select = document.querySelector('[name=release_id]');
select.value = %q;
select.dispatchEvent(new Event('change', {bubbles:true}));
return true;
})()`, fmt.Sprint(release.ID)))
			selected := fmt.Sprintf("%s%s/deploy?release_id=%d",
				f.server.URL, path, release.ID)
			deploy.waitBackTestURL(t, selected)
			deploy.wait(t, `document.querySelector('[name=environment_id]')`)
			deploy.evaluate(t, fmt.Sprintf(`(() => {
const select = document.querySelector('[name=environment_id]');
select.value = %q;
select.dispatchEvent(new Event('change', {bubbles:true}));
return true;
})()`, fmt.Sprint(env.ID)))
			f.changeRole(t, scope, "viewer")
			for _, tab := range []*packageBrowser{b, deploy} {
				tab.evaluate(t, `(() => {
window.denial = null;
document.body.addEventListener('htmx:afterRequest', e => {
 const xhr = e.detail.xhr;
 window.denial = {status:xhr.status,
  toast:JSON.parse(xhr.getResponseHeader('HX-Trigger')).makeToast.level,
  swap:xhr.getResponseHeader('HX-Reswap'),
  push:xhr.getResponseHeader('HX-Push-Url')};
}, {once:true});
document.querySelector('[data-project-form] form, form[action$="/deploy"]')
 .requestSubmit();
return true;
})()`)
				tab.wait(t, `window.denial !== null`)
				t.Logf("%s denied stale form: %s", scope,
					tab.evaluate(t, `window.denial`))
				tab.wait(t, `denial.status === 200 && denial.toast === 'danger'
 && denial.swap === 'none' && denial.push === 'false' && (() => {
const toast = Alpine.$data(document.querySelector('[x-data=toast]'));
return toast.visible && toast.type === 'error';
})()`)
			}
			b.wait(t, `staleForm.isConnected &&
 staleForm.querySelector('[name=description]').value === 'Unsaved V02 draft'
 && staleForm.querySelector('[role=alert]')?.getClientRects().length > 0`)
			f.assertDescription(t, f.project.Description.String)
			apiBody := fmt.Sprintf(`{"release_id":%d,"environment_id":%d}`,
				release.ID, env.ID)
			f.request(t, "POST", "/api/v1"+path+"/deployments", apiBody,
				f.token, "", false, 403)

			deploy.call(t, "Page.reload", struct{}{}, &struct{}{})
			deploy.wait(t, `!document.querySelector('form[action$="/deploy"]')
 && document.querySelector('#page-content')?.textContent
 .includes('cannot create a deployment')`)
			deploy.clickBackTest(t)
			deploy.waitBackTestURL(t, f.server.URL+path+"/deploy")
			deploy.wait(t, `document.querySelector('#page-content')?.textContent
 .includes('cannot create a deployment')`)
			t.Log("First Go back revisits the earlier denied deployment URL")
			deploy.screenshot(t, "stale-form-first-back-"+scope)
			deploy.clickBackTest(t)
			deploy.waitBackTestURL(t, f.server.URL+path)
			deploy.wait(t, `document.querySelector('#project-detail') &&
 !document.querySelector('#project-detail a[href$="/deploy"]')`)
			t.Log("Second Go back reaches the project")

			// A single form entry requires only one Go back.
			deploy.navigateBackTest(t, f.server.URL+path+"/deploy")
			deploy.clickBackTest(t)
			deploy.waitBackTestURL(t, f.server.URL+path)
			deploy.wait(t, `document.querySelector('#project-detail')`)
			var deployments struct{ Total int }
			decodeStepLogTest(t, f.request(t, "GET",
				"/api/v1"+path+"/deployments", "", f.adminToken, "",
				false, 200), &deployments)
			if deployments.Total != 0 {
				t.Fatal("denied stale forms created a deployment")
			}
			f.changeRole(t, scope, "maintainer")
			b.evaluate(t, `staleForm.requestSubmit(); true`)
			b.wait(t, `!document.querySelector('#project-edit-dialog')?.open &&
 document.querySelector('#project-detail')?.textContent
 .includes('Unsaved V02 draft')`)
			f.assertDescription(t, "Unsaved V02 draft")
		})
	}
}

func staleFormSecondTab(t *testing.T, first *packageBrowser) *packageBrowser {
	t.Helper()
	var info struct {
		TargetInfo struct {
			Context string `json:"browserContextId"`
		} `json:"targetInfo"`
	}
	first.call(t, "Target.getTargetInfo", struct{}{}, &info)
	var created struct {
		ID string `json:"targetId"`
	}
	if err := first.wire.call("Target.createTarget", "", map[string]string{
		"url": "about:blank", "browserContextId": info.TargetInfo.Context,
	}, &created); err != nil {
		t.Fatal(err)
	}
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := first.wire.call("Target.attachToTarget", "", map[string]any{
		"targetId": created.ID, "flatten": true,
	}, &attached); err != nil {
		t.Fatal(err)
	}
	// The original browser fixture disposes this shared context and both tabs.
	b := &packageBrowser{first.wire, attached.Session}
	b.call(t, "Page.enable", struct{}{}, &struct{}{})
	b.call(t, "Network.enable", struct{}{}, &struct{}{})
	return b
}
