# Native Ansible steps

An Ansible step invokes `ansible-playbook` directly inside a server Linux container.
Bash, PowerShell, and Python steps remain available.
Remote agents do not support Ansible steps in this version.

## Configure a step

1. Open the project Steps page.
2. Select Add Step.
3. Select Ansible in Step type.
4. Enter a YAML playbook.
5. Enter static YAML inventory.
6. Select an execution image with Ansible, Python, and the required collections.
7. Select Connection.
8. For SSH connections, select Network enabled and an SSH credential reference.
9. For SSH connections, enter verified SSH host keys in SSH known_hosts.
10. Enter explicit extra variable mappings.
11. Select Preview targets.
12. Save the step.

The execution image must support user `65534:65534`, a read-only root filesystem, and writable `/tmp` storage.
The image must not require a host runtime socket.
The server does not install dependencies or provide a host execution fallback.
Missing executables, collections, and connection tools fail the deployment.

## Local connections

Set `ansible.connection` to `local` for work inside the isolated server execution container.
Omit `connection` or set it to `ssh` for the existing SSH behavior.
Local mode keeps the existing UID, read-only root filesystem, image, network selection, and cleanup rules.
It does not execute on the server host.
Remote agents remain unsupported.

Use `all: {hosts: {localhost: {}}}` as the local inventory.
Local inventory permits only `localhost`, without host connection options.
Omit `ssh_key_variable` and `known_hosts` in local mode.
The adapter rejects mixed connection settings and skips SSH credential resolution and SSH input staging.
SSH mode still requires an enrolled credential, verified host keys, and OpenSSH in the execution image.

Use this minimal API step for an API-only playbook:

```json
{
  "name": "Local API operations",
  "kind": "ansible",
  "execution_target": "local",
  "container_image": "registry.example/ansible@sha256:REPLACE_WITH_DIGEST",
  "network_mode": "bridge",
  "ansible": {
    "connection": "local",
    "inventory": "all: {hosts: {localhost: {}}}",
    "playbook": "- hosts: localhost\n  connection: local\n  gather_facts: false\n  tasks:\n    - ansible.builtin.debug: {msg: ready}\n",
    "extra_vars": {}
  }
}
```

Select `bridge` when tasks need API network access.
Select `none` when tasks need no network access.
Releases, runbooks, execution snapshots, retries, and execution check mode preserve the selected connection.

### Packaged playbooks and staging output

Configure a project package repository containing `playbooks/export.yml`.
Create a release to capture its package pin.
Use these fields in the local step:

```json
{
  "connection": "local",
  "inventory": "all: {hosts: {localhost: {}}}",
  "playbook": "- ansible.builtin.import_playbook: '{{ package_dir }}/playbooks/export.yml'\n",
  "extra_vars": {
    "package_dir": "ARTIFACT_PATH",
    "stage_dir": "DURPDEPLOY_STAGE_DIR",
    "api_token": "DEPLOY_TOKEN"
  }
}
```

Adapt the packaged playbook to explicit extra variables:

```yaml
- hosts: localhost
  connection: local
  gather_facts: false
  environment:
    HOME: /tmp
    XDG_CACHE_HOME: /tmp/cache
    HELM_CACHE_HOME: /tmp/helm/cache
    HELM_CONFIG_HOME: /tmp/helm/config
    HELM_DATA_HOME: /tmp/helm/data
  tasks:
    - ansible.builtin.uri:
        url: https://api.example/kubeconfig
        headers:
          Authorization: "Bearer {{ api_token }}"
        return_content: true
      register: kubeconfig_response
      no_log: true
    - ansible.builtin.copy:
        content: '{{ kubeconfig_response.content }}'
        dest: '{{ stage_dir }}/kubeconfig'
        mode: '0600'
      when: not ansible_check_mode
      no_log: true
```

Replace the example API URL with your service endpoint.
Use `no_log` on tasks that handle secrets.
Use writable `/tmp` paths for Ansible, Helm, and other tool caches.
Replace project-secret environment lookups with mapped extra variables.
The adapter does not export all project variables.
The package mount stays read-only.
The deployment staging directory accepts generated files.

In a subsequent script step, read `$DURPDEPLOY_STAGE_DIR/kubeconfig` or set `KUBECONFIG` to that path.
In a subsequent Ansible step, map `DURPDEPLOY_STAGE_DIR` explicitly again.
Preserve any separate handoff operation after the playbook.
Check mode can skip API requests and file writes.
Make subsequent steps tolerate absent output when you use check mode.

Use an image digest when reproducible image contents are required.
An image tag in a snapshot does not freeze the registry contents behind that tag.

## Inventory and options

Use one YAML document for each playbook and inventory.
The playbook root must be a sequence.
The inventory root must be a mapping.

```yaml
all:
  children:
    web:
      hosts:
        web1:
          ansible_host: 192.0.2.10
          ansible_port: 22
          ansible_user: deploy
```

Inventory supports static groups, nested `children`, and literal hosts.
Group names must be unique.
Define `all` and `ungrouped` only at the inventory root.
Do not add child groups to `ungrouped`.
Quote inventory strings such as `yes`, `no`, `on`, and `off`.
Host limits use Ansible's implicit membership for both special groups.
Host settings support `ansible_host`, `ansible_port`, and `ansible_user` only.
Inventory files, dynamic inventory, inventory plugins, aliases, custom tags, and templated addresses are unsupported.
Host limits accept comma-separated inventory host or group names.
Pattern expressions and unrestricted command-line options are unsupported.

Tags select playbook tasks.
The preview lists static inventory targets selected by the host limit.
Trusted playbooks can delegate tasks or cause effects beyond those targets.

## Execution check mode

Select **Ansible check mode** when you start a deployment or runbook execution.
The Deploy page shows this option only when the selected release contains an Ansible step.
The page checks the release snapshot, including releases with both script and Ansible steps.
Each fresh execution defaults to apply mode.
The application does not remember the selection for the next deployment.
Schedules use apply mode.

Check mode passes `--check` to each Ansible step in that execution.
Other step kinds and verification steps run normally.
Some Ansible modules do not support check mode.
Trusted tasks can override check mode and make changes.
Check mode does not provide a read-only security boundary.

Retries and redeploys retain the source execution mode.
Successful check deployments do not satisfy promotion gates or become the current deployed version.
Check deployments cannot become rollback sources or targets.
Execution history shows an **Ansible check mode** badge for captured check runs.

Each YAML input permits 256 KiB.
Both inputs share a limit of 1024 YAML nodes.
Inventory permits at most 1000 hosts within that node limit.
Configuration permits 128 mappings and 64 tags.
Private staged inputs share an 8 MiB limit.

## Variables and credentials

Map each Ansible extra variable to a DurpDeploy variable name.
For example, `{"service_token":"DEPLOY_TOKEN"}` maps the effective `DEPLOY_TOKEN` value to `service_token`.
Empty mappings pass no user variables.
The adapter does not export resolved project variables into its process environment.
Environment-specific values retain their existing precedence.
Connection and internal Ansible variable names cannot be mapping destinations.

The available-variable browser shows configured names and supported predefined variables.
Map predefined environment identity variables explicitly when required.
Managed artifact and staging paths require their corresponding mounted deployment resources.

Create or enroll the SSH key through the existing Credentials page or credentials API.
The selected variable must be a secret credential accessible in the deployment environment.
Use a dedicated unencrypted private key.
Passphrase prompts and SSH agent forwarding are unsupported.
Step definitions store the variable reference without the key value.
Public step responses and target previews do not contain credential values.

Supply trusted `known_hosts` entries for every selected SSH address and port.
Host verification is always enabled.
Wrong keys and unknown hosts fail the connection.
The adapter disables inherited SSH proxies and configuration discovery.

Use Ansible `no_log` for tasks that handle secrets.
The existing log scrubber also filters captured secret values.
Trusted playbooks can disclose secrets through transformed output or remote effects.

## Snapshots and cleanup

Releases capture the playbook, inventory, mappings, credential reference, image, network, and options.
Accepted deployments capture their step source and encrypted effective variables.
Accepted executions also capture their Ansible check mode.
Older execution captures retain their historical per-step check options.
Legacy check steps also exclude a run from promotion, deployed-version selection, and rollback.
New executions override those old step options with the selected execution mode.
Project edits do not change existing captures.
Credential rotation follows the existing credential workflow.
New executions use replacement credentials while existing execution captures stay unchanged.

Each attempt receives a private tmpfs bundle mounted read-only at `/ansible`.
The adapter passes file paths and validated options as an argument array.
It does not place key values or extra-variable values in command arguments.
It removes the workload before removing the private staging keeper and volume.
Unconfirmed cleanup blocks subsequent work.
Cancellation does not undo changes already applied to an SSH target.

## Public API

Use the existing project step create, read, and update endpoints.
Set `kind` to `ansible`.
Omit script fields and `variable_names`.

```json
{
  "name": "Configure web hosts",
  "kind": "ansible",
  "execution_target": "local",
  "container_image": "registry.example/ansible@sha256:REPLACE_WITH_DIGEST",
  "network_mode": "bridge",
  "timeout_seconds": 300,
  "max_retries": 0,
  "ansible": {
    "playbook": "- hosts: all\n  tasks:\n    - ansible.builtin.ping:\n      tags: [deploy]\n",
    "inventory": "all:\n  hosts:\n    web1:\n      ansible_host: 192.0.2.10\n      ansible_user: deploy\n",
    "extra_vars": {"service_token": "DEPLOY_TOKEN"},
    "ssh_key_variable": "SSH_KEY",
    "known_hosts": "192.0.2.10 ssh-ed25519 REPLACE_WITH_VERIFIED_PUBLIC_KEY\n",
    "tags": ["deploy"],
    "limit": ["web1"]
  }
}
```

Submit the `ansible` object to `POST /api/v1/projects/{id}/ansible/preview` for target validation.
The response contains `targets`, a sorted array of selected host names.
The preview does not resolve credentials or contact hosts.
Existing bearer-token permission ceilings and project access checks apply.
Invalid step configuration returns `400` through project step endpoints.
Web validation returns `422` inside the active form.

Set `ansible_check_mode` to `true` in a deployment or manual runbook execution request.
Omit it or set it to `false` for apply mode.
The option requires at least one Ansible step.
Otherwise, the API returns `422`.
Do not send `check` inside a step or template configuration.

```json
{
  "release_id": 42,
  "environment_id": 7,
  "ansible_check_mode": true
}
```

Use `POST /api/v1/projects/{id}/deployments` for the request above.
Use `POST /api/v1/projects/{id}/runbooks/{runbookId}/executions` for manual runbooks.
Replace `release_id` with optional `version_id` in runbook requests.
Retry and redeploy endpoints keep their empty request bodies.
They copy the source mode without changing the source snapshot.

Deployment responses and runbook execution detail/history include `ansible_check_mode` as a nullable integer object.
`{"Int64":1,"Valid":true}` means check mode.
`{"Int64":0,"Valid":true}` means apply mode.
`Valid:false` means the execution uses historical per-step options.

## Log display

Ansible deployment and runbook logs show compact timestamps beside the output.
Point to a timestamp to read its full date and time.
Play, task, handler, and recap headings omit decorative star banners.
Status words remain visible with colors for successful, changed, skipped, warning, and failed output.

Recap counts use green for nonzero `ok` results and blue for nonzero `skipped` results.
Nonzero `changed`, `rescued`, and `ignored` counts use amber.
Nonzero `failed` and `unreachable` counts use red.
Zero counts stay muted.

Long messages wrap inside the output column.
Blank lines use less space.

The display uses the captured step type.
Reloaded pages use the same formatting as live streams.
API responses and log exports retain the original text, including banners and blank lines.
Read raw deployment logs at `GET /api/v1/deployments/{id}/logs`.
Read raw runbook logs at `GET /api/v1/projects/{id}/runbook-executions/{executionId}/logs`.
Use **Export all logs** to read the complete raw output.

## Remote-agent follow-up

The server rejects remote-agent Ansible steps before admission.
Existing `agent/1`, `agent/2`, and `agent/3` capabilities do not advertise native Ansible execution.
Future support requires coordinated changes in `DeveloperDurp/durpdeploy-agent` and DurpDeploy.
Define a versioned capability and typed input payload before enabling dispatch.
Define protected staging transfer and confirmed cleanup before sending credentials.
Keep legacy script payloads compatible.

## Test image licenses

The test image uses the pinned Python image digest in `testdata/ansible/Dockerfile`.
Its Python dependencies use exact versions and SHA-256 hashes in `requirements.txt`.
The image installs only binary wheels and defaults to user `65534:65534`.
The disposable SSH target explicitly selects root for host keys and `sshd` startup.
The image retains wheel license notices in `/opt/ansible-notices/THIRD_PARTY_NOTICES.txt`.

| Package | Version | License |
| --- | --- | --- |
| ansible-core | 2.21.5 | GPL-3.0-or-later |
| Jinja2 | 3.1.6 | BSD-3-Clause |
| PyYAML | 6.0.3 | MIT |
| resolvelib | 1.2.1 | ISC |
| cryptography | 50.0.2 | Apache-2.0 OR BSD-3-Clause |
| packaging | 26.3 | Apache-2.0 OR BSD-2-Clause |
| cffi | 2.1.1 | MIT-0 |
| MarkupSafe | 3.0.4 | BSD-3-Clause |
| pycparser | 3.12 | BSD-3-Clause |

OpenSSH `10.3_p1-r1` and libedit `20260508.3.1-r1` use permissive BSD-compatible licenses.
The base image contains Python under PSF terms and Alpine packages under their individual open-source licenses.
The test image is built locally and is not published by this change.
Retain required license notices and corresponding-source obligations when distributing derived images.
