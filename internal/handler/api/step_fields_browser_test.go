//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/stepdefinition"
)

func TestStepFieldsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	want := stepdefinition.Definition{
		Name: "field preservation", ScriptBody: "echo original", Interpreter: "python3",
		SortOrder: 7, TimeoutSeconds: 4321, MaxRetries: 4,
		ExecutionTarget: "local", AgentExecutionMode: "host",
		ContainerImage: "harbor.durp.info/dockerhub/library/python:3.12",
		NetworkMode:    "bridge", VariableNames: []string{"API_TOKEN", "DEPLOY_ENV"},
		ApprovalArtifactPath: "plan.bin", ApprovalReviewPath: "review.json",
		ApprovalReviewFormat: "summary",
	}
	var created struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", want, 201),
		&created,
	)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/steps-page", f.baseURL, f.project.ID),
	)
	b.wait(
		t,
		`document.querySelector('[data-step-action="edit"]') && window.Alpine`,
	)
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-step-action="edit"]')].find(el=>el.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content [name=name]')`,
	)
	if string(b.evaluate(t, `(() => {
 const form=document.querySelector('#step-edit-content form');
 const expected={interpreter:'python3',timeout_seconds:'4321',max_retries:'4',network_mode:'bridge',approval_artifact_path:'plan.bin',approval_review_path:'review.json',approval_review_format:'summary'};
 return Object.entries(expected).every(([name,value])=>form.querySelector('[name='+name+']')?.value===value)
  && form.querySelector('[name=variable_names]').value.includes('API_TOKEN');
})()`)) != "true" {
		t.Fatal("step editor did not load persisted fields")
	}
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content [name=script_body]').value='print("edited")'; document.querySelector('#step-edit-content form').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	want.ScriptBody = `print("edited")`
	assertStepFields(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/steps/%d", f.base(), created.ID),
			nil,
			200,
		),
		want,
	)
	b.captureNavigation(t, "step-fields-preserved")
}
