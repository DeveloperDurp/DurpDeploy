# DurpDeploy — Attack drill

Five-minute hands-on walkthrough of the most likely attacks against
the deployed instance, with the expected failure mode for each. Run this
after every deploy to a new VM, and again quarterly, to confirm the
defenses are still in place.

The drills assume you have a running instance reachable at
`https://durpdeploy.example.com` and you have shell access to the server's
SQLite database (via `sudo -u durpdeploy sqlite3 ...`). The server-side
queries in drill 4 require shell access to the box. Drills 1, 2 and 3 only
need `curl` for requests; drill 2 also reads the CSRF token from the server's
SQLite database. Use a disposable project with valid release and environment
IDs for deployment drills. Complete browser MFA when enrolled before copying
the session cookie from the browser.

---

## 1. Password guessing (brute force)

**Attack.** An attacker who can access the login endpoint tries many
passwords against a known email.

```bash
BASE=https://durpdeploy.example.com
for i in $(seq 1 20); do
  curl -s -o /dev/null -w "%{http_code}  %{time_total}s\n" \
    -X POST -d "email=admin@example.com&password=wrong$i" "$BASE/login"
done
```

**Expected.** Wrong-password attempts return `422`. The first five attempts for
the same normalized email and client IP in a 15-minute window reach password
verification; later attempts are throttled, still return `422`, and include
`Retry-After: 900`. Timing depends on the host and network.

**What defends.** `internal/auth/auth.go:HashPassword` uses argon2id with
`time=2, memory=64MB, threads=2`. Attempts that reach verification incur the
Argon2id CPU and memory cost; throttled attempts do not compute the hash.

**Detection.** Failed logins are deliberately **not** written to the
`audit_log` table. This is a privacy decision, so attackers cannot enumerate
which emails are real by counting rows. They DO appear in the
`request`-level slog output on the server:

```bash
docker compose logs --since 5m app | grep '"path":"/login".*"status":422'
```

A sudden spike in 422s on `/login` is the right alerting signal. Wire that
to your monitoring system. Throttled requests also emit an
`authentication request throttled` warning.

The application limits password attempts by client IP and by normalized
email-plus-IP, and applies separate IP limits to MFA and OIDC initiation. The
stock Caddy image needs no custom module. A large volume of throttle telemetry
still indicates an attack and can justify an additional firewall or edge limit.

---

## 2. CSRF via `curl` (stolen cookie)

**Attack.** A teammate is tricked into clicking a malicious link on
another site. The link has hidden form fields that POST to your
DurpDeploy with the teammate's session cookie. The attacker does not
have the cookie value — but the browser sends it automatically.

In this drill, use `curl` to simulate the attack. Use a valid session cookie
from the user interface. Send a POST request without the CSRF token. A real
cross-site form cannot know this token.

```bash
BASE=https://durpdeploy.example.com

# Use IDs from a valid release and a deployment environment in this project.
PROJECT_ID=1
RELEASE_ID=1
ENVIRONMENT_ID=1

# 1. Log in via the UI and copy its cookie when MFA is enrolled.
# This curl login is sufficient only for an account without MFA.
COOKIES=$(mktemp)
curl -s -c $COOKIES -o /dev/null -X POST \
  -d "email=admin@example.com&password=YOUR-PASSWORD" "$BASE/login"

# 2. Try to deploy without the CSRF token. This is what a cross-site
# form would send.
curl -s -b $COOKIES -o /dev/null -w "Status: %{http_code}\n" -X POST \
  -d "release_id=$RELEASE_ID&environment_id=$ENVIRONMENT_ID" "$BASE/projects/$PROJECT_ID/deploy"

# 3. Now send the same request WITH the CSRF token from your session.
# This should succeed (303 redirect to the deployment page).
SESSION_ID=$(awk '$6 == "session" { print $7 }' $COOKIES)
CSRF=$(sudo -u durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db \
  "SELECT csrf_token FROM sessions WHERE id='$SESSION_ID';")
curl -s -b $COOKIES -o /dev/null -w "Status: %{http_code}\n" -X POST \
  -d "release_id=$RELEASE_ID&environment_id=$ENVIRONMENT_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/deploy"
```

**Expected.** Step 2 returns `403` with a valid writer session. Step 3 returns
`303` when the selected release and environment pass deployment validation.
The cross-site
form, which has the session cookie but not the CSRF token, cannot
trigger a state change.

**Defense.** `internal/auth/csrf.go:CSRFMiddleware` requires a valid CSRF token
on each POST, PUT, PATCH, and DELETE request. Each session has one random
token. The server sends it only in the `X-CSRF-Token` header or the
`csrf_token` form field. A cross-site attacker cannot read these values.

**Detection.** A 403 on a state-changing endpoint is a CSRF rejection.
Like failed logins, these are NOT written to `audit_log` — only successful
state changes are audited. They show up in the slog request log:

```bash
docker compose logs --since 5m app | grep '"status":403'
```

A handful of 403s from a single IP is normal (cancelled form submits,
double-clicks). A flood of 403s from many IPs is a probing attack.

---

## 3. API token leak

**Attack.** A developer accidentally commits an API token to a public git
repository.

**What happens.** The bearer token grants the same access the user had. If
the user is an admin, the leaked token can deploy, create users, and modify
projects.

```bash
# Token found in a public repo
TOKEN=ddp_pat_xxxxxxxxxxxxxxxxxxxxxxxx
BASE=https://durpdeploy.example.com

# List projects
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/projects"

# Trigger a deployment using valid IDs in a disposable project.
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"release_id":1,"environment_id":1}' "$BASE/api/v1/projects/1/deployments"
```

**Expected.** A valid token works within its user's role, scope, and project
access until revocation or its configured expiry. CLI-created tokens have no
expiry; API/web creation can set one.

Browser MFA does not add a factor to this API request: API tokens are single
bearer factors. MFA reset does not revoke API tokens. Revoke the token itself
if its bearer value is exposed.

**Mitigation:**

1. Revoke the token immediately from `/admin/tokens` or via the CLI:
   ```bash
   durpdeploy tokens revoke <prefix>
   ```
2. Rotate deployment credentials the token could read or expose. Server-key
   rotation does not revoke tokens or undo plaintext disclosure. Use it only
   for exposure of the server key, after addressing the
   [current rotation limitation](security.md#key-rotation-runbook).
3. Audit the user's actions in `/admin/audit` between the token creation and
   revocation.

**Design defense.** Bearer tokens are hashed (SHA-256) at rest. The plaintext
is shown exactly once at creation. The request logger records `r.URL.Path`
only — headers, including `Authorization`, are never written to logs.

**Prevent tokens from going into version control.** Use environment variables or a
secrets manager for CI/CD pipelines.

---

## 4. Direct DB read (stolen backup / server compromise)

**Attack.** An attacker gets a copy of the SQLite database. Possible sources
include an incorrect backup, a stolen drive, and a compromised administrator
account. An `rsync` operation to the wrong host is another possible source.
The attacker wants to get the user passwords.

```bash
# Attacker has the DB file. Inspect users table.
sqlite3 durpdeploy.db "SELECT email, password_hash FROM users;"
```

**Expected.** The `password_hash` column contains argon2id-encoded strings
like:

```
$argon2id$v=19$m=65536,t=2,p=2$<base64-salt>$<base64-hash>
```

There is **no plaintext password anywhere in the database**.

**What defends.** `internal/auth/auth.go:HashPassword` writes the encoded
hash, never the plaintext. The `VerifyPassword` path hashes the candidate
the same way and compares in constant time (`subtle.ConstantTimeCompare`).
The parameters (`m=65536, t=2, p=2`) are the modern PHC-recommended
defaults for argon2id — they cost ~100 ms to compute and ~64 MB of memory
on the attacker's machine. A real attack against one password would
require running those parameters on a hashcat-class rig for hours per
guess. Against a unique salt per user, a dictionary attack has to pay
that cost per (user, guess) pair.

**What does NOT defend.** This drill does NOT protect session tokens, the
server encryption key, or the audit log itself. A DB read alone does not reveal
the encrypted values in `release_variables.value`, but an attacker who also
gets the matching server key can decrypt them. Audit log retention remains an
operational policy.

**Response.** If a backup or the live database leaked, reset affected passwords
through **Admin → Users → Edit**, retaining each account's role and memberships.
Password changes invalidate browser sessions and pending challenges. Revoke
exposed API tokens separately. `admin create` cannot reset an existing user;
deleting and recreating the account loses memberships, tokens, and audit-user
links. For administrator lockout, use the separate recovery-account procedure
in [the deployment runbook](deploy.md#forgot-the-admin-password).

Inspect the affected user's audit entries between compromise and recovery.

---

## 5. OIDC coexistence and recovery

**Attack.** The provider is unavailable, removes a user's mapped group, or a
user logs out locally after an SSO operation.

**Expected.** Provider failure returns a generic OIDC error while password
login, existing browser sessions, health checks, and bearer API authentication
remain usable. A removed group changes the local role only at that user's next
OIDC login. Local logout clears the DurpDeploy session but does not log out of
the provider. Provider tokens, authorization codes, and raw claims are not
persisted. OIDC does not authenticate API tokens, and an OIDC-created
empty-password account has no self-service password reset.

**Verify.** Use the [OIDC guide's checklist](authentik-oidc.md#5-verify-the-complete-path).
Test role removal, local logout, and provider outage on a disposable instance.
A role change must invalidate existing browser sessions. Use a separate local
administrator for recovery; revoke leaked API tokens independently.

---

## What this drill does not cover

These attacks are outside the application boundary:

- **Compromised teammate's laptop** — if the attacker has a teammate's
  actual cookie + CSRF token, they are that teammate. No defense
  available client-side.
- **Server root compromise** — an attacker with root on the box can
  replace the binary, read the DB, sniff process memory. OS-level
  problem, not DurpDeploy's.
- **Network-level DDoS** — handled upstream (Caddy, firewall).
- **Supply chain** — `go mod verify` and pinned versions only.

Remote agent transport is outbound-only and uses mTLS with pinned fingerprints.
If an agent host is compromised, stop its service, revoke the agent, rotate its
pairing and certificate material, and review work routed to it. See
`docs/agent-protocol.md` for the protocol boundaries.
