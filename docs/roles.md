# DurpDeploy — Roles

DurpDeploy has three roles. Every user has exactly one. The role is set at
user-creation time and stored in `users.role`. Administrators can change roles
and reset passwords through **Admin → Users → Edit**. The `durpdeploy admin create`
CLI creates a new administrator; it does not update an existing account.

## The roles

| Role       | Reads                                         | Writes                                                  | Sees audit log |
|------------|-----------------------------------------------|---------------------------------------------------------|----------------|
| `admin`    | Everything                                    | Everything (projects, steps, releases, deployments, …) | Yes (`/admin/audit`) |
| `deployer` | Shared resources and projects they belong to | Project operations allowed by membership. Member management requires project admin. Lifecycle assignment and shared variables require global administrators. | No |
| `viewer` | Shared resources and projects they belong to | Their own Security settings and logout; application writes are blocked | No |

## Permission checks

Global administrators bypass project membership. Other users must belong to a
project to read or write its resources. Only global or project administrators
can manage its members. Deployment and artifact approvals require a global admin.
The browser and bearer API enforce these checks separately.
See [security](security.md#authorization) for middleware and implementation details.

Only global administrators manage shared lifecycle variables. Lifecycle assignment
grants a project access to shared values, including secrets. Only global
administrators create or change an assignment. Project administrators can retain
or remove an existing assignment and manage project overrides.
Review the lifecycle's project list before you add shared secrets.

## Viewer self-security exception

A viewer may manage only their own Security settings after the normal session,
CSRF, and fresh-reauthentication checks. This permits optional browser MFA
enrollment, recovery-code regeneration, and MFA disablement without granting
access to tokens or unrelated writes. A viewer may not manage another user's
security state. Administrator MFA reset remains admin-only.

## Picking a role for a new user

| You want them to…                              | Pick     |
|------------------------------------------------|----------|
| Manage users + see the audit log               | `admin`  |
| Deploy to projects, but not see the audit log  | `deployer` |
| Just watch the dashboard (status, logs)        | `viewer` |

`deployer` is the common day-to-day role for an engineer. `admin` is for the
operator who owns the box and the user list. `viewer` is for stakeholders
who want to follow deploys without the ability to trigger one.

## OIDC role authority

Successful OIDC login synchronizes the user's name, email, and role.
Configured group precedence is `admin`, then `deployer`, then `viewer`.
A role change invalidates browser sessions. Group removal is observed on the
next OIDC login; password login uses the last stored role.
See [OIDC configuration and recovery](authentik-oidc.md#security-limits-and-coexistence).
