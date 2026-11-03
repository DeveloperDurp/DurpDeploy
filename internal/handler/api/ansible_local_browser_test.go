//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/stepdefinition"
)

func TestAnsibleLocalBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	definition := localAnsibleDefinition(t)
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
	b.wait(
		t,
		`document.querySelector('#step-edit-content [name=ansible_connection]')?.value === 'ssh'`,
	)
	values, _ := json.Marshal(map[string]string{
		"name": definition.Name, "container_image": definition.ContainerImage, "network_mode": "none",
		"ansible_connection": "local", "ansible_playbook": definition.Ansible.Playbook,
		"ansible_inventory": definition.Ansible.Inventory,
	})
	b.evaluate(
		t,
		`(() => { const form=document.querySelector('#step-edit-content form'); const values=`+string(
			values,
		)+`; for(const [name,value] of Object.entries(values)) form.elements.namedItem(name).value=value; form.querySelector('[hx-post$="/ansible/preview"]').click(); return true; })()`,
	)
	b.wait(
		t,
		`document.querySelector('[data-ansible-preview]')?.textContent.includes('Static inventory targets: localhost')`,
	)
	b.screenshot(t, "ansible-local-preview")
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content form').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && !document.querySelector('#step-edit-content').hasChildNodes() && !document.querySelector('.htmx-request,.htmx-settling') && document.querySelector('#step-list')?.textContent.includes('Local API playbook')`,
	)
	var list struct{ Items []stepdefinition.Definition }
	decodeLifecycleTest(t, f.api(t, "GET", f.base()+"/steps", nil, 200), &list)
	if len(list.Items) != 1 || list.Items[0].Ansible.Connection != "local" ||
		list.Items[0].Ansible.SSHKeyVariable != "" ||
		list.Items[0].Ansible.KnownHosts != "" {
		t.Fatalf("browser did not save local configuration: %+v", list)
	}
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
		`document.querySelector('#step-edit-dialog').open && document.querySelector('#step-edit-content [name=ansible_connection]')?.value === 'local'`,
	)
	b.screenshot(t, "ansible-local-saved")
}
