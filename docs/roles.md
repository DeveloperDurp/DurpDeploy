# DurpDeploy roles

The bearer [script warning API](bearer-lint.md) accepts caller text from every valid effective role.
Scoped and global PATs can use the exact `POST /api/v1/lint` operation.
This read-only exception grants no stored script or project access.
Other POST operations retain their existing permission checks.

DurpDeploy uses four permission levels: `viewer < deployer < maintainer < admin`.
Each user has one global role.
Administrators manage global roles through **Admin → Users** or the public user API.

Local user updates and deletions must leave at least one administrator.
The web form and API reject a mutation that would remove the final administrator with 422.
Create or promote a replacement administrator before handing over the role.
OIDC role synchronization remains provider-authoritative and can remove the final administrator role.
Use the [host recovery procedure](deploy.md#forgot-the-admin-password) if no administrator remains.

## Capabilities

| Role | Read accessible data and logs | Deployment execution | Content authoring |
|---|---|---|---|
| `viewer` | Yes | No | No |
| `deployer` | Yes | Start existing releases, retry, re-run, and cancel deployments | No |
| `maintainer` | Yes | Yes, including runbooks and rollback | Yes, within existing administrator boundaries |
| `admin` | All data | Yes | Yes, including instance administration |

Deployment execution can create execution records.
Deployers cannot create, edit, refresh, reorder, or delete project content.
This restriction includes scripts, steps, runbooks, releases, variables, packages, templates, settings, notifications, and schedules.
Deployers cannot execute, retry, or cancel runbooks.
Deployers cannot request rollback or approve deployments and artifacts.

Viewers and Deployers can log out and manage their own Security settings.
Every role can change its own local password after current-password verification.
The public password-change API requires an unrestricted global token and the current password.
It rejects project-scoped tokens and tokens with a permission ceiling.
These browser actions require the existing CSRF and reauthentication checks.
Only Maintainers and administrators can manage their own API tokens.
The server CLI can provision tokens for restricted users.

## Project memberships

Global administrators bypass project membership.
Other users need membership to read a project.
Project memberships support all four roles.

Ordinary project operations require permission from the global role, project membership, and any explicit token ceiling.
A lower permission at any layer restricts the operation.

The project overview shows the global role, project role, and effective project role.
Steps, Releases, and the project menu explain unavailable authoring actions.
The account menu explains the global role required for API tokens.
For global-role restrictions, ask an administrator to use the Users page.
For project-role restrictions, ask a project administrator to use Project member settings.
Restricted members can read the Members section without gaining editing permission.

Deployment recovery uses the Deployer requirement.
Authoring and rollback recovery use the Maintainer requirement.

A `viewer` membership permits reads.
A `deployer` membership also permits deployment execution.
A `maintainer` membership also permits content authoring.
An `admin` membership also permits member management when the user's global role permits authoring.

Member management requires a global administrator or a project administrator with global Maintainer permission.
An explicit token must also have an `admin` ceiling.
Project administrators cannot grant global roles.

Only instance administrators with the global `admin` role can export secret variables.
Project administrator membership does not grant this permission.
Each export requires fresh password or OAuth/OIDC reauthentication.
Local password reauthentication also requires an enrolled MFA factor when MFA is enabled.
API exports require a global token with administrator permission and approval through the same user's browser session.

Deployment, runbook, and artifact approvals require a global administrator.
Only global administrators manage shared lifecycle variables, lifecycle stages, environment verification, and lifecycle or environment deletion.
Lifecycle assignment can expose shared secrets.
Only global administrators create or change lifecycle assignments.
Authorized project administrators can retain or remove an existing assignment.

## Shared environment execution

Environments are shared execution resources without project ownership or separate caller memberships.
Effective Deployer permission on a project permits deployment of its existing releases to any environment when the project has no lifecycle.
The global role, project membership, and token ceiling must each permit deployment.
This permission includes environments used by projects that the caller cannot access.
Environment-name visibility alone does not grant deployment permission.

Lifecycle-bound projects can deploy only to their lifecycle stages.
Promotion and approval gates still apply.
Force cannot bypass lifecycle environment eligibility.
Scheduled executions recheck these gates when they run.
Schedule authoring still requires effective Maintainer permission.

The selected environment determines matching remote agents and environment-specific variable values.
Variable capture uses the source release and its project's assigned lifecycle.
Selecting an environment does not import another project's variables or grant access to that project.
Release and deployment snapshots retain their existing behavior.

Treat every free-floating project's authorized script authors and deployment operators as trusted users of every environment's matching agent fleet.
Agent labels select execution resources without establishing a project authorization boundary.
Use separate instances when projects must not share environment execution authority.

## Shared directory and environment names

Environment names are shared instance metadata.
Every authenticated browser user can list all environment names at `/environments`, without project membership.
Global bearer tokens have the same visibility at `/api/v1/environments`.
Project-scoped tokens list environments eligible through selected projects, their lifecycles, or execution history.
An accessible selected project without a lifecycle makes all environments eligible.

Global administrators and project administrators with global Maintainer permission can see all user names and emails in the member picker.
Project members can read roster names, emails, and project roles.
Non-members without global administrator permission cannot open the project member page.
The separate `/admin/users` page and `/api/v1/admin/users` endpoint require a global administrator.

This shared-infrastructure model does not provide tenant isolation for environment names or member-picker directory data.
Directory visibility does not grant project access or member management permission.

## Project groups

Project groups organize the project list without changing project permissions.
Each project can belong to one group.
Projects without a group appear in Ungrouped.
Deleting a group preserves its projects.

Only global administrators can create, rename, or delete shared groups.
Project Maintainers and administrators can move authorized projects into visible groups or Ungrouped.
Use Manage Groups for all group management and project assignments.
The Projects page provides browsing controls.
The global role, project membership, and token ceiling must each permit authoring.
Viewers and Deployers cannot change groups or assignments.

Group lists and counts include only accessible projects.
Only global administrators using global access see empty groups.
Scoped tokens see only groups containing selected, accessible projects.
Scoped tokens cannot create, rename, or delete shared groups.

## API tokens

Both `global` and `scoped` tokens support all four permission ceilings.
Global tokens cover projects available to the owner.
Scoped tokens require one or more selected projects.
The server checks the owner's current permissions for every request.
A token cannot increase the owner's permissions.

New global ceilings cannot exceed the owner's global role.
New scoped ceilings cannot exceed the owner's authority in any selected project.
Global Maintainers with project Admin membership can mint scoped `admin` tokens for delegated member management.
Global administrators bypass project membership checks.
Existing tokens retain their stored ceilings and remain limited by the owner's current permissions.

Legacy global tokens without a ceiling inherit the owner's current permissions.
Name-only creation through the API, CLI, or legacy web form retains this behavior.
The token settings page defaults to all accessible projects and a `viewer` ceiling.

Tokens with explicit ceilings cannot access token management APIs.
Scoped tokens cannot create projects, change shared resources, or access instance administration.
Project templates belong to their source project.
Current source-project membership controls template lists and previews.
Saving or applying a project template requires effective Maintainer permission on the source project.
Applying a template also requires permission to create projects.
Scoped tokens can read and save templates through selected project routes.
Scoped tokens cannot apply templates or browse the global project-template list.
Only global administrators can assign a lifecycle to the new project.
Templates disappear when their source project is deleted.
Existing projects created from those templates remain independent.
A scoped `admin` token can manage authorized project memberships.
Approval also requires a global administrator as the owner.

Scoped reads include assigned lifecycles and eligible environments.
Scoped tokens cannot read global templates or shared lifecycle variable administration endpoints.
Existing secret masking, pagination, expiry, revocation, and log stream rechecks remain active.
An empty project selection never becomes global access.
Deleting the last selected project makes the token unusable.

Use the owner's authorized browser session or a legacy global token to edit grants.
Use an explicit `global` scope to remove project restrictions.

Provision a token from the server host:

```bash
durpdeploy tokens create --user operator@example.com --name deploy \
  --scope scoped --role deployer --project-id 12 --project-id 34
durpdeploy tokens create --user engineer@example.com --name author \
  --scope global --role maintainer
```

## Upgrade behavior

Migration `064`, or SQL Server migration `049`, runs during startup.
The migration changes existing Deployer users, project memberships, and explicit token ceilings to Maintainer.
This change preserves their previous authoring capabilities.
Viewer and administrator assignments remain unchanged.
Legacy global tokens keep their empty ceiling and inherit the migrated owner's current role.
Token project selections, credentials, identities, and history remain intact.

New Deployer assignments use the restricted execution permissions.
An administrator can change migrated Maintainers to Deployer when execution access is sufficient.
Database rollback is refused because older applications grant authoring permission to Deployers.

## OIDC role authority

OIDC precedence is `admin > maintainer > deployer > viewer`.
Configure the optional `DURPDEPLOY_OIDC_MAINTAINER_GROUP` with a distinct group name.
An absent Maintainer mapping grants no Maintainer access through OIDC.
The existing Deployer group maps to the restricted Deployer role.

Successful OIDC login synchronizes the user's name, email, and global role.
This synchronization can replace a migrated Maintainer role with Deployer.
Before upgrading, configure the Maintainer mapping for groups that need existing authoring access.
Keep a distinct Deployer group for operators who need execution access.
Role changes invalidate browser sessions.
Group removal takes effect on the next OIDC login.
Password login uses the last stored role.
See [OIDC configuration and recovery](authentik-oidc.md).

## Credential rotation

Project credential metadata and rotation require effective Maintainer permission for writes.
Accessible project credentials remain readable without secret values.
Viewers and Deployers receive no rotation controls.
Shared lifecycle credentials require a global administrator and unrestricted global access.
Scoped tokens cannot manage shared lifecycle credentials.
Rotation does not grant secret export permission.
