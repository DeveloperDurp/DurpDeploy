# DurpDeploy — Security Reference

Consolidated security findings, threat model, and known gaps. Updated 2026-07-15.

---

## Threat model

DurpDeploy is a small-team internal deploy tool. The practical threat model is
**"the same access as you"** — a malicious authenticated teammate has the same
power as the operator. The defenses below are calibrated for that scope.

What we defend against:

- Unauthenticated deploys
- CSRF on a teammate's browser
- Replay with a stolen CSRF token (tokens are per-session random 16 bytes)
- Password DB leak (argon2id with per-user salt)
- Accidental writes by a viewer (UI gating + CSRF gate)
- Cross-project access by a non-member (per-project authorization middleware)
- Roster tampering by a non-admin project member (per-project admin gate on member add/remove)
- A leaked `variables`/`release_variables` DB file, on its own, does not disclose secret values (AES-256-GCM at rest)
- Remote agents do not receive the server database, server encryption key, or Docker socket
- Agent transport uses outbound-only mTLS with pinned peer fingerprints and one-time pairing

### OIDC boundary and threat model

OIDC is an optional login factor, not a replacement for local authentication.
The approved variables are `DURPDEPLOY_OIDC_ISSUER`,
`DURPDEPLOY_OIDC_CLIENT_ID`, `DURPDEPLOY_OIDC_CLIENT_SECRET`,
`DURPDEPLOY_OIDC_ADMIN_GROUP`, `DURPDEPLOY_OIDC_DEPLOYER_GROUP`,
`DURPDEPLOY_OIDC_VIEWER_GROUP`, `DURPDEPLOY_OIDC_DISPLAY_NAME`,
`DURPDEPLOY_OIDC_GROUP_CLAIM`, and
`DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED` (default `true`). It also requires the HTTPS canonical
`DURPDEPLOY_URL`. The provider redirect URI is exactly
`DURPDEPLOY_URL + /login/oidc/callback`. Scopes are `openid`, `profile`, and
`email`.

The provider is authoritative for a successful OIDC login's verified identity
and mapped group role. When unset, or set to `true`, the ID token must contain
the literal JSON boolean `email_verified: true`. With explicit lowercase
`DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false`, a present literal JSON boolean
`email_verified: true` or `email_verified: false` is accepted after normal ID
token signature, issuer, audience, and nonce verification. Missing, null,
string, and numeric claims remain rejected. This weakens identity assurance and
is appropriate only where
Authentik independently establishes address ownership. The first email match
links exactly one local account. Without a match, the application JIT-creates a user with an empty
password. Group precedence is admin, then deployer, then viewer. Each successful
OIDC login synchronizes the local name, email, and role. A role change deletes
the user's browser sessions. Removing a group is observed on the next OIDC
login only. There is no SCIM or provider back-channel deprovisioning, so this is
not instant deprovisioning.

Password login remains available and uses the most recently stored local role.
OIDC reauthentication is handled by the provider, then bound to the current
local session and stored OIDC identity. Logout is local only and clears the
DurpDeploy session, not the provider session. Provider tokens, authorization
codes, and raw claims are not persisted. OIDC does not authenticate API tokens,
and local MFA is not asserted by an OIDC login.

Provider outage is isolated from local authentication: password login, existing
sessions, health checks, and bearer API authentication remain usable. An
OIDC-created empty-password account has no self-service password reset. An
administrator must use the existing local user recovery process.

What we do **not** defend against yet (see Known Gaps):

- Audit log retention / tamper-proofing

Runner orphan cleanup on shutdown/timeout and the local step sandbox are shipped.
Remote agents are separately sandboxed as dedicated users with private state
directories and no server storage access. The agent does not provide SSH access.

---

## Authentication

**Implementation:** `internal/auth/auth.go`

- Session cookie (`session` key, `HttpOnly`, `SameSite=Lax`).
- `AuthMiddleware` validates the cookie on every protected route. Redirects to
  `/login` on miss.
- Passwords hashed with **argon2id** (`time=2, memory=64 MB, threads=2`).
  Each wrong-password guess costs ~100 ms of server CPU and ~64 MB of RAM.
  Comparison uses `subtle.ConstantTimeCompare` to prevent timing attacks.
- No plaintext password is stored anywhere in the database.

### Browser MFA

Browser MFA is optional. An enrolled user completes a browser login with their
password and one current TOTP code, passkey, or recovery code. TOTP is not
phishing-resistant. Passkeys are bound to the single origin and RP ID derived
from `DURPDEPLOY_URL`. Changing its hostname or origin invalidates the stored
credential relationship and requires passkey re-enrollment.

Recovery codes are one-time values. They are displayed only when first
generated or regenerated, and only their hashes are stored. Users must keep
the displayed values in approved protected storage. MFA ceremony and secret
responses use `Cache-Control: no-store`. Do not put recovery codes, TOTP
seeds, cookies, challenges, or assertions in URLs, logs, tickets, or docs.

The final `session` cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` when
`DURPDEPLOY_URL` uses HTTPS. Pending MFA cookies are separate from the final
session and do not authorize protected routes. Factor completion issues a new
browser session and records `reauthenticated_at`. Factor changes, disablement,
password changes, and administrator resets invalidate the affected browser
sessions and pending challenges.

Browser MFA protects browser sessions only. API tokens are single bearer
factors: `/api/v1/*` does not prompt for MFA, and MFA disablement or an
administrator reset does not revoke API tokens. Revoke API tokens separately
when their bearer value may have leaked.

Users enroll or recover factors from **Security**. An administrator may reset
another user's MFA only after fresh reauthentication. That reset removes the
target's factors, recovery codes, browser sessions, and pending challenges.
It intentionally preserves API tokens. This is an operational contract. The
browser ceremony end-to-end proof is tracked separately from this document.

---

## CSRF protection

**Implementation:** `internal/auth/csrf.go`

- Every `POST`/`PUT`/`PATCH`/`DELETE` requires a valid `csrf_token` form field
  or `X-CSRF-Token` header.
- Token is per-session, random 16 bytes, stored in the `sessions` table.
- Viewer rejections (read-only role attempting a write):
  - HTMX requests → `200` + `HX-Trigger: makeToast` (red toast, page stays).
  - Non-HTMX form submits → `403` + styled error page.
- CSRF rejections are **not** written to `audit_log` (intentional — failed
  attempts must not be enumerable).

---

## Authorization

### Role-based access

**Roles:** `admin`, `deployer`, `viewer` (global. Enforced by the
`CHECK (role IN ('admin', 'deployer', 'viewer'))` constraint in
`migrations/011_auth.sql` and by `validRoles` in `internal/handler/users.go`).
Per-project member roles are `admin`, `deployer`
(`migrations/013_project_members.sql`).  
**Reference:** `docs/roles.md`

Two-layer defense for viewer read-only enforcement:

1. **CSRF middleware** — rejects any state-changing request from a viewer at
   the protocol layer (always fires on the actual write attempt).
2. **CanWrite templ guard** — hides write affordances in the UI so a viewer
   never sees a useless form. `pages.CanWrite(ctx)` / `components.canWrite(ctx)`
   return `false` for viewers.

Both layers are necessary. Without the templ guard, the user interface has dead-end controls.
skipping the middleware leaves a security hole.

The narrow exception is self-security: a viewer may manage only their own
Security settings after the normal session, CSRF, and fresh-reauthentication
checks. A viewer cannot manage another user, deployments, projects, or tokens.

### Per-project authorization

**Implementation:** `internal/auth/projectaccess.go`

- `RequireProjectAccess` middleware: An administrator can bypass this check.
  A missing project causes a 404 response. A non-member gets a 403 response.
  A member gets a 200 response.
- `CreateProject` auto-adds the creator as project admin.
- `ListProjects` filters by membership for non-admins.

### Per-project member management

**Implementation:** `internal/handler/project_members.go`

`RequireProjectAccess` only enforces a **binary** "is a member" check — it
admits any member (per-project admin, deployer, or viewer) to every
`/projects/{id}/...` route. The finer-grained "is a per-project admin" rule
for member add/remove is enforced **at the handler level** via
`canManageProject(ctx, repo, user, projectID)`:

- Returns `true` for a global `admin` or a project member whose per-project
  `role` is `admin`.
- `AddMember` / `RemoveMember` return `403` when it returns `false`.

This keeps per-project deployers/viewers from editing the roster while still
letting them access the other project routes. `canManageProject` also gates the
Members section of the project edit page (UI layer), mirroring the
CanWrite two-layer pattern.

### Admin-only routes

Routes in `/admin/users/*` are gated by `RequireRole("admin")`. The
`CanWrite` templ guard is applied there too as belt-and-suspenders.

---

## Audit log

**Implementation:** `internal/audit/audit.go`, `audit.Middleware`

- Records every **successful** state change to the `audit_log` table.
- CSRF rejections and 4xx responses are **not** audited (intentional).
- Every new state-changing route must be added to `actionMap` in
  `internal/audit/audit.go` for a stable action name. The fallback heuristic
  (method + first path segment) is lossy.
- The `actionMap` covers the user-management routes
  (`create_user`, `update_user`, `delete_user`) and the project-member routes
  (`add_project_member`, `remove_project_member`).

---

## Middleware stack (protected routes)

All protected routes in `internal/server/server.go` go through these three
middleware in order:

1. `auth.AuthMiddleware(repo)` — session → user in context.
2. `auth.CSRFMiddleware()` — token check + viewer gate.
3. `audit.Middleware(repo)` — records successful state changes.

Do not reorder or skip any of these.

---

## Findings from code review (2026-07-15)

### [RESOLVED] Plaintext password in redirect URL

**File:** `internal/handler/users.go`

Resolved on 2026-09-02. Admin-created and reset passwords are no longer
redisplayed. Both forms require matching password and confirmation fields, and
successful requests redirect to `/admin/users` without a query string.

---

### [CRITICAL] Admin context missing project ID for `RequireProjectAccess`

**File:** `internal/auth/projectaccess.go:55–60`

For a global administrator, `RequireProjectAccess` calls `next.ServeHTTP`
without a project ID in `projectAccessKey{}`. Thus,
`auth.ProjectIDFromContext` returns `(0, false)`. A handler that uses this value
can give an incorrect result.

**Recommended fix:** Inject the project ID into context for admins the same
way it is injected for members, before calling `next.ServeHTTP`.

---

### [RESOLVED] Unvalidated query parameters rendered in password banner

**File:** `internal/handler/users.go`

Resolved on 2026-09-02 by removing the password banner and all credential query
parameter handling from the users page.

---

### [MEDIUM] `ApproveDeployment` accepts arbitrary `approved_by` string

**File:** `internal/handler/deployment.go` (`ApproveDeployment`)

The approver identity is taken directly from the form:

```go
approvedBy := strings.TrimSpace(r.FormValue("approved_by"))
if approvedBy == "" {
    approvedBy = "anonymous"
}
```

Any authenticated user can submit any string as the approver name, including
impersonating another user. The actual authenticated user identity is already
available in context via `auth.UserFromContext`.

**Recommended fix:** Ignore the `approved_by` form field. Use
`auth.UserFromContext(r.Context()).Email` (or `.ID`) as the canonical approver
identity.

---

### [LOW] `RedeployDeployment` skips the promotion gate

**File:** `internal/handler/deployment.go` (`RedeployDeployment`)

Re-running a deployment creates a new deployment record and dispatches the
runner without calling `checkPromotionGate`. A user can re-run a failed
production deployment even if the lifecycle gate would normally block it.

**Recommended fix:** Call `checkPromotionGate` in `RedeployDeployment` the
same way `ScheduleDeployment` and `CreateDeployment` do, or document this as
an intentional bypass (force-equivalent) and record `Forced=1` on the new
deployment row.

---

### [LOW] Runner uses `context.Background()` — cancellation is best-effort

**File:** `internal/handler/deployment.go` (all `go h.runner.Run(...)` calls)

The runner is dispatched with `context.Background()` rather than the request
context. This is intentional (the deploy must outlive the HTTP request), but
it means the only cancellation path is `runner.Cancel(id)`.

**Fix (P1-4, shipped):** Each step operates in its own process group. A step
timeout or `Cancel` sends SIGKILL to the group. Thus, the signal also stops
child processes. During shutdown, `DeploymentRunner.KillAll` stops all active
step process groups. A server restart does not leave a Bash process active.

---

## Secret encryption at rest (P1-3)

**Implementation:** `internal/secret/secret.go`, `internal/repository/repository.go`

The `value` column of both `variables` and `release_variables` is
AES-256-GCM encrypted before it ever reaches SQLite:

- **Key source:** `/etc/durpdeploy/key` (file, checked first) or
  `DURPDEPLOY_SECRET_KEY` (env, base64-encoded 32 bytes). The server calls
  `secret.LoadKey()` at startup and **refuses to boot** (`log.Fatalf`) if
  neither is configured — there is no "run with plaintext secrets" mode.
- **Encrypt path:** `Repository.CreateVariable` / `UpdateVariable` encrypt
  `value` before the INSERT/UPDATE. Release snapshot creation
  (`ReleaseHandler.CreateRelease` / `RefreshRelease`) re-encrypts each
  variable's value via `Repository.EncryptValue` before writing the
  `release_variables` row (values are never round-tripped through the DB
  in plaintext).
- **Decrypt path:** The repository decrypts a value into a temporary Go string.
  It does not write plaintext to the database or a log. It does not put
  plaintext in an error message. `secret.Box.Decrypt` returns only fixed error
  text.
- **Runner:** `DeploymentRunner.Run` receives plaintext from
  `ListReleaseVariablesByRelease`. The runner puts these values in environment
  variables. It also gives them to the `Scrubber` described below.
- **Acceptance check:** `sqlite3 durpdeploy.db 'select * from variables'`
  shows only base64 ciphertext in `value`. The app reads/writes normally
  through the UI because the repository layer decrypts/encrypts
  transparently.

### Key rotation runbook

```bash
# 1. Back up the DB first (see Backup below) — rotation is transactional
#    but a backup is cheap insurance.
sudo -u durpdeploy /usr/local/bin/durpdeploy secret-key rotate
```

This one-shot command (`cmd/server/main.go: runSecretKey`):

1. Loads the **current** key via `secret.LoadKey()` (same file/env lookup
   the server uses).
2. Generates a fresh random 32-byte key.
3. Inside a single DB transaction, decrypts every `variables` and
   `release_variables` row with the old key and re-encrypts it with the
   new one (`ListAllVariables`/`UpdateVariableValue`,
   `ListAllReleaseVariables`/`UpdateReleaseVariableValue`). A failure at
   any row rolls back the whole transaction — the DB is left entirely on
   the old key, never half-migrated.
4. Prints the new key (base64) to stdout.

After it prints successfully:

```bash
# install the new key (pick one)
echo '<printed-key>' | sudo -u durpdeploy tee /etc/durpdeploy/key >/dev/null
sudo chmod 0600 /etc/durpdeploy/key
# — or —
# update DURPDEPLOY_SECRET_KEY=<printed-key> in the systemd unit / env file

sudo systemctl restart durpdeploy
```

**Do not discard the old key before the server restart.** The rotate command
encrypts all rows with the new key. The active server still has the old key in
memory. Thus, it cannot decrypt a new value. Restart the server immediately
after a successful rotation.

---

## Log redaction (P1-5)

**Implementation:** `internal/runner/scrubber.go`, `internal/runner/runner.go`

DurpDeploy scrubs deployment logs before an SSE broadcast or a database write.
The old scrubber used `strings.ReplaceAll` for each line and secret. It did not
find some credential formats. It also did not find a secret in two writes or
a secret that contained a newline.

`broadcastWriter` now delegates to a `Scrubber` (`internal/runner/scrubber.go`),
built once per deployment run from that environment's secret variable
values:

- **Single compiled regular expression:** The scrubber escapes each literal
  secret with `regexp.QuoteMeta`. It sorts the secrets from longest to shortest.
  It also includes patterns for common credentials. These include bearer
  tokens, GitHub PATs, AWS keys, Slack tokens, and credential assignments. RE2
  processes the combined expression in linear time.
- **Configurable patterns:** Additional regex patterns can be added via the
  `DURPDEPLOY_EXTRA_SCRUB_PATTERNS` environment variable (comma-separated).
  These are appended to the common credential patterns at startup.
- **Buffered operation:** `broadcastWriter.Write` scrubs all text through the
  last newline in its buffer. Thus, it finds a secret in two writes. It also
  finds a secret that contains a newline.
- **Best-effort:** this catches known secret values and a handful of common
  token shapes, not every possible secret format. **Redaction is
  best-effort. Do not paste secrets into your script body. Use environment
  variables marked Secret.**

## Known gaps (P1 / future work)

| Gap | Risk | Planned |
|-----|------|---------|
| ~~**Secret encryption at rest**~~ | ~~`release_variables.value` is plaintext. A DB read leaks secrets~~ | **shipped (P1-3)** |
| ~~**Runner orphan cleanup**~~ | ~~Killed/restarted server left orphaned bash children~~ | **shipped** |
| ~~**Log redaction hardening**~~ | ~~Naive per-line `strings.ReplaceAll` missed common credential formats and multi-line/split secrets~~ | **shipped (P1-5)** |
| ~~**Runner OS-level sandboxing**~~ | ~~Steps run as a low-privilege user in a chroot'd scratch directory with cgroup limits~~ | **shipped (P1-4)** |
| ~~**Login rate limiting**~~ | ~~Password, MFA, and OIDC login surfaces lacked application limits~~ | **shipped** |
| **Audit log retention** | No retention policy or tamper-proofing on `audit_log` | P2-5 |
| **Password reset flow** | No self-service reset. Admin must delete + recreate the user | P2 |
| **Session invalidation on password change** | Changing a user's password does not invalidate existing sessions | P2 |

---

## What this document does not cover

- **Compromised teammate's laptop** — if the attacker has a valid cookie +
  CSRF token, they are that teammate. No defense available client-side.
- **Server root compromise** — an attacker with root can replace the binary,
  read the DB, or sniff process memory. OS-level problem.
- **Network-level DDoS** — handled upstream (Caddy, firewall).
- **Supply chain** — `go mod verify` and pinned versions only.
- **Compromised remote agent host:** An agent can run deployments for its assigned environments. Isolate it from the control-plane host. Rotate its pairing and certificate material.

See `docs/attack-drill.md` for hands-on verification of the active defenses.
