# DurpDeploy agent operator runbook

This runbook configures a DurpDeploy server and one outbound-only remote agent.
It covers the server listener, admin enrollment, agent installation, routing,
maintenance, and recovery.

## Two storage boundaries

The **server owns the DurpDeploy database**. SQLite, its WAL and SHM files,
projects, releases, deployments, variables, audit records, agent records,
pool membership, tags, policies, and enrollment-token hashes stay on the
server. The agent never receives the SQLite database, `DURPDEPLOY_SECRET_KEY`,
or the server's control-plane state directory.

The **agent has no database**. Its private state directory contains only the
agent identity certificate and key, pinned server fingerprint state, and a
temporary hash-only current-claim marker. Keep that directory private and
back it up only if preserving the enrolled identity is intentional.

Agents initiate all connections. The server never connects inbound to an agent,
and remote dispatch does not use SSH.

## Transport and ports

There are two separate HTTPS paths:

* Caddy serves the browser and JSON API HTTPS origin on port 443. Caddy obtains
  the public certificate from Let's Encrypt and reverse proxies to the normal
  application listener on port 8080.
* The dedicated agent listener uses port 10943, or another operator-selected
  address, directly on the DurpDeploy process. It uses TLS 1.3, self-signed
  Ed25519 identities, and direct SHA-256 certificate fingerprint pinning. It is
  **not routed through Caddy** and must not be terminated by Caddy.

Only the agent network should reach the dedicated listener. Allow inbound TCP
10943 from the agent hosts or agent network, allow the server's normal 80 and
443 as required by Caddy, and keep application port 8080 private. Agents need
outbound TCP access to the server's 10943 address. They do not need an inbound
agent port.

## Configure the server listener

For a local foreground run, use the Makefile targets below. `make dev` remains
the ordinary browser/API development path and does not enable the agent
listener. `dev-agent` requires an explicit direct listener address, public HTTPS
origin, and identity directory because the hostname becomes the self-signed
certificate SAN:

```bash
AGENT_LISTEN_ADDR=:10943 AGENT_PUBLIC_URL=https://localhost \
  AGENT_IDENTITY_DIR=.agent-identity make dev-agent
```

The public URL is the direct server mTLS endpoint, not the Caddy/Let's Encrypt
browser or API endpoint. To run an agent from a separate environment file:

```bash
chmod 0600 /path/to/agent.env
AGENT_ENV_FILE=/path/to/agent.env make agent-run
```

`agent-run` builds `durpdeploy-agent`, sources only `AGENT_ENV_FILE`, and does
not set `DURPDEPLOY_DB`, `DURPDEPLOY_SECRET_KEY`, or Docker/Caddy variables. The
file must define the seven `DURPDEPLOY_AGENT_*` values listed below. The agent
is outbound-only and has no database; its state directory is not server
storage. Run `make agent-help` for the compact prerequisite summary.

The listener is disabled unless all three variables are set together:

```dotenv
DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>
DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity
```

`DURPDEPLOY_AGENT_PUBLIC_URL` must be an HTTPS origin with no path, query, or
fragment. Its hostname is placed in the self-signed certificate SAN. The agent
uses the same host with port 10943 for `DURPDEPLOY_AGENT_SERVER_URL`.

On a systemd server, put those variables in the root-owned server environment
file referenced by the unit, for example:

```bash
sudo install -d -o durpdeploy -g durpdeploy -m 0750 /var/lib/durpdeploy/agent-identity
sudo install -m 0600 /tmp/durpdeploy.env /etc/durpdeploy/durpdeploy.env
sudo systemctl daemon-reload
sudo systemctl restart durpdeploy
sudo systemctl status durpdeploy --no-pager
```

The server unit already permits writes below `/var/lib/durpdeploy`. Keep the
identity directory owned by `durpdeploy`, mode `0700` or stricter, and do not
copy its private key to an agent. Check the listener and server logs before
enrolling:

```bash
sudo journalctl -u durpdeploy -n 100 --no-pager
sudo ss -ltn '( sport = :10943 )'
```

For Compose, add the same three variables to `compose.app.env`, mount a private
server identity directory at the configured path, and publish only the direct
listener port to the required agent network. The stock Compose files publish
only Caddy's 80 and 443, so port 10943 is intentionally not exposed by
default. Do not put the server identity directory in the agent service.

Retrieve the server fingerprint from the server identity certificate through a
trusted administrative path. Do not retrieve it from the agent connection,
disable verification, or accept it on first use:

```bash
sudo openssl x509 -in /var/lib/durpdeploy/agent-identity/identity.crt \
  -outform DER | sha256sum
```

The result is 64 lowercase hexadecimal characters. Compare it out of band,
then place that exact value in the agent configuration. The server UI displays
agent fingerprints after enrollment, but the initial server fingerprint must
be obtained from the server identity file or another trusted server-side
administrative channel.

## Admin UI enrollment workflow

Use an administrator browser session at the Caddy URL. The agent pages are
admin-only.

1. Open **Admin, Agent pools**, choose **New pool**, enter a name, and leave the
   pool enabled. A disabled pool cannot be selected by an environment policy.
2. Open **Admin, Agents**, choose **New agent**, and enter the stable Agent ID,
   display name, and optional agent version. The ID must be unique.
3. Open the new agent's details. Add its membership to the intended pool from
   the pool details page. Add any tags from the agent details page, using exact
   `key=value` pairs such as `region=<region>,role=<role>`. Keys are lowercase
   letters, digits, underscore, dot, or hyphen. Values are case-sensitive.
4. Open the agent's **Enroll** page and choose **Generate enrollment token** only
   when the agent is ready. The token is displayed once, expires after 15
   minutes, and cannot be retrieved later. Copy it directly into a protected
   environment file. Never put it in source control, tickets, chat, shell
   history, or logs.
5. Edit the target environment. Set execution mode to **remote**, select the
   enabled pool, and enter the required agent tags. An empty selector means any
   member of that pool. The server canonicalizes tag order and rejects duplicate
   keys or malformed pairs.
6. Save the environment, then verify the agent is **active** and has a recent
   heartbeat before creating a deployment.

The server owns pool membership and tags. An agent cannot select its own pool,
change its tags, or bypass an environment policy. A remote environment with no
matching agent remains waiting. It never falls back to local execution.

## Agent variables

The agent reads exactly these environment variables:

```dotenv
DURPDEPLOY_AGENT_SERVER_URL=https://<agent-control-host>:10943
DURPDEPLOY_AGENT_SERVER_FINGERPRINT=<64-lowercase-hex-sha256-fingerprint>
DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent
DURPDEPLOY_AGENT_ENROLLMENT_TOKEN=<one-time-enrollment-token>
DURPDEPLOY_AGENT_ID=<stable-agent-id>
DURPDEPLOY_AGENT_NAME=<agent-name>
DURPDEPLOY_AGENT_VERSION=<agent-version>
```

The protocol is fixed by the binary as `agent/1`; there is no protocol variable.
`DURPDEPLOY_AGENT_SERVER_URL` must be an HTTPS origin and may include port
10943, but no path, query, or fragment. The fingerprint must be the exact
lowercase SHA-256 value. The ID and name must match the admin record, and the
version is sent in heartbeats and enrollment metadata.

### First enrollment versus restart

On first enrollment, the agent creates `identity.crt` and `identity.key` in
the state directory, writes `server-pins.json` mode `0600`, and posts the
enrollment request with the one-time token. The server stores only the token
hash and activates the pending agent after matching the presented certificate.

The current agent binary requires `DURPDEPLOY_AGENT_ENROLLMENT_TOKEN` at every
startup and calls the enrollment endpoint at every startup. The token is
single-use, so a consumed token cannot be reused for an ordinary restart. Do
not document or assume a reusable token or an enrollment skip flag. If a
restart is needed after the token was consumed, use the recovery procedure
below to revoke and re-enroll the agent, then start it with the new token.

After successful enrollment, normal work is outbound polling, heartbeats, log
uploads, and result or cancellation acknowledgements. The agent stores no
server secret or deployment payload at rest. A current claim marker contains
only the deployment ID and a SHA-256 hash of the claim token and is removed
after the claim completes.

## Direct binary installation

Build the agent binary from the repository. This builds only `cmd/agent` and
does not create or open a database:

```bash
make build-agent
sudo install -o root -g root -m 0755 ./durpdeploy-agent /usr/local/bin/durpdeploy-agent
```

Create the dedicated account and private state directory:

```bash
sudo useradd --system --home-dir /var/lib/durpdeploy-agent \
  --shell /usr/sbin/nologin durpdeploy-agent
sudo install -d -o durpdeploy-agent -g durpdeploy-agent -m 0700 \
  /var/lib/durpdeploy-agent
sudo install -m 0600 /tmp/durpdeploy-agent.env /etc/durpdeploy-agent.env
```

For a first enrollment, a foreground run can use a protected environment file
owned by the agent user. Keep the token out of the command line:

```bash
sudo install -o durpdeploy-agent -g durpdeploy-agent -m 0600 \
  /tmp/durpdeploy-agent.env /var/lib/durpdeploy-agent/agent.env
sudo -u durpdeploy-agent sh -c \
  'set -a; . /var/lib/durpdeploy-agent/agent.env; exec /usr/local/bin/durpdeploy-agent'
```

The systemd procedure below is preferred for production. Its
`EnvironmentFile=/etc/durpdeploy-agent.env` is read by systemd, while the
service process receives the values without putting them in shell history.

## Docker or Podman Compose

The optional `agent` profile is a co-located demonstration and validation
path. It is not a server sidecar and is not a production placement
recommendation. Production agents should run remotely on the host where the
deployment commands belong.

Create `compose.agent.env` with the same variables, use mode `0600`, and do not
include a credential-shaped literal:

```dotenv
DURPDEPLOY_AGENT_SERVER_URL=https://<agent-control-host>:10943
DURPDEPLOY_AGENT_SERVER_FINGERPRINT=<64-lowercase-hex-sha256-fingerprint>
DURPDEPLOY_AGENT_ENROLLMENT_TOKEN=<one-time-enrollment-token>
DURPDEPLOY_AGENT_ID=<stable-agent-id>
DURPDEPLOY_AGENT_NAME=<agent-name>
DURPDEPLOY_AGENT_VERSION=<agent-version>
```

Docker Compose:

```bash
chmod 0600 compose.agent.env
docker compose --profile agent up -d --build agent
docker compose --profile agent ps agent
docker compose --profile agent logs -f agent
```

Podman Compose:

```bash
chmod 0600 compose.agent.env
podman compose --profile agent up -d --build agent
podman compose --profile agent ps agent
podman compose --profile agent logs -f agent
```

The profile mounts one volume at `/var/lib/durpdeploy-agent`, has no `/data`
mount, server secret, Docker socket, host network, or inbound listener. That
volume is agent identity state, not SQLite and not a server backup.

## systemd installation and operations

Install the unit shipped in the repository after installing the binary and
environment file:

```bash
sudo install -m 0644 systemd/durpdeploy-agent.service \
  /etc/systemd/system/durpdeploy-agent.service
sudo systemctl daemon-reload
sudo systemctl enable --now durpdeploy-agent
sudo systemctl status durpdeploy-agent --no-pager
```

The unit runs as `durpdeploy-agent`, sets the state directory, uses
`/etc/durpdeploy-agent.env`, applies a private `UMask=0077`, and permits writes
only to the agent state directory. Keep both `/etc/durpdeploy-agent.env` and
the state directory inaccessible to other users:

```bash
sudo chown root:root /etc/durpdeploy-agent.env
sudo chmod 0600 /etc/durpdeploy-agent.env
sudo chown -R durpdeploy-agent:durpdeploy-agent /var/lib/durpdeploy-agent
sudo chmod 0700 /var/lib/durpdeploy-agent
```

Operations:

```bash
sudo systemctl start durpdeploy-agent
sudo systemctl status durpdeploy-agent --no-pager
sudo journalctl -u durpdeploy-agent -f
sudo systemctl restart durpdeploy-agent
sudo systemctl stop durpdeploy-agent
```

Because the current binary enrolls at every startup, use a freshly issued
enrollment token before a restart that follows a completed enrollment. See
revocation and re-enrollment below.

## Verify before deploying

```bash
sudo systemctl is-active durpdeploy-agent
sudo journalctl -u durpdeploy-agent -n 50 --no-pager
```

In the UI, confirm the agent is active, its fingerprint is the expected one,
its pool membership and tags are correct, and its heartbeat is current. Start a
small non-production deployment first. Watch the deployment routing state. A
remote deployment should show its pool and agent, not local execution.

## Troubleshooting

### 404 or a stale binary

The dedicated listener has its own routes under `/agent/v1`; the browser and
API routes are not valid substitutes. A 404 often means the agent URL points
at Caddy or port 443 instead of the direct listener, or the server binary was
built before the listener code was installed. Confirm the server has all three
listener variables, check `ss -ltn` for 10943, verify the installed binary and
restart the server. Rebuild the agent with `make build-agent` and reinstall it
when its behavior does not match the checkout.

### Wrong fingerprint

The agent rejects an unpinned or changed certificate. Recalculate the value
from the server's `identity.crt`, compare it through a trusted channel, and
correct `DURPDEPLOY_AGENT_SERVER_FINGERPRINT`. Do not use trust-all TLS or
replace the value with a fingerprint copied from an untrusted connection.

### Expired or reused enrollment token

Tokens expire after 15 minutes and are consumed once. Generate a new token from
the agent's **Enroll** page and update the protected env file. For an already
active agent, revoke and re-enroll it first.

### No match

Check that the pool is enabled, the agent is a member, tag keys and values match
exactly, and the environment policy is remote. A missing tag is not a wildcard.
The deployment remains waiting until a matching active agent polls.

### Revoked agent

Revocation blocks the old identity. Stop the old process, confirm the agent is
revoked in **Admin, Agents**, issue a new enrollment token with **Re-enroll**,
and enroll the replacement using a controlled state directory. Do not restore
the old private key onto an unrelated host.

### Lost agent

Stop the service and inspect the agent and server journals. Started work that
misses heartbeats becomes lost and is not automatically replayed. Review the
original deployment, fix the host, and create an explicit new deployment.

### Cancel unconfirmed

The server keeps the deployment running until the agent acknowledges a cancel.
If the 30-second acknowledgement deadline expires, routing becomes
`cancel_unconfirmed`. Treat the outcome as uncertain, inspect the host and
logs, and create a new deployment only after confirming what happened. Do not
retry the original deployment or assume cancellation succeeded.

## Revocation, re-enrollment, and fingerprint rotation

For a lost or compromised host:

1. Stop the agent service and isolate the host.
2. In **Admin, Agents**, revoke the agent and remove it from its pool.
3. Review redacted agent events, deployment history, and the audit log.
4. Prepare a replacement host with a new private state directory.
5. Use **Re-enroll** on the revoked agent, generate a fresh one-time token, and
   enroll the replacement. Update only the replacement environment file.

For planned server certificate rotation, keep the current server identity and
fingerprint working first. The server can advertise the old and pending pins
through an authenticated heartbeat. Agents stage `[old, pending]`, connect to
the pending certificate, and remove the old pin only after that verified
connection succeeds. Do not delete the old server identity or pin until all
agents have promoted the pending pin. If rotation is interrupted, restore the
old listener identity and complete the staged process rather than replacing
the state file by guesswork.

## Upgrades, rollback, and backup scope

Upgrade the server and agents from the same repository revision when possible.
For an agent, build a new `durpdeploy-agent`, install it over the binary, and
restart through the normal controlled enrollment procedure. Preserve the state
directory across a compatible upgrade. If rollback is required, stop the
service, install the previous binary, and restore the matching known-good
configuration. Do not delete pins or identity files during an ordinary binary
rollback.

Back up the server SQLite database and the matching server encryption key as a
pair. Litestream or SQLite backup procedures cover server database state. Back
up an agent state directory only to preserve that exact enrolled identity. It
is not a database, and restoring it to another host transfers the identity.

## Safety rules

Do not route port 10943 through Caddy. Do not use a central CA, trust-on-first-
use, SSH, a trust-all TLS setting, inbound agent ports, or a shared agent state
directory. Do not give an agent the server database, server secret key, Docker
socket, or control-plane state directory.

For protocol details and fixed timing limits, see
[`agent-protocol.md`](agent-protocol.md). For server deployment and backup
operations, see [`deploy.md`](deploy.md) and
[`backup-restore.md`](backup-restore.md).
