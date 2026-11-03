//go:build e2e

package api_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/stepdefinition"
)

func localAnsibleDefinition(t *testing.T) stepdefinition.Definition {
	t.Helper()
	return stepdefinition.Definition{
		Name: "Local API playbook", Kind: "ansible", ExecutionTarget: "local",
		ContainerImage: ansibleTestImage(t), NetworkMode: "none",
		Ansible: &stepdefinition.Ansible{
			Connection: "local", Inventory: "all: {hosts: {localhost: {}}}",
			Playbook: "- hosts: localhost\n  gather_facts: false\n  tasks:\n    - ansible.builtin.debug: {msg: local-container-success}\n",
		},
	}
}

func TestAnsibleLocalRetryAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	definition := localAnsibleDefinition(t)
	definition.MaxRetries = 1
	definition.Ansible.ExtraVars = map[string]string{
		"stage_dir": "DURPDEPLOY_STAGE_DIR",
	}
	definition.Ansible.Playbook = `- hosts: localhost
  gather_facts: false
  tasks:
    - ansible.builtin.shell: |
        if test -f '{{ stage_dir }}/attempt'; then
          echo local-retry-success
        else
          touch '{{ stage_dir }}/attempt'
          exit 1
        fi
`
	f.api(t, "POST", f.base()+"/steps", definition, 201)
	release := verificationRelease(t, f, "local-retry")
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "attempt=2") ||
		!strings.Contains(logs, "local-retry-success") {
		t.Fatalf("local attempt retry did not run: %s", logs)
	}
}

func TestAnsibleLocalURICheckModeAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	definition := localAnsibleDefinition(t)
	definition.Ansible.ExtraVars = map[string]string{
		"stage_dir": "DURPDEPLOY_STAGE_DIR",
	}
	// The documented recipe must skip URI and its dependent copy in check mode.
	// Network none ensures this check cannot contact the example endpoint.
	definition.Ansible.Playbook = `- hosts: localhost
  connection: local
  gather_facts: false
  tasks:
    - ansible.builtin.uri:
        url: https://api.example/kubeconfig
        return_content: true
      register: kubeconfig_response
      no_log: true
    - ansible.builtin.copy:
        content: '{{ kubeconfig_response.content }}'
        dest: '{{ stage_dir }}/kubeconfig'
        mode: '0600'
      when: not ansible_check_mode
      no_log: true
`
	f.api(t, "POST", f.base()+"/steps", definition, 201)
	release := verificationRelease(t, f, "local-uri-check")
	var deployment db.Deployment
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID, "ansible_check_mode": true,
		}, 201),
		&deployment,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
			nil,
			200,
		),
	)
	if strings.Count(logs, "skipping: [localhost]") != 2 {
		t.Fatalf("URI recipe did not skip both tasks in check mode: %s", logs)
	}
}

func TestAnsibleLocalAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	definition := localAnsibleDefinition(t)
	var step struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", definition, 201),
		&step,
	)
	preview := f.api(
		t,
		"POST",
		f.base()+"/ansible/preview",
		definition.Ansible,
		200,
	)
	if !bytes.Contains(preview, []byte(`"localhost"`)) {
		t.Fatalf("local preview missing: %s", preview)
	}
	testAnsibleRunbookSnapshot(t, f, definition, step.ID)
	testAnsibleStepTemplateInsertion(t, f, definition)
	// No SSH credential exists in this project, including execution snapshots.
	release := verificationRelease(t, f, "local-container")
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
	if !bytes.Contains(logs, []byte("local-container-success")) {
		t.Fatalf("local task did not run: %s", logs)
	}
	for _, mixed := range []stepdefinition.Ansible{
		{Connection: "local", SSHKeyVariable: "UNUSED"},
		{Connection: "local", KnownHosts: "unused"},
		{Connection: "unknown"},
	} {
		mixed.Playbook = definition.Ansible.Playbook
		mixed.Inventory = definition.Ansible.Inventory
		f.api(t, "POST", f.base()+"/ansible/preview", mixed, 400)
		definition.Ansible = &mixed
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/steps/%d", f.base(), step.ID),
			definition,
			400,
		)
	}
}

func TestAnsibleLocalPackageHandoffAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	definition := localAnsibleDefinition(t)
	definition.Ansible.Playbook = "- ansible.builtin.import_playbook: '{{ package_dir }}/local.yml'\n"
	definition.Ansible.ExtraVars = map[string]string{
		"package_dir": "ARTIFACT_PATH", "stage_dir": "DURPDEPLOY_STAGE_DIR", "message": "MESSAGE",
	}
	playbook := `- hosts: localhost
  connection: local
  gather_facts: false
  tasks:
    - ansible.builtin.assert:
        that:
          - lookup('env', 'UNSELECTED') == ''
          - lookup('env', 'MESSAGE') == ''
          - query('fileglob', '/ansible/id_key') | length == 0
          - query('fileglob', '/ansible/known_hosts') | length == 0
    - ansible.builtin.debug:
        msg: "local-check-mode={{ ansible_check_mode }}"
    - ansible.builtin.copy:
        content: '{{ message }}'
        dest: '{{ stage_dir }}/kubeconfig'
        mode: '0600'
`
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("local.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(playbook)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "playbooks.zip")
	if err := os.WriteFile(filename, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	f.changePackage("file:" + filename)
	f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": f.upstream + "/{version}.zip", "auth_type": "bearer", "credential": "artifact-secret",
	}, 200)
	for name, value := range map[string]string{"MESSAGE": "mapped-kubeconfig", "UNSELECTED": "must-not-export"} {
		f.api(
			t,
			"POST",
			f.base()+"/variables",
			map[string]string{"name": name, "value": value},
			201,
		)
	}
	f.api(t, "POST", f.base()+"/steps", definition, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Read local output", "interpreter": "python3", "container_image": definition.ContainerImage,
		"script_body": "import os, pathlib\np = pathlib.Path(os.environ['DURPDEPLOY_STAGE_DIR']) / 'kubeconfig'\nif p.exists():\n assert p.read_text() == 'mapped-kubeconfig'\n print('local-handoff-consumed')\nelse:\n print('local-check-no-output')\n",
	}, 201)
	release := verificationRelease(t, f, "local-package")
	for _, check := range []bool{true, false} {
		var deployment db.Deployment
		decodeLifecycleTest(
			t,
			f.api(t, "POST", f.base()+"/deployments", map[string]any{
				"release_id": release.ID, "environment_id": f.environment.ID, "ansible_check_mode": check,
			}, 201),
			&deployment,
		)
		f.completion(t, deployment.ID, events.DeploymentSucceeded)
		assertLocalHandoffLogs(t, f, deployment.ID, check)
		var rerun db.Deployment
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				fmt.Sprintf("/api/v1/deployments/%d/redeploy", deployment.ID),
				nil,
				201,
			),
			&rerun,
		)
		if rerun.AnsibleCheckMode != deployment.AnsibleCheckMode {
			t.Fatal("local redeploy lost execution mode")
		}
		f.completion(t, rerun.ID, events.DeploymentSucceeded)
		assertLocalHandoffLogs(t, f, rerun.ID, check)
	}
}

func assertLocalHandoffLogs(
	t *testing.T,
	f *artifactE2E,
	id int64,
	check bool,
) {
	t.Helper()
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", id),
			nil,
			200,
		),
	)
	marker, mode := "local-handoff-consumed", "False"
	if check {
		marker, mode = "local-check-no-output", "True"
	}
	if !strings.Contains(logs, marker) ||
		!strings.Contains(logs, "local-check-mode="+mode) {
		t.Fatalf("local check/handoff contract failed: %s", logs)
	}
}
