# DurpDeploy — Attack drill

Five-minute hands-on walkthrough of the most likely attacks against
the deployed instance, with the expected failure mode for each. Run this
after every deploy to a new VM, and again quarterly, to confirm the
defenses are still in place.

The drills assume you have a running instance reachable at
`https://durpdeploy.example.com` and you have shell access to the server's
SQLite database (via `sudo -u durpdeploy sqlite3 ...`). The server-side
queries in drill 4 require shell access to the box; drills 1, 2 and 3 only
need `curl`.

---

## 1. Password guessing (brute force)

**Attack.** An attacker who can reach the login endpoint tries many
passwords against a known email.

```bash
BASE=https://durpdeploy.example.com
for i in $(seq 1 20); do
  curl -s -o /dev/null -w "%{http_code}  %{time_total}s\n" \
    -X POST -d "email=admin@example.com&password=wrong$i" "$BASE/login"
done
```

**Expected.** Every attempt returns `422` and takes roughly **100 ms** of
server time. The `time_total` you see is mostly network on localhost and
mostly the argon2id hash on the server.

**What defends.** `internal/auth/auth.go:HashPassword` uses argon2id with
`time=2, memory=64MB, threads=2`. Each wrong-password guess costs the
attacker ~100 ms of server CPU *and* ~64 MB of memory for the duration of
the guess. At 10 attempts/sec, 1 vCPU is fully saturated and the server
stops responding to other requests.

**Detection.** Failed logins are deliberately **not** written to the
`audit_log` table — that's a privacy decision, so attackers can't enumerate
which emails are real by counting rows. They DO appear in the
`request`-level slog output on the server:

```bash
sudo journalctl -u durpdeploy --since "5 min ago" | grep '"path":"/login".*"status":422'
```

A sudden spike in 422s on `/login` is the right alerting signal. Wire that
to whatever you use (Promtail, Loki, Slack via the P2 notifier).

The application limits password attempts by client IP and by normalized
email-plus-IP, and applies separate IP limits to MFA and OIDC initiation. The
stock Caddy image needs no custom module. A large volume of throttle telemetry
still indicates an attack and can justify an additional firewall or edge limit.

---

## 2. CSRF via `curl` (stolen cookie)

**Attack.** A teammate is tricked into clicking a malicious link on
another site. The link has hidden form fields that POST to your
DurpDeploy, using the teammate's session cookie. The attacker does not
have the cookie value — but the browser sends it automatically.

In this drill we **simulate** the attack with `curl`: we have a valid
session cookie (yours, from logging in via the UI), and we POST without
the CSRF token that a real cross-site form couldn't know.

```bash
BASE=https://durpdeploy.example.com

# 1. Log in via the UI (or grab a cookie from your browser dev tools).
COOKIES=$(mktemp)
curl -s -c $COOKIES -o /dev/null -X POST \
  -d "email=admin@example.com&password=YOUR-PASSWORD" "$BASE/login"

# 2. Try to deploy without the CSRF token. This is what a cross-site
# form would send.
curl -s -b $COOKIES -o /dev/null -w "Status: %{http_code}\n" -X POST \
  -d "release_id=1&environment_id=1" "$BASE/deployments"

# 3. Now send the same request WITH the CSRF token from your session.
# This should succeed (303 redirect to the deployment page).
SESSION_ID=$(awk '$6 == "session" { print $7 }' $COOKIES)
CSRF=$(sudo -u durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db \
  "SELECT csrf_token FROM sessions WHERE id='$SESSION_ID';")
curl -s -b $COOKIES -o /dev/null -w "Status: %{http_code}\n" -X POST \
  -d "release_id=1&environment_id=1&csrf_token=$CSRF" "$BASE/deployments"
```

**Expected.** Step 2 returns `403`. Step 3 returns `303`. The cross-site
form, which has the session cookie but not the CSRF token, cannot
trigger a state change.

**What defends.** `internal/auth/csrf.go:CSRFMiddleware` requires a valid
CSRF token on every POST/PUT/PATCH/DELETE. The token is per-session,
random, and never leaves the server's DB except via the `X-CSRF-Token`
header / `csrf_token` form field, which a cross-site attacker can't read
(`SameSite=Lax` cookie + the CORS default-deny).

**Detection.** A 403 on a state-changing endpoint is a CSRF rejection.
Like failed logins, these are NOT written to `audit_log` — only successful
state changes are audited. They show up in the slog request log:

```bash
sudo journalctl -u durpdeploy --since "5 min ago" | grep '"status":403'
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

# Trigger a deployment
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -d "release_id=1&environment_id=1" "$BASE/api/v1/deployments"
```

**Expected.** The token works until it is revoked. There is no automatic
expiration.

Browser MFA does not add a factor to this API request: API tokens are single
bearer factors. MFA reset does not revoke API tokens. Revoke the token itself
if its bearer value is exposed.

**Mitigation:**

1. Revoke the token immediately from `/admin/tokens` or via the CLI:
   ```bash
   durpdeploy tokens revoke <prefix>
   ```
2. Rotate the server secret key (`durpdeploy secret-key rotate`) if the
   leaked token had access to encrypted variables.
3. Audit the user's actions in `/admin/audit` between the token creation and
   revocation.

**Design defense.** Bearer tokens are hashed (SHA-256) at rest; the plaintext
is shown exactly once at creation. The request logger records `r.URL.Path`
only — headers, including `Authorization`, are never written to logs.

**Avoid putting tokens in version control.** Use environment variables or a
secrets manager for CI/CD pipelines.

---

## 4. Direct DB read (stolen backup / server compromise)

**Attack.** An attacker gets a copy of the SQLite database — via a
misconfigured backup, a stolen drive, a compromised admin account, an
`rsync` to the wrong host, etc. They want to learn the user passwords.

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
guess; against a unique salt per user, a dictionary attack has to pay
that cost per (user, guess) pair.

**What does NOT defend.** This drill does NOT protect the session token,
the secret variables stored in `release_variables.value`, or the audit log
itself. A DB read of those is still useful to an attacker. P1-2
(secret encryption at rest) and the audit log retention policy in P2-5
are the upgrade paths.

**Detection.** If you suspect a backup or the live DB leaked, rotate
every user's password immediately:

```bash
# For each user:
sudo -u durpdeploy /usr/local/bin/durpdeploy admin create \
  --email user@example.com --password '<new-strong-password>'
# (this errors with "user already exists" if the email is taken — that's
# expected. Use the `user reset` flow once it ships, or for now:
sudo -u durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db \
  "DELETE FROM users WHERE email='user@example.com';"
# (the ON DELETE CASCADE on sessions kills their active sessions too)
sudo -u durpdeploy /usr/local/bin/durpdeploy admin create \
  --email user@example.com --password '<new-strong-password>'
```

Then check the audit log for the new user_id and any actions by the
old id in the window between compromise and rotation — those are
suspect.

---

## 5. OIDC coexistence and recovery

**Attack.** The provider is unavailable, removes a user's mapped group, or a
user logs out locally after using SSO.

**Expected.** Provider failure returns a generic OIDC error while password
login, existing browser sessions, health checks, and bearer API authentication
remain usable. A removed group changes the local role only at that user's next
OIDC login. Local logout clears the DurpDeploy session but does not log out of
the provider. Provider tokens, authorization codes, and raw claims are not
persisted. OIDC does not authenticate API tokens, and an OIDC-created
empty-password account has no self-service password reset.

**What defends.** When unset, or set to `true`, OIDC requires the literal JSON
boolean `email_verified: true`. Explicit lowercase
`DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false` accepts a present literal JSON
boolean `email_verified: true` or `email_verified: false` after normal ID token
signature, issuer, audience, and nonce verification. Missing, null, string, and
numeric claims remain rejected. This weakens identity assurance and is
appropriate only where Authentik independently
establishes address ownership. OIDC links the first exact email match and
otherwise JIT-creates an empty-password account. Group mapping
uses admin, deployer, viewer precedence. A changed role invalidates the user's
browser sessions. Reauthentication is handled by the provider and bound to the
current local session and OIDC identity. There is no SCIM or provider
back-channel deprovisioning, so deprovisioning is not instant.

**Configuration contract.** OIDC requires the HTTPS `DURPDEPLOY_URL`, the
redirect URI `DURPDEPLOY_URL + /login/oidc/callback`, and the OIDC variables documented in the deployment runbook. Never put a live issuer,
client value, secret, token, or claim in this document.

---

## What this drill does not cover

These attacks are out of scope for P0:

- **Compromised teammate's laptop** — if the attacker has a teammate's
  actual cookie + CSRF token, they are that teammate. No defense
  available client-side.
- **Server root compromise** — an attacker with root on the box can
  replace the binary, read the DB, sniff process memory. OS-level
  problem, not DurpDeploy's.
- **Network-level DDoS** — handled upstream (Caddy, firewall).
- **Supply chain** — `go mod verify` and pinned versions only.

P1 closes several more gaps (per-project authorization, secret
encryption at rest, runner sandbox) — see `.omo/plans/team-hardening.md`.
