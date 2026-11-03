//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/stepdefinition"
)

func TestAnsibleBrowserE2E(t *testing.T) {
	f, definition, private, target := newAnsibleE2E(t)
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
		`document.querySelector('#step-edit-content form[data-step-add-form]') && document.querySelector('#step-edit-dialog').open && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	b.evaluate(
		t,
		`const kind=document.querySelector('#step-edit-content [name=kind]'); kind.value='ansible'; kind.dispatchEvent(new Event('change',{bubbles:true})); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content [name=script_body]')?.disabled`,
	)
	mappings, _ := json.Marshal(definition.Ansible.ExtraVars)
	values, _ := json.Marshal(map[string]string{
		"name": definition.Name, "container_image": definition.ContainerImage, "network_mode": "bridge",
		"ansible_playbook": "[invalid", "ansible_inventory": definition.Ansible.Inventory,
		"ansible_extra_vars": string(
			mappings,
		), "ansible_ssh_key_variable": "SSH_KEY",
		"ansible_known_hosts": definition.Ansible.KnownHosts, "ansible_tags": "deploy", "ansible_limit": "target",
	})
	b.evaluate(
		t,
		`(() => { const form=document.querySelector('#step-edit-content form'); const values=`+string(
			values,
		)+`; for(const [name,value] of Object.entries(values)) form.elements.namedItem(name).value=value; form.requestSubmit(); return true; })()`,
	)
	b.wait(t, `document.querySelector('#step-edit-content [data-form-error]')`)
	t.Log(
		"form validation:",
		string(
			b.evaluate(
				t,
				`document.querySelector('#step-edit-content [data-form-error]').textContent`,
			),
		),
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content').textContent.includes('invalid playbook YAML')`,
	)
	assertSingleModalError(t, b, "#step-edit-dialog", "invalid playbook YAML")
	playbook, _ := json.Marshal(definition.Ansible.Playbook)
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content [name=ansible_playbook]').value=`+string(
			playbook,
		)+`; document.querySelector('#step-edit-content [hx-post$="/ansible/preview"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-ansible-preview]')?.textContent.includes('Static inventory targets: target')`,
	)
	if strings.Contains(
		string(
			b.evaluate(
				t,
				`document.querySelector('#step-edit-content').innerHTML`,
			),
		),
		private,
	) {
		t.Fatal("private key rendered into editor")
	}
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content form').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && !document.querySelector('#step-edit-content').hasChildNodes() && !document.querySelector('.htmx-request,.htmx-settling') && document.querySelector('#step-list').textContent.includes('Native Ansible')`,
	)
	var list struct {
		Items []struct {
			ID int64
			stepdefinition.Definition
		}
	}
	decodeLifecycleTest(t, f.api(t, "GET", f.base()+"/steps", nil, 200), &list)
	if len(list.Items) != 1 || list.Items[0].Kind != "ansible" ||
		list.Items[0].Ansible.Playbook != definition.Ansible.Playbook {
		t.Fatal("web creation lost Ansible inputs")
	}
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-step-action="edit"]')].find(el=>el.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content [name=ansible_playbook]') && document.querySelector('#step-edit-dialog').open`,
	)
	b.evaluate(
		t,
		`(() => {if(document.querySelector('#step-edit-content [name=ansible_check]')) throw new Error('check mode remains on step'); document.querySelector('#step-edit-content form').requestSubmit(); return true;})()`,
	)
	b.wait(t, `!document.querySelector('#step-edit-dialog').open`)
	release := verificationRelease(t, f, "browser-ansible")
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/projects/%d/deploy?release_id=%d",
			f.baseURL,
			f.project.ID,
			release.ID,
		),
	)
	b.wait(
		t,
		`document.querySelector('[name=ansible_check_mode]') && window.Alpine`,
	)
	b.captureNavigation(t, "native-ansible-deployment-form")
	b.evaluate(
		t,
		fmt.Sprintf(
			`(() => {const form=document.querySelector('[name=ansible_check_mode]').form; if(form.elements.ansible_check_mode.checked) throw new Error('fresh default'); const env=form.elements.environment_id; env.value='%d'; env.dispatchEvent(new Event('change',{bubbles:true})); form.elements.ansible_check_mode.checked=true; form.requestSubmit(); return true;})()`,
			f.environment.ID,
		),
	)
	b.wait(
		t,
		`location.pathname.startsWith('/deployments/') && document.querySelector('[data-ansible-check-mode]')`,
	)
	var deployment db.Deployment
	decodeLifecycleTest(
		t,
		b.evaluate(t, `Number(location.pathname.split('/').pop())`),
		&deployment.ID,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	if code, _, err := target.Exec(t.Context(), []string{"test", "!", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("browser check mode changed target filesystem", err)
	}
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID),
	)
	b.wait(t, `document.body.textContent.includes('succeeded')`)
	b.captureNavigation(t, "native-ansible-success")
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/projects/%d/deploy?release_id=%d",
			f.baseURL,
			f.project.ID,
			release.ID,
		),
	)
	b.wait(
		t,
		`document.querySelector('[name=ansible_check_mode]') && !document.querySelector('[name=ansible_check_mode]').checked`,
	)
	var template struct {
		ID int64
		stepdefinition.Definition
	}
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/templates", map[string]any{
		"name": "Native Ansible template", "kind": "ansible", "ansible": definition.Ansible,
		"execution_target": "local", "container_image": definition.ContainerImage, "network_mode": "bridge",
	}, 201), &template)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/templates/%d/edit", f.baseURL, template.ID),
	)
	b.wait(
		t,
		`document.querySelector('#template-settings-form [name=kind]')?.value === 'ansible'`,
	)
	b.evaluate(
		t,
		`(() => {if(document.querySelector('[name=ansible_check]')) throw new Error('check mode remains on template'); document.querySelector('#template-settings-form').requestSubmit(); return true;})()`,
	)
	b.wait(t, `!document.querySelector('.htmx-request,.htmx-settling')`)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/templates/%d", template.ID),
			nil,
			200,
		),
		&template,
	)
	if template.Ansible == nil || template.Ansible.Check != nil ||
		template.Interpreter != "" {
		t.Fatal("template editor lost Ansible configuration")
	}
	b.captureNavigation(t, "native-ansible-template")
	var book struct {
		Runbook db.Runbook
		Version db.RunbookVersion
	}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/runbooks", map[string]any{
			"name": "Browser native runbook", "steps": []stepdefinition.Definition{definition},
		}, 201),
		&book,
	)
	bookPath := fmt.Sprintf("%s/runbooks/%d", f.base(), book.Runbook.ID)
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/projects/%d/runbooks/%d/edit",
			f.baseURL,
			f.project.ID,
			book.Runbook.ID,
		),
	)
	b.wait(
		t,
		`document.querySelector('#runbook-version-form [name=step_kind]')?.value === 'ansible'`,
	)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form button.btn-secondary').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('dialog')?.open && document.querySelector('dialog [name=step_name]')`,
	)
	b.evaluate(
		t,
		`const input=document.querySelector('dialog [name=step_name]'); input.value='Renamed native step'; input.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('dialog button[type=submit]').click(); true`,
	)
	b.wait(t, `!document.querySelector('dialog').open`)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form').requestSubmit(); true`,
	)
	b.waitBackTestURL(
		t,
		fmt.Sprintf(
			"%s/projects/%d/runbooks/%d",
			f.baseURL,
			f.project.ID,
			book.Runbook.ID,
		),
	)
	var detail struct{ Versions []db.RunbookVersion }
	decodeLifecycleTest(t, f.api(t, "GET", bookPath, nil, 200), &detail)
	var saved struct{ Steps []stepdefinition.Definition }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/versions/%d", bookPath, detail.Versions[0].ID),
			nil,
			200,
		),
		&saved,
	)
	if len(saved.Steps) != 1 || saved.Steps[0].Name != "Renamed native step" ||
		saved.Steps[0].Kind != "ansible" ||
		saved.Steps[0].Ansible.JSON() != definition.Ansible.JSON() {
		t.Fatal("browser runbook save lost native configuration")
	}
	b.captureNavigation(t, "native-ansible-runbook")
	b.wait(
		t,
		`document.querySelector('[name=ansible_check_mode]') && !document.querySelector('[name=ansible_check_mode]').checked`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`(() => {const form=document.querySelector('[name=ansible_check_mode]').form; form.elements.environment_id.value='%d'; form.elements.ansible_check_mode.checked=true; form.requestSubmit(); return true;})()`,
			f.environment.ID,
		),
	)
	b.wait(
		t,
		`location.pathname.includes('/runbooks/executions/') && document.querySelector('[data-ansible-check-mode]')`,
	)
	var executionID int64
	decodeLifecycleTest(
		t,
		b.evaluate(t, `Number(location.pathname.split('/').pop())`),
		&executionID,
	)
	var execution db.GetRunbookExecutionRow
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/runbook-executions/%d", f.base(), executionID),
			nil,
			200,
		),
		&execution,
	)
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	b.captureNavigation(t, "native-ansible-runbook-check")
	b.navigateBackTest(
		t,
		fmt.Sprintf(
			"%s/projects/%d/runbooks/%d",
			f.baseURL,
			f.project.ID,
			book.Runbook.ID,
		),
	)
	b.wait(
		t,
		`document.querySelector('[name=ansible_check_mode]') && !document.querySelector('[name=ansible_check_mode]').checked`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`(() => {const form=document.querySelector('[name=ansible_check_mode]').form; form.elements.environment_id.value='%d'; form.requestSubmit(); return true;})()`,
			f.environment.ID,
		),
	)
	b.wait(
		t,
		`location.pathname.includes('/runbooks/executions/') && !document.querySelector('[data-ansible-check-mode]')`,
	)
	decodeLifecycleTest(
		t,
		b.evaluate(t, `Number(location.pathname.split('/').pop())`),
		&executionID,
	)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/runbook-executions/%d", f.base(), executionID),
			nil,
			200,
		),
		&execution,
	)
	if execution.AnsibleCheckMode.Int64 != 0 ||
		!execution.AnsibleCheckMode.Valid {
		t.Fatal("fresh web runbook inherited check mode")
	}
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	if code, _, err := target.Exec(t.Context(), []string{"test", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("fresh web runbook did not apply", err)
	}
}
