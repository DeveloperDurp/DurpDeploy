//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfiguredDefaultTimeoutBrowserE2E(t *testing.T) {
	f := newDefaultTimeoutFixture(t, 600)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/steps-page", f.baseURL, f.project.ID),
	)
	b.wait(
		t,
		`document.querySelector('[x-ref="addStepButton"]') && window.Alpine`,
	)
	b.evaluate(
		t,
		`document.querySelector('[x-ref="addStepButton"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content [name=timeout_seconds]')`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content').innerText.includes('0 = configured default')`,
	)
	b.evaluate(t, `(() => {
 const form=document.querySelector('#step-edit-content form');
 const fields={name:'Configured default',script_body:'echo default',container_image:'harbor.durp.info/dockerhub/library/bash:5.2',timeout_seconds:'0'};
 for (const [name,value] of Object.entries(fields)) form.querySelector('[name='+name+']').value=value;
 form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.querySelector('#step-list').textContent.includes('Configured default')`,
	)
	var listed struct {
		Items []struct {
			TimeoutSeconds int64 `json:"timeout_seconds"`
		}
	}
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/steps", nil, 200),
		&listed,
	)
	require.Len(t, listed.Items, 1)
	require.Zero(t, listed.Items[0].TimeoutSeconds)
	b.captureNavigation(t, "configured-default-timeout")
}
