# DurpDeploy — Roles

DurpDeploy has three roles. Every user has exactly one. The role is set at
user-creation time and stored in `users.role`. There is no UI to change a
user's role today; use the `durpdeploy admin` CLI or a direct DB update.

## The roles

| Role       | Reads                                         | Writes                                                  | Sees audit log |
|------------|-----------------------------------------------|---------------------------------------------------------|----------------|
| `admin`    | Everything                                    | Everything (projects, steps, releases, deployments, …) | Yes (`/admin/audit`) |
| `deployer` | Everything                                    | Everything — same writes as `admin`                     | No             |
| `viewer`   | Everything (dashboard, projects, deployments) | Only their own Security settings; all other writes return 403 | No             |

## Where the gates live

The role enforcement is in three places:

1. **Auth middleware** (`internal/auth/middleware.go`) — verifies the session
   cookie and injects the user into request context. Same for all three
   roles; no role check here.
2. **CSRF middleware** (`internal/auth/csrf.go`) — the coarse write gate.
   It rejects every state-changing request from a `viewer` except the narrow
   self-security path. This is what stops a viewer from clicking Deploy or
   saving a project.
3. **`RequireRole("admin")` middleware** on the `/admin/*` sub-group
   (`internal/server/server.go:170-177`) — gates only the audit-log viewer.

`deployer` and `admin` share every other behaviour today. The two are only
distinguishable in the `/admin/audit` page.

## Viewer self-security exception

A viewer may manage only their own Security settings after the normal session,
CSRF, and fresh-reauthentication checks. This permits optional browser MFA
enrollment, recovery-code regeneration, and MFA disablement without granting
access to tokens or unrelated writes. A viewer may not manage another user's
security state; administrator MFA reset remains admin-only.

## A note on per-project authorization

Today, **any `deployer` can deploy to any project**. There is no project
membership check yet. P1-1 in the team-hardening plan introduces a
`project_members` table that restricts deployers to projects they're a
member of. Until that ships, the practical "least privilege" is to make
non-admins `viewer`.

## Picking a role for a new user

| You want them to…                              | Pick     |
|------------------------------------------------|----------|
| Manage users + see the audit log               | `admin`  |
| Deploy to projects, but not see the audit log  | `deployer` |
| Just watch the dashboard (status, logs)        | `viewer` |

`deployer` is the common day-to-day role for an engineer. `admin` is for the
operator who owns the box and the user list. `viewer` is for stakeholders
who want to follow deploys without the ability to trigger one.

## Programmatic role check

The middleware writes the user into request context, so any handler can do:

```go
u := auth.UserFromContext(r.Context())
if u == nil || u.Role == "viewer" {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

In practice the global CSRF gate covers the common case. The pattern above is
useful for finer-grained handler-level checks (e.g. "only `admin` can
toggle a feature flag" — that lands in P2).
