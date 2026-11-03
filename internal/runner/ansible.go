package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"durpdeploy/internal/stepdefinition"
	"golang.org/x/crypto/ssh"
)

const ansibleMount = "/ansible"
const ansibleStagingBytes = 8 << 20

const ansibleSSHArgs = "-F /dev/null -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/ansible/known_hosts -o GlobalKnownHostsFile=/dev/null -o IdentitiesOnly=yes -o IdentityAgent=none -o ForwardAgent=no -o ProxyCommand=none -o ProxyJump=none -o ControlMaster=no"

func ansibleEnvironmentArgs() []string {
	return []string{
		"--env=ANSIBLE_CONFIG=/ansible/ansible.cfg",
		"--env=ANSIBLE_NOCOLOR=1",
		"--env=ANSIBLE_HOST_KEY_CHECKING=True",
		"--env=ANSIBLE_PRIVATE_KEY_FILE=/ansible/id_key",
		"--env=ANSIBLE_SSH_ARGS=" + ansibleSSHArgs,
		"--env=ANSIBLE_SSH_COMMON_ARGS=",
		"--env=ANSIBLE_SSH_EXTRA_ARGS=",
		"--env=ANSIBLE_LOG_PATH=",
		"--env=SSH_AUTH_SOCK=", "--env=SSH_AGENT_PID=",
		"--env=HTTP_PROXY=", "--env=HTTPS_PROXY=", "--env=ALL_PROXY=",
		"--env=http_proxy=", "--env=https_proxy=", "--env=all_proxy=",
	}
}

type ansibleAttempt struct {
	stage artifactStage
	args  []string
}

func (r *DeploymentRunner) prepareAnsibleAttempt(
	ctx context.Context,
	request localStepAttempt,
) (ansibleAttempt, error) {
	var prepared ansibleAttempt
	if err := stepdefinition.Definition(request.step).ValidateKind(); err != nil {
		return prepared, err
	}
	a := request.step.Ansible
	key, err := r.ansibleSSHKey(ctx, request)
	if err != nil {
		return prepared, err
	}
	encoded, err := request.ansibleExtraVariables()
	if err != nil {
		return prepared, err
	}
	files := map[string]string{
		"playbook.yml": a.Playbook, "inventory.yml": a.Inventory,
		"vars.json": string(
			encoded,
		), "id_key": key, "known_hosts": a.KnownHosts,
		"ansible.cfg": ansibleConfig,
	}
	archive, err := ansibleArchive(files)
	if err != nil {
		return prepared, err
	}
	prepared.stage, err = r.createStagingVolume(
		ctx,
		request.deploymentID,
		ansibleMount,
	)
	if err != nil {
		return prepared, err
	}
	command := r.engine.command(
		ctx,
		"exec",
		"--interactive",
		prepared.stage.keeper,
		"tar",
		"-xf",
		"-",
		"-C",
		ansibleMount,
	)
	command.Stdin = bytes.NewReader(archive)
	// Helper diagnostics must not echo private input.
	if err := command.Run(); err != nil {
		return prepared, errors.New("stage Ansible inputs failed")
	}
	targets, err := a.Targets()
	if err != nil {
		return prepared, err
	}
	prepared.args = []string{
		"/ansible/playbook.yml",
		"--inventory",
		"/ansible/inventory.yml",
		"--extra-vars",
		"@/ansible/vars.json",
		"--limit",
		strings.Join(targets, ","),
	}
	if len(a.Tags) != 0 {
		prepared.args = append(
			prepared.args,
			"--tags",
			strings.Join(a.Tags, ","),
		)
	}
	if a.Check != nil && *a.Check {
		prepared.args = append(prepared.args, "--check")
	}
	return prepared, nil
}

func (r *DeploymentRunner) ansibleSSHKey(
	ctx context.Context,
	request localStepAttempt,
) (string, error) {
	a := request.step.Ansible
	variablesSnapshot, err := r.repo.ListDeploymentVariables(
		ctx,
		request.deploymentID,
	)
	if err != nil {
		return "", err
	}
	deployment, err := r.repo.Queries.GetDeployment(ctx, request.deploymentID)
	if err != nil {
		return "", err
	}
	resolved, err := ResolveDeploymentVariables(variablesSnapshot, deployment)
	if err != nil {
		return "", err
	}
	secret := false
	for _, variable := range resolved {
		if variable.Name == a.SSHKeyVariable {
			secret = variable.Secret
		}
	}
	if !secret {
		return "", errors.New(
			"SSH credential must be secret in the accepted variable snapshot",
		)
	}
	key, exists := request.environment[a.SSHKeyVariable]
	if !exists {
		return "", errors.New(
			"SSH credential is unavailable in the accepted variable snapshot",
		)
	}
	if _, err := ssh.ParsePrivateKey([]byte(key)); err != nil {
		return "", errors.New(
			"SSH credential must contain a valid unencrypted private key",
		)
	}
	// OpenSSH requires the final newline that pasted credentials can omit.
	key = strings.TrimSpace(key) + "\n"
	return key, nil
}
func (request localStepAttempt) ansibleExtraVariables() ([]byte, error) {
	variables := make(map[string]string, len(request.step.Ansible.ExtraVars))
	for target, source := range request.step.Ansible.ExtraVars {
		value, exists := request.ansibleVariable(source)
		if !exists {
			return nil, fmt.Errorf(
				"mapped variable %q is unavailable",
				source,
			)
		}
		variables[target] = value
	}
	return json.Marshal(variables)

}
func (request localStepAttempt) ansibleVariable(source string) (string, bool) {
	switch source {
	case "ARTIFACT_PATH":
		if request.artifact.volume != "" {
			return artifactMount, true
		}
	case "DURPDEPLOY_STAGE_DIR":
		if request.handoff.volume != "" {
			return deploymentStageMount, true
		}
	case "DURPDEPLOY_APPROVED_DIR":
		if request.approved.volume != "" {
			return approvedMount, true
		}
	}
	value, exists := request.environment[source]
	return value, exists
}

func ansibleArchive(files map[string]string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		body := files[name]
		if len(body) > ansibleStagingBytes-buffer.Len()-2048 {
			return nil, errors.New(
				"ansible inputs exceed private staging capacity",
			)
		}
		if err := writer.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Mode: 0600, Uid: 65534, Gid: 65534}); err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// The existing staging registry retains ownership for shutdown reconciliation.
// Remove private inputs after each attempt, before retry or another step.
func (r *DeploymentRunner) cleanupAnsibleAttempt(stage artifactStage) error {
	if stage.volume == "" {
		return nil
	}
	r.mu.Lock()
	for _, stages := range r.staging {
		for _, owned := range stages {
			if owned.volume == stage.volume && owned.creationUnconfirmed {
				stage.creationUnconfirmed = true
			}
		}
	}
	r.mu.Unlock()
	if stage.creationUnconfirmed {
		return errContainerCleanup
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := r.engine.command(ctx, r.engine.removeArgs(stage.keeper)...).
		CombinedOutput()
	if r.engine.removalError(output, err) != nil {
		return errContainerCleanup
	}
	output, err = r.engine.command(ctx, "volume", "rm", stage.volume).
		CombinedOutput()
	if err != nil &&
		!strings.Contains(strings.ToLower(string(output)), "no such volume") {
		return errContainerCleanup
	}
	return nil
}

const ansibleConfig = `[defaults]
inventory = /ansible/inventory.yml
private_key_file = /ansible/id_key
host_key_checking = True
retry_files_enabled = False
local_tmp = /tmp/ansible-local
remote_tmp = ~/.ansible/tmp
interpreter_python = auto_silent
nocows = True
force_color = False
display_args_to_stdout = False
[inventory]
unparsed_is_failed = True
[ssh_connection]
ssh_args = ` + ansibleSSHArgs + `
`
