//go:build e2e

package api_test

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/stepdefinition"
)

func TestAnsibleInventoryPreviewAPIE2E(t *testing.T) {
	f, definition, _, _ := newAnsibleE2E(t)
	a := *definition.Ansible
	a.Inventory = "all: {hosts: {node1: {}}}\nweb: {hosts: {node2: {}}}"
	a.Limit = []string{"all"}
	var preview struct {
		Targets []string `json:"targets"`
	}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/ansible/preview", a, 200),
		&preview,
	)
	if fmt.Sprint(preview.Targets) != "[node1 node2]" {
		t.Fatalf("implicit all preview=%v", preview.Targets)
	}
	a.Limit = []string{"ungrouped"}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/ansible/preview", a, 200),
		&preview,
	)
	if fmt.Sprint(preview.Targets) != "[node1]" {
		t.Fatalf("implicit ungrouped preview=%v", preview.Targets)
	}
	a.Inventory = "ungrouped: {hosts: {node1: {}}}\nweb: {hosts: {node1: {}}}"
	f.api(t, "POST", f.base()+"/ansible/preview", a, 400)
	for _, inventory := range []string{
		"all: {hosts: {yes: {}}}",
		"all: {hosts: {target: {ansible_user: on}}}",
		"ungrouped: {children: {web: {hosts: {node1: {}}}}}",
	} {
		a.Inventory = inventory
		a.Limit = nil
		f.api(t, "POST", f.base()+"/ansible/preview", a, 400)
		definition.Ansible = &a
		f.api(t, "POST", f.base()+"/steps", definition, 400)
	}
}

func TestAnsibleCompositionAPIE2E(t *testing.T) {
	f, definition, private, _ := newAnsibleE2E(t)
	var step struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", definition, 201),
		&step,
	)
	testAnsibleRunbookSnapshot(t, f, definition, step.ID)
	testAnsibleProjectTemplate(t, f, definition, private)
	testAnsibleStepTemplateInsertion(t, f, definition)
}

func testAnsibleRunbookSnapshot(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
	stepID int64,
) {
	t.Helper()
	var book struct {
		Runbook db.Runbook
		Version db.RunbookVersion
	}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/runbooks", map[string]any{
			"name": "Native Ansible runbook", "steps": []stepdefinition.Definition{definition},
		}, 201),
		&book,
	)
	bookPath := fmt.Sprintf("%s/runbooks/%d", f.base(), book.Runbook.ID)
	var version struct{ Steps []stepdefinition.Definition }
	versionPath := fmt.Sprintf("%s/versions/%d", bookPath, book.Version.ID)
	decodeLifecycleTest(t, f.api(t, "GET", versionPath, nil, 200), &version)
	if len(version.Steps) != 1 || version.Steps[0].Kind != "ansible" ||
		version.Steps[0].Ansible.JSON() != definition.Ansible.JSON() {
		t.Fatal("runbook version lost native configuration")
	}
	changed := definition
	changedConfig := *definition.Ansible
	changed.Ansible = &changedConfig
	changedConfig.Playbook = "- hosts: all\n  tasks:\n    - ansible.builtin.fail: {msg: changed-project-step}\n"
	f.api(t, "PUT", fmt.Sprintf("%s/steps/%d", f.base(), stepID), changed, 200)
	var execution db.RunbookExecution
	decodeLifecycleTest(
		t,
		f.api(t, "POST", bookPath+"/executions", map[string]int64{
			"environment_id": f.environment.ID, "version_id": book.Version.ID,
		}, 201),
		&execution,
	)
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	// Saving another version cannot modify the original immutable source.
	f.api(
		t,
		"PUT",
		bookPath,
		map[string]any{"steps": []stepdefinition.Definition{changed}},
		201,
	)
	decodeLifecycleTest(t, f.api(t, "GET", versionPath, nil, 200), &version)
	if version.Steps[0].Ansible.JSON() != definition.Ansible.JSON() {
		t.Fatal("new runbook version mutated old Ansible configuration")
	}
	f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/steps/%d", f.base(), stepID),
		definition,
		200,
	)
}

func testAnsibleProjectTemplate(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
	private string,
) {
	t.Helper()
	var template struct {
		ID       int64
		Snapshot repository.ProjectTemplateSnapshot
	}
	raw := f.api(
		t,
		"POST",
		f.base()+"/project-templates",
		map[string]string{"name": "Native Ansible project snapshot"},
		201,
	)
	if bytes.Contains(raw, []byte(private)) {
		t.Fatal("project template exported credential bytes")
	}
	decodeLifecycleTest(t, raw, &template)
	if len(template.Snapshot.Steps) != 1 ||
		template.Snapshot.Steps[0].Ansible.JSON() != definition.Ansible.JSON() {
		t.Fatal("project template capture lost native configuration")
	}
	input := repository.ApplyProjectTemplate{
		Name: "Imported native Ansible project", Reviewed: true,
		EnvironmentMappings: map[string]int64{
			strconv.FormatInt(f.environment.ID, 10): f.environment.ID,
		},
		VariableValues: map[string]string{},
	}
	for _, variable := range template.Snapshot.Variables {
		if variable.Secret {
			value := "ansible-selected-secret"
			if variable.Name == "SSH_KEY" {
				value = private
			}
			input.VariableValues[variable.Key] = value
		}
	}
	var copied struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf(
				"%s/project-templates/%d/projects",
				f.base(),
				template.ID,
			),
			input,
			201,
		),
		&copied,
	)
	copiedBase := fmt.Sprintf("/api/v1/projects/%d", copied.ID)
	var steps struct{ Items []stepdefinition.Definition }
	decodeLifecycleTest(
		t,
		f.api(t, "GET", copiedBase+"/steps", nil, 200),
		&steps,
	)
	if len(steps.Items) != 1 || steps.Items[0].Kind != "ansible" ||
		steps.Items[0].Ansible.JSON() != definition.Ansible.JSON() {
		t.Fatal("project template apply lost native configuration")
	}
	var release db.Release
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			copiedBase+"/releases",
			map[string]string{"version": "native-copy"},
			201,
		),
		&release,
	)
	var deployment db.Deployment
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			copiedBase+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
			},
			201,
		),
		&deployment,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func testAnsibleStepTemplateInsertion(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
) {
	t.Helper()
	// The web template insertion path must copy kind and configuration too.
	var stepTemplate struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/templates", map[string]any{
		"name": "Inserted native template", "kind": "ansible", "ansible": definition.Ansible,
		"execution_target": "local", "container_image": definition.ContainerImage, "network_mode": "bridge",
	}, 201), &stepTemplate)
	f.web(
		t,
		"POST",
		fmt.Sprintf(
			"/projects/%d/steps/from-template/%d",
			f.project.ID,
			stepTemplate.ID,
		),
		nil,
		200,
	)
	var steps struct{ Items []stepdefinition.Definition }
	decodeLifecycleTest(t, f.api(t, "GET", f.base()+"/steps", nil, 200), &steps)
	found := false
	for _, copiedStep := range steps.Items {
		if copiedStep.Name == "Inserted native template" {
			found = copiedStep.Kind == "ansible" &&
				copiedStep.Ansible.JSON() == definition.Ansible.JSON()
		}
	}
	if !found {
		t.Fatal("web template insertion lost native configuration")
	}
}
