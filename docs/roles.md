# DurpDeploy — Roles

DurpDeploy has three roles. Every user has exactly one. The role is set at
user-creation time and stored in `users.role`. There is no UI to change a
user's role today. Use the `durpdeploy admin` CLI or a direct DB update.

## The roles

| Role       | Reads                                         | Writes                                                  | Sees audit log |
|------------|-----------------------------------------------|---------------------------------------------------------|----------------|
| `admin`    | Everything                                    | Everything (projects, steps, releases, deployments, …) | Yes (`/admin/audit`) |
| `deployer` | Everything                                    | Everything — same writes as `admin`                     | No             |
| `viewer`   | Everything (dashboard, projects, deployments) | Only their own Security settings. All other writes return 403 | No             |

## Where the gates live

The role enforcement is in three places:

1. **Auth middleware** (`internal/auth/middleware.go`) — verifies the session
   cookie and injects the user into request context. Same for all three
   roles. No role check here.
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
security state. Administrator MFA reset remains admin-only.

## A note on per-project authorization

Per-project authorization is enforced through `project_members`. Global admins
bypass the membership check. Other users must be project members to read or
write project resources. Only global administrators and project administrators
can manage project members.

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

When optional OIDC is enabled, a successful OIDC login is authoritative for the
verified email identity and the configured group-to-role mapping. Group
precedence is `admin`, then `deployer`, then `viewer`. The login synchronizes the
local name, email, and role every time. A role change invalidates all browser
sessions for that user. Removing a provider group is applied on the next OIDC
login only. There is no SCIM or provider back-channel deprovisioning, so role
removal is not immediate between logins.

OIDC and password login coexist. Password login uses the most recently stored
local role. When unset, or set to `true`, the ID token must contain the literal
JSON boolean `email_verified: true`. Explicit lowercase
`DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false` accepts a present literal JSON
boolean `email_verified: true` or `email_verified: false` after normal ID token
signature, issuer, audience, and nonce verification. Missing, null, string, and
numeric claims remain rejected. This weakens identity assurance and is
appropriate only where Authentik independently
establishes address ownership. An email match links to exactly one existing
local account. Otherwise OIDC JIT-creates a user with an empty password. OIDC
reauthentication is done by the provider and bound back to the current
local session. Local logout clears only the DurpDeploy session, and OIDC does
not authenticate API tokens. Provider tokens, codes, and raw claims are never
stored. If the provider is unavailable, local password login remains available.

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
