# Curated template library

Open **Templates → Template Library** to browse individual steps and complete workflows.
Open **Insert from Template** in a project's Steps page to reach the library with that project selected.
Select an entry to preview its ordered scripts, variables, interpreter, execution target, dependencies, and permissions.

The library contains 19 entries.
Each entry has a version and creates editable copies.
These six templates have Bash, PowerShell 7, and Python 3 variants:

| Template | Contents |
| --- | --- |
| Download an artifact | Download one HTTPS file into managed staging. Supply an optional fresh bearer token. |
| Copy or install a file | Install one file with mode `0755`. Deployment execution replaces the destination file. |
| Restart a Linux service | Restart a provisioned systemd service and check its active state. |
| Check HTTP health | Check an HTTP or HTTPS endpoint with bounded timeouts and retries. |
| Deploy a Linux service | Download a single executable, install it, restart its existing service, and check HTTP health. |
| Deploy a Compose application | Pull images, start a provisioned Docker Compose application, and check HTTP health. |

The existing slugs select Bash entries.
Append `-powershell` or `-python` to select another interpreter.
For example, use `linux-service-powershell` or `compose-application-python`.
All three variants target provisioned Linux hosts.
PowerShell entries use `pwsh` and do not add Windows support.
Python entries use `python3` and standard-library HTTP clients.
PowerShell and Python downloads keep credentials out of command arguments and cross-origin redirects.
Each host must have the interpreter and native tools listed in its preview.

## Configure and apply

Choose a project where you have effective Maintainer permission.
Supply every required variable.
Supply fresh secrets through the secret inputs.
Review every script and dependency.
Select registered agent labels for the intended Linux hosts.
Confirm the review checkbox.
Select **Apply Template**.

The Linux and Compose variants use agent host mode.
Each matching agent runs the steps.
For one host, select a label unique to that host.
Host scripts run with the agent account's authority.
Server containers receive no host execution fallback or runtime socket.

Prepare the listed tools, paths, services, and permissions before execution.
For the Linux workflow, provision the destination directory and service unit first.
For Compose, provision an absolute Compose file that uses `${APP_IMAGE}` for the application image.
Configure registry authentication on the agent when required.
Templates do not install tools, provision infrastructure, or grant privileges.

The artifact download uses HTTPS with certificate validation.
Use `ARTIFACT_TOKEN` for optional bearer authentication.
Do not include credentials in artifact or health URLs.
The Bash download sends the token through curl configuration input rather than command arguments.
Install GNU `mv` before running the Bash download.
Keep the managed `artifact` destination as a file path.
The download rejects a directory at that path.
`FILE_SOURCE` defaults to `artifact` within `DURPDEPLOY_STAGE_DIR` when empty.
The file installation step replaces its destination only when you later execute a deployment.

Application adds independent project steps and variable definitions in one transaction.
New steps append after existing steps in template order.
Existing project or inherited lifecycle variable names cause `409` without changes.
Rename conflicting configuration or select another project before applying.
The initial library does not overwrite or merge existing variables.

Application starts no deployment and creates no release, schedule, or runbook.
Edit the copied steps and variables through the existing project editors.
Create a release and deploy it separately when configuration is ready.
Later library updates do not modify copied records or existing release snapshots.

## Terraform workflow

Select `terraform-plan-apply` to copy the Terraform workflow.
It uses the existing artifact approval system in server containers.
It does not use agent host mode.
Do not supply `agent_selectors` for this entry.

Supply `TF_CONTAINER_IMAGE` with a reviewed Linux toolchain image.
Application sets both copied container image fields from this value.
Later variable edits do not change those fields.
Include Bash, Python 3, Terraform, provider plugins, and configuration in that image.
Include GNU `cp` to preserve file modes without copying private SELinux labels.
Supply `TF_CONFIG_DIR` with the configuration directory inside the image.
Include `.terraform.lock.hcl` in that directory.
Configure a remote backend with state locking.
The workflow rejects the local backend.
Server containers use bridge networking and have no host mounts or runtime socket.

For an HTTP backend, supply optional `TF_HTTP_USERNAME` and secret `TF_HTTP_PASSWORD` values.
For another backend, configure the copied steps with its required secret variables before creating a release.
Do not bake credentials into the image or backend arguments.
Keep configuration, plans, and state private because they can contain sensitive values.

The first step copies configuration into executable scratch space.
It excludes local state, cached backend data, and old plans.
It runs `terraform init`, `terraform validate`, and `terraform plan`.
It saves the binary plan and review JSON in managed staging.
Keep `terraform-context` absent before the planning step.
The step rejects an existing context and preserves its files.
It then pauses for human approval.
Inspect the exact artifact before approving it.
The review summary alone does not verify the binary plan.

The second step restores the approved context into executable scratch space.
It applies the exact saved plan without generating another plan.
Terraform rejects stale remote state.
Rejection stops the workflow before apply.
Use the same reviewed toolchain image and provider versions for both steps.
Edit both copied step image fields together when changing the toolchain.
Use configuration that supports relocation between isolated container scratch directories.
This workflow does not install Terraform, create a backend, or execute a deployment when copied.

## Public API

Global tokens can browse `GET /api/v1/template-library` and preview `GET /api/v1/template-library/{slug}`.
Lists use the existing `items`, `total`, `limit`, and `offset` envelope.
Project routes support selected-project tokens without exposing shared user templates.

```text
GET  /api/v1/projects/{id}/template-library
GET  /api/v1/projects/{id}/template-library/{slug}
POST /api/v1/projects/{id}/template-library/{slug}/apply
```

Use the preview's version and variable keys in an application request:

```json
{
  "version": "1",
  "reviewed": true,
  "agent_selectors": ["app-host-01"],
  "variable_values": {
    "HEALTH_URL": "https://app.example.test/health"
  }
}
```

This example applies `http-health-check`.
Use the preview's configuration definitions for other templates.
Optional inputs can be omitted.
Required inputs must contain values.
Host entries require registered agent labels.
The Terraform entry requires no labels and rejects nonempty selectors.
Secret inputs create encrypted secret variables and are absent from responses and audit details.

Successful application returns `201` with project, slug, version, and created-record counts.
Invalid configuration returns `422`.
A stale library version or existing variable name returns `409`.
Refresh the preview before retrying a stale version.
Missing entries return `404`.
Viewers, Deployers, and tokens below Maintainer permission cannot apply templates.
Browser writes also require CSRF protection.
Successful applications produce the `apply_library_template` audit event.

## Library maintenance

Definitions reside in `internal/repository/template_library_catalog.go`.
Scripts reside in `internal/repository/template_library/` and are embedded in the binary.
Change the entry version when its scripts, requirements, or configuration contract changes.
Keep a blank line after the final PowerShell block because the container runner reads standard input.
Never add credentials to bundled definitions.
Keep defaults independent of local project, environment, and agent identities.
The library has no remote publishing or marketplace integration.
