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

What we do **not** defend against yet (see Known Gaps):

- Rate limiting on the login endpoint
- Audit log retention / tamper-proofing

Runner orphan cleanup on shutdown/timeout is handled (process-group SIGKILL,
see "Runner cleanup" below); per-step OS-level sandboxing
(chroot/namespaces/seccomp) is still future work.

---

## Authentication

**Implementation:** `internal/auth/auth.go`

- Session cookie (`session` key, `HttpOnly`, `SameSite=Lax`).
- `AuthMiddleware` validates the cookie on every protected route; redirects to
  `/login` on miss.
- Passwords hashed with **argon2id** (`time=2, memory=64 MB, threads=2`).
  Each wrong-password guess costs ~100 ms of server CPU and ~64 MB of RAM.
  Comparison uses `subtle.ConstantTimeCompare` to prevent timing attacks.
- No plaintext password is stored anywhere in the database.

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
  attempts should not be enumerable).

---

## Authorization

### Role-based access

**Roles:** `admin`, `deployer`, `viewer` (global; enforced by the
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

Both layers are required. Skipping the templ guard leaves dead-end UI;
skipping the middleware leaves a security hole.

### Per-project authorization

**Implementation:** `internal/auth/projectaccess.go`

- `RequireProjectAccess` middleware: admin bypass; 404 on missing project
  (hides existence); 403 on non-member; 200 on member.
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
letting them reach the other project routes. `canManageProject` also gates the
Members section of the project edit page (UI layer), mirroring the
CanWrite two-layer pattern.

### Admin-only routes

Routes under `/admin/users/*` are gated by `RequireRole("admin")`. The
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

### [CRITICAL] Plaintext password in redirect URL

**File:** `internal/handler/users.go:156–158`

After creating a user, the one-time password is embedded in the redirect URL
as a query parameter (`new_password=...`):

```go
redirect := fmt.Sprintf(
    "/admin/users?new_user_id=%d&new_user_email=%s&new_password=%s",
    created.ID, url.QueryEscape(created.Email), url.QueryEscape(password),
)
```

**Risk:** The plaintext password appears in:
- Server access logs of any reverse proxy that logs the full URL (Caddy,
  nginx, etc.) — the current code-level comment assumes only `r.URL.Path` is
  logged, but this is not guaranteed across deployments or future logging
  changes.
- Browser history.
- The `Referer` header on any subsequent navigation away from the page.

**Recommended fix:** Store the one-time password in a short-lived DB row (or
session flash) keyed by `new_user_id`, delete it after the first read, and
never embed it in the URL.

---

### [CRITICAL] Admin context missing project ID for `RequireProjectAccess`

**File:** `internal/auth/projectaccess.go:55–60`

When the user is a global admin, `RequireProjectAccess` calls
`next.ServeHTTP` without injecting the project ID into context via
`projectAccessKey{}`. Any handler that calls `auth.ProjectIDFromContext`
downstream will receive `(0, false)` for admins, which may cause silent
failures or incorrect behavior if the handler relies on the context value
being set.

**Recommended fix:** Inject the project ID into context for admins the same
way it is injected for members, before calling `next.ServeHTTP`.

---

### [MEDIUM] Unvalidated query parameters rendered in password banner

**File:** `internal/handler/users.go:67–68`

`new_user_email` and `new_password` are read from the query string without
validation or length cap and passed directly to the template:

```go
newUserEmail = r.URL.Query().Get("new_user_email")
newPassword  = r.URL.Query().Get("new_password")
```

A crafted URL like `/admin/users?new_password=<very-long-string>` can render
arbitrary content in the password banner (visible to any admin who follows
such a link).

**Recommended fix:** Only display the banner when `new_user_id` resolves to a
real user in the `users` slice already loaded from the DB. Ignore
`new_password` entirely if `new_user_id` is absent or does not match a known
user.

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

**Fix (P1-3, shipped):** Each step now runs in its own process group
(`cmd.SysProcAttr.Setpgid`, `internal/runner/runner.go`). Step timeout and
`Cancel` SIGKILL the whole group (`-pid`), not just the bash PID, so
grandchildren spawned by a script are reaped too. `cmd/server/main.go` now
traps `SIGINT`/`SIGTERM`, drains the HTTP server, and calls
`DeploymentRunner.KillAll` to SIGKILL every in-flight step's process group
before exiting — a server restart no longer orphans a running bash tree.

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
- **Decrypt path:** `Repository.GetVariable` / `ListVariablesByProject` /
  `GetReleaseVariable` / `ListReleaseVariablesByRelease` decrypt into a
  transient Go string on read. The plaintext is never written back to the
  DB, logged, or included in an error message — `secret.Box.Decrypt`
  returns only static error strings (`"authentication failed"`, etc.), never
  the ciphertext or plaintext.
- **Runner:** `DeploymentRunner.Run` still receives plaintext via the
  decrypting `ListReleaseVariablesByRelease` — the runner needs real values
  to inject as env vars — and the P0 log-redaction logic
  (`broadcastWriter.redact`, `secretValues`) is unchanged.
- **Acceptance check:** `sqlite3 durpdeploy.db 'select * from variables'`
  shows only base64 ciphertext in `value`; the app reads/writes normally
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

Until the server is restarted with the new key installed, **do not discard
the old key** — the rotate command already re-encrypted every row with the
new one, so the running server (still holding the old key in memory) will
fail to decrypt on its next read. Restart promptly after a successful
rotation.

---

## Known gaps (P1 / future work)

| Gap | Risk | Planned |
|-----|------|---------|
| ~~**Secret encryption at rest**~~ | ~~`release_variables.value` is plaintext; a DB read leaks secrets~~ | **shipped (P1-3)** |
| ~~**Runner orphan cleanup**~~ | ~~Killed/restarted server left orphaned bash children~~ | **shipped** |
| **Runner OS-level sandboxing** | A step still runs as the server's user with full DB/filesystem access (chroot/namespaces/seccomp not implemented) | future work |
| **Login rate limiting** | No rate limit on `/login`; argon2id cost is the only brute-force defense | P2 (custom Caddy build) |
| **Audit log retention** | No retention policy or tamper-proofing on `audit_log` | P2-5 |
| **Password reset flow** | No self-service reset; admin must delete + recreate the user | P2 |
| **Session invalidation on password change** | Changing a user's password does not invalidate existing sessions | P2 |

---

## What this document does not cover

- **Compromised teammate's laptop** — if the attacker has a valid cookie +
  CSRF token, they are that teammate. No defense available client-side.
- **Server root compromise** — an attacker with root can replace the binary,
  read the DB, or sniff process memory. OS-level problem.
- **Network-level DDoS** — handled upstream (Caddy, firewall).
- **Supply chain** — `go mod verify` and pinned versions only.

See `docs/attack-drill.md` for hands-on verification of the active defenses.
