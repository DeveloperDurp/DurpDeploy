# DurpDeploy agent operator runbook

This runbook configures a DurpDeploy server and one outbound-only remote agent.
It covers the server listener, admin pairing, agent installation, routing,
maintenance, and recovery.

## Two storage boundaries

The **server owns the DurpDeploy database**. SQLite, its WAL and SHM files,
projects, releases, deployments, variables, audit records, and agent records
stay on the server. The agent never receives the SQLite database,
`DURPDEPLOY_SECRET_KEY`,
or the server's control-plane state directory.

The **agent has no database**. Its private state directory contains only the
agent identity certificate and key, paired server identity state, and a
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

Only the agent network must have access to the dedicated listener. Allow inbound TCP
10943 from the agent hosts or agent network. Allow ports 80 and 443 for Caddy.
Keep application port 8080 private. Agents need
outbound TCP access to the server's 10943 address. They do not need an inbound
agent port.

## Configure the server listener

Configure the direct listener through the server environment file. `make dev`
remains the ordinary browser/API development path and does not enable the agent
listener. For a local foreground run, set the server listener variables before
starting `go run ./cmd/server`. The public origin hostname becomes the
self-signed certificate SAN:

```bash
DURPDEPLOY_AGENT_LISTEN_ADDR=:10943 \
  DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost \
  DURPDEPLOY_AGENT_IDENTITY_DIR=.agent-identity \
  go run ./cmd/server
```

The public URL is the direct server mTLS endpoint, not the Caddy/Let's Encrypt
browser or API endpoint. The agent is outbound-only and has no database. Its
state directory is not server storage.

The listener is disabled unless all three variables are set together:

```dotenv
DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>
DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity
```

`DURPDEPLOY_AGENT_PUBLIC_URL` must be an HTTPS origin with no path, query, or
fragment. Its hostname is placed in the self-signed certificate SAN. Pairing
persists the direct listener URL for the agent. Operators do not enter it on
the agent host.

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
pairing:

```bash
sudo journalctl -u durpdeploy -n 100 --no-pager
sudo ss -ltn '( sport = :10943 )'
```

For Compose, add the same three variables to `compose.app.env`. Mount a private
server identity directory at the configured path. Publish the direct listener
port only to the necessary agent network. The stock Compose files publish
only Caddy's 80 and 443, so port 10943 is intentionally not exposed by
default. Do not put the server identity directory in the agent service.

## Admin UI pairing workflow

Use an administrator browser session at the Caddy URL. The agent pages are
admin-only.

1. Open **Admin, Agents**, choose **New agent**, and enter the stable Agent ID,
   display name, and optional agent version. The ID must be unique.
2. Start the local agent listener, then open the agent's pairing page only when
the operator can complete the ceremony. Enter the short-lived, one-time pairing code,
   compare the displayed fingerprint through a trusted channel, and confirm.
   You can use the code one time. You cannot retrieve it later. Never put it in source
   control, tickets, chat, shell history, or logs.
3. Assign an environment to the paired active agent from its details page, then
   verify its heartbeat before creating a deployment.

Each remote deployment is assigned to exactly one paired agent. It never falls back to local execution.

## Agent start and pairing

Start an unpaired agent with only local inputs:

```dotenv
DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent
DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
DURPDEPLOY_AGENT_VERSION=<agent-version>
```

`DURPDEPLOY_AGENT_STATE_DIR` defaults to the platform configuration directory,
but production services set it explicitly. `DURPDEPLOY_AGENT_LISTEN_ADDR` is
needed only while the local pairing listener is open. `DURPDEPLOY_AGENT_VERSION`
is sent in heartbeats after pairing. The protocol is fixed by the binary as
`agent/1`. There is no protocol variable.

The first run prints a short-lived pairing code and agent fingerprint. Enter
those values in the authenticated admin pairing flow, compare the displayed
fingerprint, and confirm before the code expires. Do not put the pairing code,
fingerprint, endpoint, or private key in documentation, tickets, shell history,
or logs. Pairing persists the agent identity, pull URL, server pins, and agent
ID in the private state directory.

After pairing, restart with `DURPDEPLOY_AGENT_STATE_DIR` and
`DURPDEPLOY_AGENT_VERSION`. Do not supply a server URL, certificate,
fingerprint, token, or agent ID manually. Normal work is outbound polling,
heartbeats, log uploads, and result or cancellation acknowledgements. The agent
stores no server secret or deployment payload at rest. A current claim marker
contains only the deployment ID and a SHA-256 hash of the claim token and is
removed after the claim completes.

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

For first pairing, a foreground run can use a protected environment file owned
by the agent user:

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
recommendation. Production agents must run remotely on the host where the
deployment commands belong.

Create `compose.agent.env` with the same local variables, use mode `0600`, and
do not include a credential-shaped literal:

```dotenv
DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
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

After a successful pairing, keep the state directory across an ordinary restart.
The agent resumes its pinned outbound connection without manual server settings.

## Verify before deploying

```bash
sudo systemctl is-active durpdeploy-agent
sudo journalctl -u durpdeploy-agent -n 50 --no-pager
```

In the UI, confirm the agent is active and its heartbeat is current. Start a
small non-production deployment first. A remote deployment must show its
assigned agent, not local execution.

## Troubleshooting

### 404 or a stale binary

The dedicated listener has its own routes in `/agent/v1`. The browser and
API routes are not valid substitutes. A 404 often means the agent URL points
at Caddy or port 443 instead of the direct listener. A stale server binary can
also cause a 404. Confirm that the server has all three listener variables.
Use `ss -ltn` to check port 10943. Verify the installed binary. Restart the
server. Rebuild the agent with `make build-agent` and install it again
when its behavior does not match the checkout.

### Wrong fingerprint

The agent rejects an unpinned or changed certificate. For an unexpected change,
stop the agent. Investigate the server identity through a trusted channel.
Then do the controlled re-pair or planned rotation procedure below. Do not
use trust-all TLS or accept a fingerprint copied from an untrusted connection.

### Expired or reused pairing code

Pairing codes expire after 15 minutes and are consumed once. Restart the
unpaired local listener to obtain a fresh code. For an already active agent,
revoke and re-pair it first.

### No match

Check that the environment is assigned to this paired active agent. The
deployment remains waiting until that agent polls.

### Revoked agent

Revocation blocks the old identity. Stop the old process, confirm the agent is
revoked in **Admin, Agents**, use **Re-pair**, and pair the replacement with
a controlled state directory. Do not restore the old private key onto an
unrelated host.

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

## Revocation, re-pairing, and fingerprint rotation

For a lost or compromised host:

1. Stop the agent service and isolate the host.
2. In **Admin, Agents**, revoke the agent.
3. Review redacted agent events, deployment history, and the audit log.
4. Prepare a replacement host with a new private state directory.
5. Use **Re-pair** on the revoked agent and pair the replacement. Update only
   the replacement local listener configuration.

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
restart through the normal controlled pairing procedure. Preserve the state
directory across a compatible upgrade. If rollback is necessary, stop the
service, install the previous binary, and restore the matching known-good
configuration. Do not delete pins or identity files during an ordinary binary
rollback.

Back up the server SQLite database and the matching server encryption key as a
pair. Litestream or SQLite backup procedures cover server database state. Back
up an agent state directory only to preserve that exact paired identity. It
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
