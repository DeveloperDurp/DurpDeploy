# DurpDeploy — Attack drill

Five-minute hands-on walkthrough of the three most likely attacks against
the deployed instance, with the expected failure mode for each. Run this
after every deploy to a new VM, and again quarterly, to confirm the
defenses are still in place.

The drills assume you have a running instance reachable at
`https://durpdeploy.example.com` and you have shell access to the server's
SQLite database (via `sudo -u durpdeploy sqlite3 ...`). The server-side
queries in drill 3 require shell access to the box; drills 1 and 2 only
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

**No edge rate limit (yet).** The Caddyfile deliberately omits a
`rate_limit` block on `/login` because the stock `caddy:2-alpine` image
lacks the `caddy-ratelimit` module. The CPU-cost mitigation is the only
line of defense until a custom Caddy build is in place. If you start
seeing 1000+ failed logins/minute, that's a real attack — block the
source IP at the firewall.

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

## 3. Direct DB read (stolen backup / server compromise)

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
