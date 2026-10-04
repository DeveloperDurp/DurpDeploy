//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactGateBrowserE2E(t *testing.T) {
	// Given: an API-generated artifact waiting for administrator review.
	f, deployment, _ := newGateDeployment(t)
	f.api(t, "PUT", f.base(), map[string]string{"name": "Artifact review"}, 200)
	browser := startPackageBrowser(t)
	var cookie struct {
		Success bool `json:"success"`
	}
	browser.call(
		t,
		"Network.setCookie",
		map[string]any{
			"name":     "session",
			"value":    f.session,
			"url":      f.baseURL,
			"httpOnly": true,
		},
		&cookie,
	)
	if !cookie.Success {
		t.Fatal("session cookie rejected")
	}
	page := fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID)
	for _, width := range []int{375, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width":             width,
			"height":            900,
			"deviceScaleFactor": 1,
			"mobile":            false,
		}, &struct{}{})
		browser.call(
			t,
			"Page.navigate",
			map[string]string{"url": f.baseURL + "/"},
			&struct{}{},
		)
		browser.wait(
			t,
			`Array.from(document.querySelectorAll('h2')).find(e=>e.textContent==='Currently running')?.parentElement.innerText.includes('awaiting_artifact_approval')`,
		)
		if string(
			browser.evaluate(
				t,
				`Array.from(document.querySelectorAll('table .badge')).every(b=>b.getBoundingClientRect().right<=b.closest('td').getBoundingClientRect().right)`,
			),
		) != "true" {
			t.Fatal("dashboard status badge overlaps the date column")
		}
		browser.screenshot(
			t,
			fmt.Sprintf("artifact-dashboard-waiting-%d", width),
		)
		for _, formPage := range []string{fmt.Sprintf("/projects/%d/steps-page", f.project.ID), "/templates/new"} {
			browser.call(
				t,
				"Page.navigate",
				map[string]string{"url": f.baseURL + formPage},
				&struct{}{},
			)
			if formPage != "/templates/new" {
				browser.wait(
					t,
					`document.querySelector('[hx-get$="/steps/new"]') && window.htmx && document.readyState==='complete'`,
				)
				browser.evaluate(
					t,
					`document.querySelector('[hx-get$="/steps/new"]').click()`,
				)
			}
			browser.wait(
				t,
				`document.querySelector('[name="approval_artifact_path"]') && window.htmx && document.readyState==='complete'`,
			)
			browser.wire.events = nil
			browser.evaluate(t, `(() => {
const form=document.querySelector('[name="approval_artifact_path"]').form;
form.querySelector('[name="name"]').value='Invalid gate';
const script=form.querySelector('[name="script_body"]'); script.value='true'; script.dispatchEvent(new Event('input',{bubbles:true}));
form.querySelector('[name="container_image"]').value='docker.io/library/bash:5.2';
form.querySelector('[name="variable_names"]').value='KEEP_THIS';
form.querySelector('[name="approval_artifact_path"]').value='../plan';
form.querySelector('[name="approval_review_path"]').value='review';
form.querySelector('[name="approval_review_format"]').value='summary';
setTimeout(()=>form.requestSubmit(),100); return true;
})()`)
			if formPage == "/templates/new" {
				if err := browser.wire.waitEvent(
					"Page.loadEventFired",
					browser.session,
				); err != nil {
					t.Fatal(err)
				}
			}
			browser.wait(
				t,
				`document.body.innerText.includes('invalid artifact approval configuration') && document.querySelector('[name="container_image"]').value==='docker.io/library/bash:5.2' && document.querySelector('[name="variable_names"]').value==='KEEP_THIS'`,
			)
			browser.screenshot(
				t,
				fmt.Sprintf(
					"artifact-invalid-form-%d-%d",
					width,
					len(formPage),
				),
			)
		}
		browser.call(t, "Page.navigate", map[string]string{
			"url": fmt.Sprintf(
				"%s/projects/%d/steps-page",
				f.baseURL,
				f.project.ID,
			),
		}, &struct{}{})
		browser.wait(
			t,
			`window.htmx && document.readyState==='complete' && Array.from(document.querySelectorAll('[data-step-action="edit"]')).some(e=>e.getClientRects().length)`,
		)
		browser.evaluate(
			t,
			`Array.from(document.querySelectorAll('[data-step-action="edit"]')).find(e=>e.getClientRects().length).click()`,
		)
		browser.wait(
			t,
			`Array.from(document.querySelectorAll('[name="approval_artifact_path"]')).some(e=>e.getClientRects().length)`,
		)
		browser.evaluate(t, `(() => {
const field=Array.from(document.querySelectorAll('[name="approval_artifact_path"]')).find(e=>e.getClientRects().length);
const form=field.form; field.value='../plan';
form.querySelector('[name="variable_names"]').value='KEEP_EDIT';
setTimeout(()=>form.requestSubmit(),100); return true;
})()`)
		browser.wait(
			t,
			`Array.from(document.querySelectorAll('[name="approval_artifact_path"]')).some(e=>e.getClientRects().length && e.value==='../plan' && e.form.innerText.includes('invalid artifact approval configuration') && e.form.querySelector('[name="variable_names"]').value==='KEEP_EDIT') && document.querySelector('#step-list')`,
		)
		browser.screenshot(t, fmt.Sprintf("artifact-invalid-edit-%d", width))
	}
	var agentTemplate struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(
		f.api(
			t,
			"POST",
			"/api/v1/templates",
			map[string]any{
				"name":             "Agent template",
				"script_body":      "true",
				"execution_target": "agent",
			},
			201,
		),
		&agentTemplate,
	); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{375, 768, 1280} {
		browser.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            900,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		browser.call(
			t,
			"Page.navigate",
			map[string]string{"url": page},
			&struct{}{},
		)
		browser.wait(
			t,
			`document.querySelector('section[aria-label="Artifact approval"]') && document.querySelector('form[action$="/approve"]')`,
		)
		browser.wait(
			t,
			`document.querySelector('section[aria-label="Artifact approval"]').innerText.includes('Unverified summary supplied by the deployment step.')`,
		)
		if string(
			browser.evaluate(
				t,
				`document.documentElement.scrollWidth <= innerWidth`,
			),
		) != "true" {
			t.Fatal("horizontal overflow")
		}
		if string(
			browser.evaluate(
				t,
				`document.body.innerText.includes('generated-sensitive-value') || document.body.innerText.includes('exact-approved-plan-secret')`,
			),
		) != "false" {
			t.Fatal("sensitive content leaked")
		}
		browser.screenshot(t, fmt.Sprintf("artifact-review-%d", width))
	}
	// Polling must preserve the keyboard user's focused decision control.
	if string(
		browser.evaluate(
			t,
			`new Promise(resolve => { const button = document.querySelector('form[action$="/approve"] button'); button.focus(); setTimeout(() => resolve(document.activeElement === button), 3500); })`,
		),
	) != "true" {
		t.Fatal("approval polling discarded keyboard focus")
	}
	browser.screenshot(t, "artifact-focused-approval")
	// When: the actual web form approves this checksum and revision.
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/approve"] button').click()`,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	// Then: the review becomes approved and apply completes.
	browser.wait(
		t,
		`document.querySelector('section[aria-label="Artifact approval"]')?.innerText.includes('approved')`,
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge')?.innerText.includes('succeeded')`,
	)
	browser.wait(t, `!document.querySelector('[hx-post$="/cancel"]')`)
	browser.screenshot(t, "artifact-approved")
	browser.wait(
		t,
		`!document.querySelector('[data-artifact-gates]').hasAttribute('hx-trigger')`,
	)
	// HTMX cancellation must swap only the badge, then stop gate polling.
	release, err := f.h.repo.Queries.GetDeploymentRelease(
		t.Context(), deployment.ReleaseID,
	)
	if err != nil {
		t.Fatal(err)
	}
	paused := verificationDeploy(t, f, release)
	f.completion(t, paused.ID, events.ArtifactAwaitingApproval)
	browser.call(t, "Page.navigate", map[string]string{
		"url": fmt.Sprintf("%s/deployments/%d", f.baseURL, paused.ID),
	}, &struct{}{})
	browser.wait(t, `document.querySelector('form[action$="/approve"]')`)
	browser.evaluate(
		t,
		`document.querySelector('[hx-post$="/cancel"]').click()`,
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge')?.innerText.includes('cancelled')`,
	)
	browser.wait(t, `!document.querySelector('[hx-post$="/cancel"]')`)
	browser.wait(
		t,
		`!document.querySelector('[data-artifact-gates]').hasAttribute('hx-trigger')`,
	)
	if string(
		browser.evaluate(t, `document.querySelectorAll('h1').length`),
	) != "1" {
		t.Fatal("cancellation swapped a full page into the status badge")
	}
	browser.screenshot(t, "artifact-cancelled")
	// And: configuration fields render at mobile and desktop widths.
	for _, width := range []int{375, 1280} {
		browser.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            900,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		for _, surface := range []struct{ name, path, field string }{
			{"step", fmt.Sprintf("/projects/%d/steps-page", f.project.ID), "network_mode"},
			{"template", "/templates/new", "network_mode"},
			{"runbook", fmt.Sprintf("/projects/%d/runbooks/new", f.project.ID), "step_network"},
		} {
			browser.call(
				t,
				"Page.navigate",
				map[string]string{"url": f.baseURL + surface.path},
				&struct{}{},
			)
			browser.wait(
				t,
				`document.readyState === 'complete' && window.htmx !== undefined`,
			)
			if surface.name == "step" {
				browser.evaluate(
					t,
					`document.querySelector('[hx-get$="/steps/new"]').click()`,
				)
			}
			browser.wait(
				t,
				fmt.Sprintf(
					`document.querySelector('[name="%s"]')`,
					surface.field,
				),
			)
			browser.screenshot(
				t,
				fmt.Sprintf("artifact-%s-form-%d", surface.name, width),
			)
			if string(
				browser.evaluate(
					t,
					`document.documentElement.scrollWidth <= innerWidth`,
				),
			) != "true" {
				t.Fatalf(
					"%s configuration horizontal overflow: %s",
					surface.name,
					browser.evaluate(
						t,
						`Array.from(document.querySelectorAll('body *')).filter(e=>e.getBoundingClientRect().right>innerWidth+1).map(e=>({tag:e.tagName,class:e.className,width:e.clientWidth})).slice(-12)`,
					),
				)
			}
			if surface.name == "runbook" {
				browser.evaluate(
					t,
					`const network = document.querySelector('select[name="step_network"]'); network.value = 'bridge'; network.dispatchEvent(new Event('change', {bubbles:true}));`,
				)
				browser.evaluate(
					t,
					`const target = document.querySelector('[name="step_target"]'); target.value = 'agent'; target.dispatchEvent(new Event('change', {bubbles:true}));`,
				)
				browser.wait(
					t,
					`document.querySelector('select[name="step_network"]').disabled && document.querySelector('select[name="step_network"]').value === ''`,
				)
				if string(
					browser.evaluate(
						t,
						`new FormData(document.querySelector('form[x-data]')).getAll('step_network').join(',') === ''`,
					),
				) != "true" {
					t.Fatal("agent form retains network access")
				}
				browser.screenshot(
					t,
					fmt.Sprintf("artifact-agent-runbook-%d", width),
				)
				browser.evaluate(
					t,
					fmt.Sprintf(
						`for (const [name,value] of [['name','Agent runbook %d'],['step_name','Agent'],['step_script','true'],['step_selectors','']]) { const field=document.querySelector('[name="'+name+'"]'); field.value=value; field.dispatchEvent(new Event('input',{bubbles:true})); } setTimeout(() => document.querySelector('form[x-data]').requestSubmit(), 100); true`,
						width,
					),
				)
				browser.wire.events = nil
				if err := browser.wire.waitEvent(
					"Page.loadEventFired",
					browser.session,
				); err != nil {
					t.Fatal(err)
				}
				browser.wait(
					t,
					`location.pathname.match(/\/runbooks\/\d+$/) && document.readyState === 'complete'`,
				)
				var books []db.Runbook
				if err := json.Unmarshal(
					f.api(t, "GET", f.base()+"/runbooks", nil, 200),
					&books,
				); err != nil {
					t.Fatal(err)
				}
				if len(books) == 0 {
					t.Fatal("agent runbook form did not save")
				}
				found := false
				for _, book := range books {
					if book.Name != fmt.Sprintf("Agent runbook %d", width) {
						continue
					}
					found = true
					var detail struct {
						Versions []db.RunbookVersion `json:"versions"`
					}
					bookPath := fmt.Sprintf("%s/runbooks/%d", f.base(), book.ID)
					if err := json.Unmarshal(
						f.api(t, "GET", bookPath, nil, 200),
						&detail,
					); err != nil ||
						len(detail.Versions) != 1 {
						t.Fatalf("saved versions=%+v err=%v", detail, err)
					}
					var version struct {
						Steps []struct {
							ExecutionTarget string `json:"execution_target"`
							NetworkMode     string `json:"network_mode"`
						} `json:"steps"`
					}
					if err := json.Unmarshal(
						f.api(
							t,
							"GET",
							fmt.Sprintf(
								"%s/versions/%d",
								bookPath,
								detail.Versions[0].ID,
							),
							nil,
							200,
						),
						&version,
					); err != nil || len(version.Steps) != 1 || version.Steps[0].ExecutionTarget != "agent" ||
						version.Steps[0].NetworkMode != "" {
						t.Fatalf("saved agent network=%+v err=%v", version, err)
					}
				}
				if !found {
					t.Fatal("saved agent runbook missing from public API")
				}
			}
			if surface.name == "step" {
				browser.evaluate(
					t,
					`const form=document.querySelector('form[data-step-add-form]'); form.querySelector('[name="network_mode"]').value='bridge'; form.querySelector('[name="approval_artifact_path"]').value='plan'; const target=form.querySelector('[name="execution_target"]'); target.value='agent'; target.dispatchEvent(new Event('change',{bubbles:true}));`,
				)
				browser.wait(
					t,
					`document.querySelector('form[data-step-add-form] fieldset').disabled`,
				)
				if string(
					browser.evaluate(
						t,
						`!new FormData(document.querySelector('form[data-step-add-form]')).has('network_mode') && !new FormData(document.querySelector('form[data-step-add-form]')).has('approval_artifact_path')`,
					),
				) != "true" {
					t.Fatal("agent step submits unsupported artifact controls")
				}
				browser.screenshot(
					t,
					fmt.Sprintf("artifact-agent-step-%d", width),
				)
				browser.evaluate(
					t,
					fmt.Sprintf(
						`(() => { const form=document.querySelector('form[data-step-add-form]'); form.querySelector('[name="name"]').value='Agent step %d'; const script=form.querySelector('[name="script_body"]'); script.value='true'; script.dispatchEvent(new Event('input',{bubbles:true})); form.requestSubmit(); return true; })()`,
						width,
					),
				)
				browser.wait(
					t,
					`!document.querySelector('form[data-step-add-form]')`,
				)
				var steps struct {
					Items []struct {
						Name                 string `json:"name"`
						ExecutionTarget      string `json:"execution_target"`
						NetworkMode          string `json:"network_mode"`
						ApprovalArtifactPath string `json:"approval_artifact_path"`
					} `json:"items"`
				}
				if err := json.Unmarshal(
					f.api(t, "GET", f.base()+"/steps", nil, 200),
					&steps,
				); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, step := range steps.Items {
					if step.Name == fmt.Sprintf("Agent step %d", width) {
						found = true
						if step.ExecutionTarget != "agent" ||
							step.NetworkMode != "" ||
							step.ApprovalArtifactPath != "" {
							t.Fatalf("saved agent step=%+v", step)
						}
					}
				}
				if !found {
					t.Fatal("saved agent step missing from API")
				}
			}
			if surface.name == "template" {
				browser.call(
					t,
					"Page.navigate",
					map[string]string{
						"url": fmt.Sprintf(
							"%s/templates/%d/edit",
							f.baseURL,
							agentTemplate.ID,
						),
					},
					&struct{}{},
				)
				browser.wait(
					t,
					`document.querySelector('form[hx-put]') && document.readyState === 'complete' && window.htmx`,
				)
				if string(
					browser.evaluate(
						t,
						`document.querySelector('[name="network_mode"], [name="approval_artifact_path"], [name="approval_review_path"], [name="approval_review_format"]') === null`,
					),
				) != "true" {
					t.Fatal("agent template offers unsupported gate fields")
				}
				browser.screenshot(
					t,
					fmt.Sprintf("artifact-agent-template-%d", width),
				)
				browser.evaluate(
					t,
					fmt.Sprintf(
						`const form=document.querySelector('form[hx-put]'); form.querySelector('[name="name"]').value='Agent template %d'; form.requestSubmit();`,
						width,
					),
				)
				browser.wait(
					t,
					`document.body.innerText.includes('Template updated')`,
				)
				var saved struct {
					Name            string `json:"name"`
					ExecutionTarget string `json:"execution_target"`
					NetworkMode     string `json:"network_mode"`
				}
				if err := json.Unmarshal(
					f.api(
						t,
						"GET",
						fmt.Sprintf("/api/v1/templates/%d", agentTemplate.ID),
						nil,
						200,
					),
					&saved,
				); err != nil || saved.Name != fmt.Sprintf("Agent template %d", width) || saved.ExecutionTarget != "agent" ||
					saved.NetworkMode != "" {
					t.Fatalf("saved agent template=%+v err=%v", saved, err)
				}
			}
		}
	}
	for _, width := range []int{375, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900,
			"deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		browser.call(t, "Page.navigate", map[string]string{
			"url": f.baseURL + "/deployments?status=awaiting_artifact_approval",
		}, &struct{}{})
		browser.wait(
			t,
			`document.querySelector('[name="status"]')?.value === 'awaiting_artifact_approval'`,
		)
		browser.screenshot(t, fmt.Sprintf("artifact-status-filter-%d", width))
	}
	for _, terminal := range []string{"rejected", "expired"} {
		next := verificationDeploy(t, f, db.Release{ID: deployment.ReleaseID})
		f.completion(t, next.ID, events.ArtifactAwaitingApproval)
		browser.call(
			t,
			"Page.navigate",
			map[string]string{
				"url": fmt.Sprintf("%s/deployments/%d", f.baseURL, next.ID),
			},
			&struct{}{},
		)
		browser.wait(
			t,
			`window.Alpine && document.querySelector('[x-data^="deploymentStream"]') && Alpine.$data(document.querySelector('[x-data^="deploymentStream"]')).source !== null`,
		)
		if terminal == "rejected" {
			var gates []struct {
				SHA256 string `json:"sha256"`
			}
			if err := json.Unmarshal(
				f.api(t, "GET", gateAPIPath(next.ID), nil, 200),
				&gates,
			); err != nil ||
				len(gates) != 1 {
				t.Fatalf("gate=%+v err=%v", gates, err)
			}
			f.api(
				t,
				"POST",
				gateAPIPath(next.ID)+"/0/reject",
				map[string]any{"revision": 1, "sha256": gates[0].SHA256},
				200,
			)
		} else {
			if _, err := f.h.repo.DB.Exec(
				"UPDATE artifact_gates SET expires_at=0 WHERE deployment_id=?",
				next.ID,
			); err != nil {
				t.Fatal(err)
			}
			f.api(t, "GET", gateAPIPath(next.ID), nil, 200)
		}
		browser.wait(
			t,
			fmt.Sprintf(
				`document.querySelector('#status-badge')?.innerText.includes('%s') && Alpine.$data(document.querySelector('[x-data^="deploymentStream"]')).source === null`,
				terminal,
			),
		)
		browser.wait(
			t,
			`!document.querySelector('[data-artifact-gates]').hasAttribute('hx-trigger')`,
		)
		browser.screenshot(t, "artifact-stream-"+terminal)
	}
	var gates json.RawMessage
	if err := json.Unmarshal(
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	); err != nil {
		t.Fatal(err)
	}
}
