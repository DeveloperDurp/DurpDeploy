//go:build e2e && agentbrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestAgentContainerBrowserE2E(t *testing.T) {
	h := newAPIHarness(t)
	project := seedProject(t, h.repo)
	user := seedAPIUser(t, h.repo, "container-browser@test.local", "admin")
	if _, err := h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: fmt.Sprint(user.ID), UserID: user.ID, CsrfToken: "container-browser",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	browser := startPackageBrowser(t)
	fleetBrowserSession(t, browser, srv.URL, user.ID)
	base := fmt.Sprintf("/projects/%d", project.ID)
	set := func(name, value string) {
		t.Helper()
		browser.evaluate(t, fmt.Sprintf(`(() => {
			const e = document.querySelector('[name=%q]:not([type=hidden]):not(:disabled)');
			e.value = %q; e.dispatchEvent(new Event('input', {bubbles:true}));
			e.dispatchEvent(new Event('change', {bubbles:true})); return true;
		})()`, name, value))
	}
	capture := func(name string) {
		t.Helper()
		for _, width := range []int{375, 768, 1280} {
			browser.call(
				t,
				"Emulation.setDeviceMetricsOverride",
				map[string]any{
					"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
				},
				&struct{}{},
			)
			browser.screenshot(t, fmt.Sprintf("container-%s-%d", name, width))
			if string(
				browser.evaluate(
					t,
					`document.documentElement.scrollWidth > innerWidth`,
				),
			) != "false" {
				t.Fatalf(
					"%s overflows at %d: %s",
					name,
					width,
					browser.evaluate(
						t,
						`Array.from(document.querySelectorAll('body *')).filter(e=>e.getBoundingClientRect().right>innerWidth).map(e=>({tag:e.tagName,id:e.id,cls:e.className,width:e.getBoundingClientRect().width})).slice(0,20)`,
					),
				)
			}
		}
	}
	// Host remains the default; opting in enables the required image input.
	fleetBrowserNavigate(t, browser, srv.URL+base+"/steps-page")
	browser.evaluate(
		t,
		`document.querySelector('button[hx-get$="/steps/new"]').click(); true`,
	)
	browser.wait(
		t,
		`document.querySelector('[name=agent_execution_mode]') !== null`,
	)
	set("execution_target", "agent")
	browser.wait(
		t,
		`document.querySelector('[name=agent_execution_mode]').value === 'host' && document.querySelector('[name=container_image]').disabled`,
	)
	browser.screenshot(t, "container-step-host-default")
	set("agent_execution_mode", "container")
	browser.wait(
		t,
		`document.querySelector('[name=container_image]').required && !document.querySelector('[name=container_image]').disabled`,
	)
	set("name", "browser-container")
	set("script_body", "echo browser-container")
	set("container_image", "alpine:3.20")
	set("variable_names", "DEPLOY_ENV")
	capture("step-new")
	browser.evaluate(
		t,
		`document.querySelector('form[data-step-add-form] button[type=submit]').click(); true`,
	)
	browser.wait(
		t,
		`document.querySelector('form[data-step-add-form]') === null && document.body.innerText.includes('browser-container')`,
	)
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 ||
		steps[0].AgentExecutionMode != "container" ||
		steps[0].VariableNames != `["DEPLOY_ENV"]` {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
	// The table and mobile edit components both retain the saved mode.
	for _, width := range []int{375, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		fleetBrowserNavigate(t, browser, srv.URL+base+"/steps-page")
		browser.evaluate(
			t,
			`Array.from(document.querySelectorAll('[hx-get*="/edit"]')).find(e => e.getBoundingClientRect().width > 0).click(); true`,
		)
		browser.wait(
			t,
			`document.querySelector('form [name=agent_execution_mode]')?.value === 'container' && document.querySelector('form [name=container_image]')?.value === 'alpine:3.20'`,
		)
		browser.screenshot(t, fmt.Sprintf("container-step-edit-%d", width))
	}
	// A native template form saves and reopens container placement.
	fleetBrowserNavigate(t, browser, srv.URL+"/templates/new")
	set("execution_target", "agent")
	set("agent_execution_mode", "container")
	set("name", "browser-template")
	set("script_body", "echo template")
	set("container_image", "alpine:3.20")
	set("variable_names", "DEPLOY_ENV")
	capture("template-new")
	browser.wire.events = nil
	browser.evaluate(
		t,
		`document.querySelector('form[action="/templates"] button[type=submit]').click(); true`,
	)
	fleetBrowserLoaded(t, browser)
	browser.wait(
		t,
		`location.pathname === '/templates' && document.body.innerText.includes('browser-template')`,
	)
	fleetBrowserNavigate(t, browser, srv.URL+"/templates/1/edit")
	browser.wait(
		t,
		`document.querySelector('[name=agent_execution_mode]').value === 'container'`,
	)
	capture("template-edit")
	set("agent_execution_mode", "host")
	browser.wait(t, `document.querySelector('[name=container_image]') === null`)
	browser.evaluate(
		t,
		`document.querySelector('form[hx-put="/templates/1"] button[type=submit]').click(); true`,
	)
	browser.wait(
		t,
		`document.body.innerText.includes('Template updated')`,
	)
	versions, err := h.repo.Queries.ListStepTemplateVersions(t.Context(), 1)
	if err != nil || len(versions) != 2 ||
		versions[0].AgentExecutionMode != "host" ||
		versions[1].AgentExecutionMode != "container" {
		t.Fatalf("versions=%+v error=%v", versions, err)
	}
	// Dynamic runbook arrays preserve each step's mode and empty host image.
	fleetBrowserNavigate(t, browser, srv.URL+base+"/runbooks/new")
	browser.wait(t, `document.querySelector('[name=step_agent_mode]') !== null`)
	set("name", "browser-runbook")
	set("step_name", "container")
	set("step_script", "echo container")
	set("step_target", "agent")
	set("step_agent_mode", "container")
	set("step_image", "alpine:3.20")
	set("step_variable_names", "DEPLOY_ENV")
	capture("runbook-new")
	browser.evaluate(
		t,
		`Array.from(document.querySelectorAll('button')).find(e => e.textContent.trim() === 'Add step').click(); true`,
	)
	browser.wait(
		t,
		`document.querySelectorAll('[name=step_name]').length === 2`,
	)
	browser.evaluate(t, `(() => {
		for (const [name,value] of [['step_name','host'],['step_script','echo host'],['step_target','agent']]) {
			const e=document.querySelectorAll('[name='+name+']')[1]; e.value=value;
			e.dispatchEvent(new Event('input',{bubbles:true})); e.dispatchEvent(new Event('change',{bubbles:true}));
		} return true;
	})()`)
	browser.wait(
		t,
		`document.querySelectorAll('[name=step_image]:not([type=hidden])')[1].disabled`,
	)
	browser.wire.events = nil
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/runbooks"] button[type=submit]').click(); true`,
	)
	fleetBrowserLoaded(t, browser)
	version, err := h.repo.Queries.GetLatestRunbookVersion(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.GetRelease(t.Context(), version.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot []struct {
		AgentExecutionMode string `json:"agent_execution_mode"`
		ContainerImage     string `json:"container_image"`
	}
	if err := json.Unmarshal([]byte(release.StepsJson), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 || snapshot[0].AgentExecutionMode != "container" ||
		snapshot[1].AgentExecutionMode != "host" ||
		snapshot[1].ContainerImage != "" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	fleetBrowserNavigate(t, browser, srv.URL+base+"/runbooks/1/edit")
	browser.wait(
		t,
		`document.querySelector('[name=step_agent_mode]:not(:disabled)').value === 'container' && document.querySelector('[name=step_variable_names]').value === 'DEPLOY_ENV'`,
	)
	capture("runbook-edit")
}
