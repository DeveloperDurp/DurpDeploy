//go:build e2e

package api_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/stepdefinition"
	"durpdeploy/internal/testutil"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
)

var buildAnsibleImage sync.Once
var ansibleImageError error

func ansibleTestImage(t *testing.T) string {
	t.Helper()
	const image = "localhost:5000/durpdeploy-ansible-e2e:143"
	buildAnsibleImage.Do(func() {
		engine := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
		if engine == "" {
			engine = "podman"
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		output, err := exec.CommandContext(ctx, engine, "build", "-q", "-t", image, filepath.Join("..", "..", "..", "testdata", "ansible")).
			CombinedOutput()
		if err != nil {
			ansibleImageError = fmt.Errorf(
				"build Ansible fixture: %w: %s",
				err,
				output,
			)
		}
	})
	if ansibleImageError != nil {
		t.Fatal(ansibleImageError)
	}
	return image
}

func ansibleKey(t *testing.T) (string, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "Ansible E2E only")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return string(
			pem.EncodeToMemory(block),
		), string(
			ssh.MarshalAuthorizedKey(key),
		)
}

func newAnsibleE2E(
	t *testing.T,
) (*artifactE2E, stepdefinition.Definition, string, testcontainers.Container) {
	t.Helper()
	f := newArtifactE2E(t)
	f.completionTimeout = 90 * time.Second
	image := ansibleTestImage(t)
	private, public := ansibleKey(t)
	private = strings.TrimSpace(private)
	hostPrivate, hostPublic := ansibleKey(t)
	target, err := testcontainers.GenericContainer(
		t.Context(),
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: image,
				User:  "0:0", // The disposable SSH server needs host-key ownership.
				Files: []testcontainers.ContainerFile{
					{
						Reader:            strings.NewReader(public),
						ContainerFilePath: "/authorized_keys",
						FileMode:          0600,
					},
					{
						Reader:            strings.NewReader(hostPrivate),
						ContainerFilePath: "/host_key",
						FileMode:          0600,
					},
				},
				Cmd: []string{
					"sh",
					"-c",
					"mkdir -p /run/sshd; passwd -d root >/dev/null; exec /usr/sbin/sshd -D -e -h /host_key -o AuthorizedKeysFile=/authorized_keys -o PermitRootLogin=yes -o PasswordAuthentication=no -o StrictModes=no -o UsePAM=no",
				},
				WaitingFor: wait.ForLog("Server listening").
					WithStartupTimeout(time.Minute),
			}, Started: true,
		},
	)
	testutil.CleanupContainer(t, target)
	if err != nil {
		t.Fatal(err)
	}
	address, err := target.ContainerIP(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rotationAPIVariable(t, f, f.base(), "SSH_KEY", private)
	f.api(
		t,
		"POST",
		f.base()+"/variables",
		map[string]any{"name": "MESSAGE", "value": "global"},
		201,
	)
	f.api(
		t,
		"POST",
		f.base()+"/variables",
		map[string]any{
			"name":           "MESSAGE",
			"value":          "scoped",
			"environment_id": f.environment.ID,
		},
		201,
	)
	f.api(
		t,
		"POST",
		f.base()+"/variables",
		map[string]any{
			"name":   "PRIVATE",
			"value":  "ansible-selected-secret",
			"secret": true,
		},
		201,
	)
	f.api(
		t,
		"POST",
		f.base()+"/variables",
		map[string]any{
			"name":   "UNSELECTED",
			"value":  "must-not-export",
			"secret": true,
		},
		201,
	)
	definition := stepdefinition.Definition{
		Kind: stepdefinition.AnsibleKind, Name: "Native Ansible", ExecutionTarget: "local", ContainerImage: image, NetworkMode: "bridge", TimeoutSeconds: 60,
		Ansible: &stepdefinition.Ansible{
			Playbook: "- hosts: all\n  gather_facts: false\n  tasks:\n    - name: explicit variables and environment precedence\n      ansible.builtin.assert:\n        that:\n          - message == 'scoped'\n          - lookup('env', 'UNSELECTED') == ''\n          - lookup('env', 'SSH_KEY') == ''\n      tags: [deploy]\n    - name: secret output\n      ansible.builtin.debug:\n        msg: '{{ private }}'\n      no_log: true\n      tags: [deploy]\n    - name: selected secret scrubber\n      ansible.builtin.debug:\n        msg: '{{ private }}'\n      tags: [deploy]\n    - name: write example file\n      ansible.builtin.copy:\n        content: ansible-native-success\n        dest: /tmp/native-ansible-result\n        mode: '0600'\n      tags: [deploy]\n    - name: unselected tag\n      ansible.builtin.fail:\n        msg: wrong tag selected\n      tags: [excluded]\n",
			Inventory: fmt.Sprintf(
				"all:\n  hosts:\n    target:\n      ansible_host: %s\n      ansible_user: root\n    excluded:\n      ansible_host: 192.0.2.1\n",
				address,
			),
			ExtraVars: map[string]string{
				"message": "MESSAGE",
				"private": "PRIVATE",
			}, SSHKeyVariable: "SSH_KEY", KnownHosts: address + " " + hostPublic, Tags: []string{"deploy"}, Limit: []string{"target"},
		},
	}
	return f, definition, private, target
}

func TestAnsibleAPIE2E(t *testing.T) {
	f, definition, private, target := newAnsibleE2E(t)
	var created struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", definition, 201),
		&created,
	)
	path := fmt.Sprintf("%s/steps/%d", f.base(), created.ID)
	f.api(t, "POST", f.base()+"/ansible/preview", definition.Ansible, 200)
	assertAnsibleVariableCatalog(t, f, created.ID, private)
	testAnsibleCheckAndApply(t, f, definition, target, path, private)
	testAnsibleFailureOutcomes(t, f, definition, path)
	testAnsibleAuthorization(t, f, definition, private, path)
	testAnsibleParserAndEmptyMappings(t, f, definition, path)
}

func assertAnsibleVariableCatalog(
	t *testing.T, f *artifactE2E, stepID int64, private string,
) {
	t.Helper()
	var catalog struct {
		Context struct {
			Kind          string
			VariableNames []string `json:"variable_names"`
		}
		Configured []struct{ Name, Status, Reference string }
	}
	metadata := f.api(
		t,
		"GET",
		fmt.Sprintf(
			"%s/variables/available?step_id=%d&environment_id=%d",
			f.base(),
			stepID,
			f.environment.ID,
		),
		nil,
		200,
	)
	decodeLifecycleTest(t, metadata, &catalog)
	if catalog.Context.Kind != "ansible" ||
		fmt.Sprint(catalog.Context.VariableNames) != "[MESSAGE PRIVATE]" {
		t.Fatal("variable browser lost explicit Ansible selection")
	}
	excluded := false
	for _, variable := range catalog.Configured {
		if variable.Reference != "" {
			t.Fatal("Ansible catalog returned a script-language reference")
		}
		if variable.Name == "UNSELECTED" {
			excluded = variable.Status == "excluded"
		}
	}
	if !excluded || bytes.Contains(metadata, []byte(private)) {
		t.Fatal("Ansible catalog leaked or selected an unavailable value")
	}
}

func testAnsibleCheckAndApply(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
	target testcontainers.Container, path, private string,
) {
	t.Helper()
	release := verificationRelease(t, f, "ansible-original")
	var check db.Deployment
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID,
			"ansible_check_mode": true,
		}, 201),
		&check,
	)
	if !check.AnsibleCheckMode.Valid || check.AnsibleCheckMode.Int64 != 1 {
		t.Fatal("check mode was not captured")
	}
	f.completion(t, check.ID, events.DeploymentSucceeded)
	assertAnsibleCheckDoesNotPromote(t, f, release)
	var redeploy db.Deployment
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/redeploy", check.ID),
			nil,
			201,
		),
		&redeploy,
	)
	if redeploy.AnsibleCheckMode != check.AnsibleCheckMode {
		t.Fatal("redeploy lost check mode")
	}
	f.completion(t, redeploy.ID, events.DeploymentSucceeded)
	if code, _, err := target.Exec(t.Context(), []string{"test", "!", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("check mode changed the target filesystem", err)
	}
	deployment := verificationDeploy(t, f, release)
	if !deployment.AnsibleCheckMode.Valid ||
		deployment.AnsibleCheckMode.Int64 != 0 {
		t.Fatal("fresh deployment inherited check mode")
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	assertAnsibleOutput(t, f, deployment.ID, private)
	definition.Ansible.Playbook += "# project edit after acceptance\n"
	f.api(t, "PUT", path, definition, 200)

	if code, _, err := target.Exec(t.Context(), []string{"grep", "-q", "^ansible-native-success$", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("apply did not write the expected target file", err)
	}
	// Project edits cannot mutate either accepted source.
	stored, err := f.h.repo.Queries.GetDeploymentStepSource(
		t.Context(),
		deployment.ID,
	)
	if err != nil || stored.StepsJson != release.StepsJson {
		t.Fatal("deployment source changed", err)
	}
	original, err := f.h.repo.Queries.GetRelease(t.Context(), release.ID)
	if err != nil || original.StepsJson != release.StepsJson {
		t.Fatal("release snapshot changed", err)
	}
}

func assertAnsibleOutput(
	t *testing.T,
	f *artifactE2E,
	deploymentID int64,
	private string,
) {
	t.Helper()
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deploymentID),
		nil,
		200,
	)
	for _, forbidden := range []string{private, "ansible-selected-secret", "must-not-export"} {
		if bytes.Contains(logs, []byte(forbidden)) {
			t.Fatal("private data appeared in logs")
		}
	}
	if !bytes.Contains(logs, []byte("PLAY RECAP")) {
		t.Fatal("native Ansible output missing")
	}
}

func assertAnsibleCheckDoesNotPromote(
	t *testing.T,
	f *artifactE2E,
	release db.Release,
) {
	t.Helper()
	var lifecycle, next struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Ansible promotion"},
			201,
		),
		&lifecycle,
	)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/environments",
			map[string]string{"name": "Ansible next stage"},
			201,
		),
		&next,
	)
	for i, environmentID := range []int64{f.environment.ID, next.ID} {
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]any{
				"environment_id": environmentID, "sort_order": 5 + i*5,
			},
			201,
		)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": next.ID,
	}, 422)
}

func testAnsibleFailureOutcomes(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
	path string,
) {
	t.Helper()
	for name, mutate := range map[string]func(*stepdefinition.Definition){
		"failed task": func(d *stepdefinition.Definition) {
			d.MaxRetries = 1
			d.Ansible.Playbook = "- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.fail:\n        msg: deliberate-failure\n"
			d.Ansible.Tags = nil
		},
		"unreachable": func(d *stepdefinition.Definition) {
			d.Ansible.Inventory = "all:\n  hosts:\n    target:\n      ansible_host: 192.0.2.1\n      ansible_port: 1\n"
			d.TimeoutSeconds = 10
		},
		"missing collection": func(d *stepdefinition.Definition) {
			d.Ansible.Playbook = "- hosts: all\n  tasks:\n    - community.general.this_module_does_not_exist: {}\n"
			d.Ansible.Tags = nil
		},
		"missing executable": func(d *stepdefinition.Definition) {
			d.ContainerImage = "harbor.durp.info/dockerhub/library/alpine:3.24"
		},
		"timeout": func(d *stepdefinition.Definition) {
			d.Ansible.Playbook = "- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.command: sleep 30\n"
			d.Ansible.Tags = nil
			d.TimeoutSeconds = 3
		},
		"wrong host key": func(d *stepdefinition.Definition) {
			_, pub := ansibleKey(t)
			host := strings.Split(d.Ansible.KnownHosts, " ")[0]
			d.Ansible.KnownHosts = host + " " + pub
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := definition
			a := *definition.Ansible
			d.Ansible = &a
			mutate(&d)
			f.api(t, "PUT", path, d, 200)
			release := verificationRelease(
				t,
				f,
				"ansible-"+strings.ReplaceAll(name, " ", "-"),
			)
			deployment := verificationDeploy(t, f, release)
			f.completion(t, deployment.ID, events.DeploymentFailed)
			if name == "failed task" {
				logs := f.api(
					t,
					"GET",
					fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
					nil,
					200,
				)
				if !bytes.Contains(logs, []byte("attempt=2")) {
					t.Fatal("native Ansible retry did not execute")
				}
				testAnsibleCheckRetry(t, f, release)
			}
		})
	}
}

func testAnsibleAuthorization(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
	private, path string,
) {
	t.Helper()
	for _, check := range []bool{false, true} {
		bad := definition
		config := *definition.Ansible
		config.Check = &check
		bad.Ansible = &config
		f.api(t, "PUT", path, bad, 400)
	}
	bad := definition
	bad.ExecutionTarget = "agent"
	f.api(t, "PUT", path, bad, 400)
	bad = definition
	bad.Ansible = &stepdefinition.Ansible{Playbook: "[invalid"}
	f.api(t, "PUT", path, bad, 400)
	// Credential names resolve within the project, never another project.
	foreign := seedProject(t, f.h.repo)
	rotationAPIVariable(
		t,
		f,
		fmt.Sprintf("/api/v1/projects/%d", foreign.ID),
		"FOREIGN_KEY",
		private,
	)
	bad = definition
	foreignConfig := *definition.Ansible
	foreignConfig.SSHKeyVariable = "FOREIGN_KEY"
	bad.Ansible = &foreignConfig
	f.api(t, "PUT", path, bad, 400)
	// Both step reads and preview enforce the same project boundary.
	outsider := seedAPIUser(
		t,
		f.h.repo,
		"ansible-outsider@example.com",
		"deployer",
	)
	_, outsiderToken := seedAPIToken(t, f.h.repo, outsider.ID)
	adminToken := f.token
	f.token = outsiderToken
	f.api(t, "GET", path, nil, 403)
	f.api(t, "PUT", path, definition, 403)
	f.api(t, "POST", f.base()+"/ansible/preview", definition.Ansible, 403)
	f.api(t, "GET", f.base()+"/credentials", nil, 403)
	f.token = adminToken
	public := f.api(t, "GET", path, nil, 200)
	if bytes.Contains(public, []byte(private)) {
		t.Fatal("credential value appeared in public step response")
	}
}

func testAnsibleCheckRetry(t *testing.T, f *artifactE2E, release db.Release) {
	t.Helper()
	var source, retry db.Deployment
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID,
			"ansible_check_mode": true,
		}, 201),
		&source,
	)
	f.completion(t, source.ID, events.DeploymentFailed)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/retry", source.ID),
			nil,
			201,
		),
		&retry,
	)
	if retry.AnsibleCheckMode != source.AnsibleCheckMode ||
		retry.AnsibleCheckMode.Int64 != 1 {
		t.Fatal("retry lost captured check mode")
	}
	f.completion(t, retry.ID, events.DeploymentFailed)
}

func TestAnsibleRunbookCheckModeAPIE2E(t *testing.T) {
	f, definition, _, target := newAnsibleE2E(t)
	var book struct {
		Runbook db.Runbook
		Version db.RunbookVersion
	}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/runbooks", map[string]any{
			"name": "Ansible execution options", "steps": []stepdefinition.Definition{definition},
		}, 201),
		&book,
	)
	executePath := fmt.Sprintf(
		"%s/runbooks/%d/executions",
		f.base(),
		book.Runbook.ID,
	)
	var source, retry, fresh db.RunbookExecution
	decodeLifecycleTest(t, f.api(t, "POST", executePath, map[string]any{
		"environment_id": f.environment.ID, "ansible_check_mode": true,
	}, 201), &source)
	f.completion(t, source.DeploymentID, events.RunbookSucceeded)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("%s/runbook-executions/%d/retry", f.base(), source.ID),
			nil,
			201,
		),
		&retry,
	)
	f.completion(t, retry.DeploymentID, events.RunbookSucceeded)
	var history db.GetRunbookExecutionRow
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/runbook-executions/%d", f.base(), retry.ID),
			nil,
			200,
		),
		&history,
	)
	if !history.AnsibleCheckMode.Valid || history.AnsibleCheckMode.Int64 != 1 {
		t.Fatal("runbook retry lost check mode")
	}
	if code, _, err := target.Exec(t.Context(), []string{"test", "!", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("runbook check changed target", err)
	}
	decodeLifecycleTest(t, f.api(t, "POST", executePath, map[string]int64{
		"environment_id": f.environment.ID,
	}, 201), &fresh)
	f.completion(t, fresh.DeploymentID, events.RunbookSucceeded)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/runbook-executions/%d", f.base(), fresh.ID),
			nil,
			200,
		),
		&history,
	)
	if !history.AnsibleCheckMode.Valid || history.AnsibleCheckMode.Int64 != 0 {
		t.Fatal("fresh runbook inherited check mode")
	}
	if code, _, err := target.Exec(t.Context(), []string{"test", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("fresh runbook did not apply", err)
	}
}

func testAnsibleParserAndEmptyMappings(
	t *testing.T, f *artifactE2E, definition stepdefinition.Definition,
	path string,
) {
	t.Helper()
	// Ansible must fail if its inventory parser rejects the fixed source.
	parserCheck := definition
	parserConfig := *definition.Ansible
	parserCheck.Ansible = &parserConfig
	parserConfig.Tags = nil
	parserConfig.Playbook = `- hosts: all
  gather_facts: false
  tasks:
    - name: inventory parse failures are fatal
      ansible.builtin.command:
        argv: [ansible-inventory, --inventory, /ansible/playbook.yml, --list]
      delegate_to: localhost
      vars:
        ansible_remote_tmp: /tmp/ansible-inventory-probe
      register: invalid_inventory
      failed_when: invalid_inventory.rc == 0
      changed_when: false
`
	f.api(t, "PUT", path, parserCheck, 200)
	parserDeployment := verificationDeploy(
		t, f, verificationRelease(t, f, "ansible-inventory-parse-failure"),
	)
	f.completion(t, parserDeployment.ID, events.DeploymentSucceeded)
	// Empty mappings do not inherit the script runner's unrestricted default.
	empty := definition
	emptyConfig := *definition.Ansible
	empty.Ansible = &emptyConfig
	emptyConfig.ExtraVars = nil
	emptyConfig.Tags = nil
	emptyConfig.Playbook = "- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.assert:\n        that:\n          - message is not defined\n          - private is not defined\n"
	f.api(t, "PUT", path, empty, 200)
	emptyDeployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "ansible-empty-mappings"),
	)
	f.completion(t, emptyDeployment.ID, events.DeploymentSucceeded)
}

func TestAnsibleCancellationAPIE2E(t *testing.T) {
	f, definition, private, _ := newAnsibleE2E(t)
	definition.Ansible.Playbook = "- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.command: sleep 60\n"
	definition.Ansible.Tags = nil
	f.api(t, "POST", f.base()+"/steps", definition, 201)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "ansible-cancel"),
	)
	waitAnsibleDeploymentStatus(t, f, deployment.ID, "running")
	// Wait until the direct workload exists, then inspect its nonsecret argv.
	engine := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	namespace := engine + ":" + os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	id := waitAnsibleWorkload(t, ctx, engine, namespace)
	output, err := exec.CommandContext(ctx, engine, "inspect", "--format={{json .Config.Entrypoint}} {{json .Config.Cmd}}", id).
		Output()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, []byte(private)) ||
		bytes.Contains(output, []byte("ansible-selected-secret")) {
		t.Fatal("secret leaked into workload arguments")
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", deployment.ID),
		nil,
		200,
	)
	waitAnsibleDeploymentStatus(t, f, deployment.ID, "cancelled")
	for _, args := range [][]string{
		{"ps", "-aq", "--filter=label=io.durpdeploy.namespace=" + namespace},
		{"volume", "ls", "-q", "--filter=label=io.durpdeploy.namespace=" + namespace},
	} {
		output, err := exec.CommandContext(t.Context(), engine, args...).
			Output()
		if err != nil || len(bytes.TrimSpace(output)) != 0 {
			t.Fatalf("cancelled deployment retained managed resources: %v", err)
		}
	}
	// A new accepted execution can proceed only after confirmed cleanup.
	var retried db.Deployment
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/redeploy", deployment.ID),
			nil,
			201,
		),
		&retried,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", retried.ID),
		nil,
		200,
	)
}

func waitAnsibleWorkload(
	t *testing.T, ctx context.Context, engine, namespace string,
) string {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		output, err := exec.CommandContext(ctx, engine, "ps", "-q", "--filter=label=io.durpdeploy.namespace="+namespace, "--filter=label=io.durpdeploy.attempt").
			Output()
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(output)) != 0 {
			return strings.TrimSpace(string(output))
		}
		select {
		case <-ctx.Done():
			t.Fatal("Ansible workload did not start")
		case <-ticker.C:
		}
	}
}

func waitAnsibleDeploymentStatus(
	t *testing.T, f *artifactE2E, id int64, want string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		row, err := f.h.repo.Queries.GetDeployment(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("deployment did not reach %s: %s", want, row.Status)
		case <-ticker.C:
		}
	}
}
