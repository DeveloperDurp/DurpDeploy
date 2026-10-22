# DurpDeploy roles

DurpDeploy uses four permission levels: `viewer < deployer < maintainer < admin`.
Each user has one global role.
Administrators manage global roles through **Admin → Users** or the public user API.

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

Deployment, runbook, and artifact approvals require a global administrator.
Only global administrators manage shared lifecycle variables, lifecycle stages, environment verification, and lifecycle or environment deletion.
Lifecycle assignment can expose shared secrets.
Only global administrators create or change lifecycle assignments.
Authorized project administrators can retain or remove an existing assignment.

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
