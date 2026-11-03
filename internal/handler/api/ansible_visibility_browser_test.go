//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestAnsibleDeployOptionVisibilityBrowserE2E(t *testing.T) {
	f, definition, _, _ := newAnsibleE2E(t)
	var script, native struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", map[string]string{
			"name": "Script", "script_body": "echo script",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 201),
		&script,
	)
	scriptRelease := verificationRelease(t, f, "visibility-script")
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", definition, 201),
		&native,
	)
	mixedRelease := verificationRelease(t, f, "visibility-mixed")
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/steps/%d", f.base(), script.ID),
		nil,
		204,
	)
	nativeRelease := verificationRelease(t, f, "visibility-ansible")
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("%s/projects/%d/deploy", f.baseURL, f.project.ID)
	b.navigateBackTest(t, path)
	b.wait(
		t,
		`document.querySelector('[name=release_id]') && window.Alpine && !document.querySelector('[name=ansible_check_mode]')`,
	)
	selectAnsibleVisibilityRelease(t, b, path, scriptRelease.ID, false)
	b.captureNavigation(t, "ansible-option-hidden-script")
	selectAnsibleVisibilityRelease(t, b, path, mixedRelease.ID, true)
	b.evaluate(
		t,
		`document.querySelector('[name=ansible_check_mode]').checked=true; true`,
	)
	selectAnsibleVisibilityRelease(t, b, path, scriptRelease.ID, false)
	// Current project edits cannot change which saved releases contain Ansible.
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/steps/%d", f.base(), native.ID),
		nil,
		204,
	)
	selectAnsibleVisibilityRelease(t, b, path, nativeRelease.ID, true)
	b.captureNavigation(t, "ansible-option-visible-snapshot")
	selectAnsibleVisibilityRelease(t, b, path, mixedRelease.ID, true)
	b.evaluate(
		t,
		`const select=document.querySelector('[name=release_id]'); select.value=''; select.dispatchEvent(new Event('change',{bubbles:true})); true`,
	)
	b.waitBackTestURL(t, path+"?release_id=")
	b.wait(
		t,
		`document.querySelector('[name=release_id]') && window.Alpine && !document.querySelector('[name=ansible_check_mode]')`,
	)
}

func selectAnsibleVisibilityRelease(
	t *testing.T,
	b *packageBrowser,
	path string,
	releaseID int64,
	visible bool,
) {
	t.Helper()
	b.evaluate(
		t,
		fmt.Sprintf(
			`(() => {const select=document.querySelector('[name=release_id]'); select.value='%d'; select.dispatchEvent(new Event('change',{bubbles:true})); return true;})()`,
			releaseID,
		),
	)
	b.waitBackTestURL(t, fmt.Sprintf("%s?release_id=%d", path, releaseID))
	b.wait(t, `document.querySelector('[name=release_id]') && window.Alpine`)
	if visible {
		b.wait(
			t,
			`document.querySelector('[name=ansible_check_mode]') && !document.querySelector('[name=ansible_check_mode]').checked`,
		)
	} else {
		b.wait(t, `!document.querySelector('[name=ansible_check_mode]')`)
	}
}
