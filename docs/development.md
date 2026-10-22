# Development and testing

Run `make build` to embed the software version, source commit, and commit-count build number.
Read [software versioning](versioning.md) for tagged builds and development defaults.

## Tooling

Use Go 1.26+, Node.js, and the templ CLI. Install sqlc only when changing queries.
Run `make install-hooks` once per clone. Read [AGENTS.md](../AGENTS.md) for generated
code, migrations, authorization, and required verification rules.

Run `make lint` to check all Go packages and tests with `golangci-lint`.
The target generates required templates and Swagger assets before analysis.
It downloads the version pinned in `Makefile` into ignored `bin/lint/`.
The installer verifies the release checksum and retains its license.
Linux and macOS support `amd64` and `arm64` binaries.
CI runs the same target before the dependent Go and E2E jobs.

## Build and local HTTPS

```bash
# Generate templ files
make templ-generate

# Build Tailwind CSS
make tailwind-build

# Full build
make build

# Run with hot-reload behind an ephemeral Caddy HTTPS proxy (requires Docker or Podman)
make dev
```

The asset build uses Tailwind CSS 4 and DaisyUI 5. Sources and both custom
themes live in `static/css/input.css`; there is no JavaScript Tailwind config.
`make tailwind-build` runs `@tailwindcss/cli` and bundles the MIT license
notices in `static/css/tailwind.licenses.txt`. The UI requires Safari 16.4+,
Chrome 111+, or Firefox 128+.

Tailwind and its CLI are pinned to 4.3.0 because CLI 4.3.3 pins an older
Parcel watcher that pulls in vulnerable `braces`. Check `npm audit` and the
resolved watcher dependencies before upgrading the CLI.

`make dev`, `make dev-postgres`, and `make dev-mssql` keep the app on
`http://localhost:8080` and expose it through `https://localhost:8443`. The
proxy creates a temporary local CA and one certificate for `localhost`, the
loopback addresses, and every host IP reported at startup. Accept the local
browser warning, import the printed CA certificate into your browser, or use
`curl -k`. The proxy and certificate files are removed when the dev command
exits.

The database targets replace their named development containers. Before
`make dev-postgres`, export `POSTGRES_PASSWORD` and a matching
`DURPDEPLOY_DB` PostgreSQL DSN. Before `make dev-mssql`, export
`MSSQL_SA_PASSWORD` and a matching SQL Server DSN. Supply credentials from
your secret manager; percent-encode passwords in DSNs. Both database ports
are bound to `127.0.0.1`. The targets do not print connection credentials.
For `make e2e-postgres` or `make e2e-mssql`, keep the same DSN exported.
The SQL Server E2E client uses `SQLCMDPASSWORD` (or `MSSQL_PASSWORD`), or the exported
`MSSQL_SA_PASSWORD` when invoked through Make. These checks retain their
examples for manual testing as described in [manual E2E](manual-e2e.md).

Configure the ephemeral proxy without installing Caddy on the host:

```bash
DEV_HTTPS_PROXY_CONTAINER=my-dev-proxy \
DEV_HTTPS_PROXY_PORT=9443 \
DEV_HTTPS_PROXY_BACKEND=host.docker.internal:8080 make dev
```

The container engine must support `host-gateway`; startup fails clearly if the
host backend cannot be reached through that mapping. Docker is preferred when
available, with a healthy Podman engine used otherwise.

## Isolated development stacks

For coding threads, use the [thread wrapper](#thread-owned-stacks-and-automatic-teardown).
Use `make dev-stack` when multiple worktrees or threads share one container engine.
Start the rootless Podman socket before the launcher.
Keep each state directory outside every source worktree.

```bash
systemctl --user start podman.socket
make dev-stack STATE="$HOME/.local/state/durpdeploy/thread-a"
make dev-stack ACTION=status STATE="$HOME/.local/state/durpdeploy/thread-a"
make dev-stack ACTION=logs STATE="$HOME/.local/state/durpdeploy/thread-a"
bash scripts/dev_stack.sh logs "$HOME/.local/state/durpdeploy/thread-a" caddy
```

Each `up` creates an app container, a Caddy container, a network, and four volumes.
The volumes contain the database, agent identity, and Caddy data and configuration.
The app image, runner namespace, and artifact workspace belong to that stack.
The app HTTP listener stays inside the stack network.
The container engine allocates loopback HTTPS and agent ports.
The launcher sets the public URLs before the app starts.
The app and proxy have CPU, memory, and process limits.

Read `connection.json` for the stack ID, URLs, resource IDs, and state directory.
The connection record and `status` output contain no credentials or encryption keys.
The private state directory contains the encryption key and TLS private keys.
Keep that directory private.
The launcher does not read the worktree `.env` file or inherit application settings.

The launcher uses rootless Podman by default.
Set `DEV_STACK_SOCKET` to select another local Unix socket.
For Docker, set `DEV_STACK_RUNTIME=docker` and select its local socket.
Set `DEV_STACK_SOURCE` to build another worktree.
Set `DEV_STACK_IMAGE` to use an existing app image with a private ownership layer.
The runtime socket grants the app the operator's container authority.
Use trusted deployment scripts.

Use the generated `.localhost` hostname with a separate browser profile for each stack.
Keep browser trust scoped to that profile.
Do not change the system trust store or `/etc/hosts`.
For CLI requests, use a separate cookie jar and the printed CA file:

```bash
state="$HOME/.local/state/durpdeploy/thread-a"
url=$(jq -r .url "$state/connection.json")
host=${url#https://}
curl --noproxy '*' --cacert "$state/tls/ca.pem" \
  --resolve "$host:127.0.0.1" "$url/healthz"
```

Create an administrator through the existing CLI inside the app container.
Read the container name from the connection record.
Load `/data/secret-key` into `DURPDEPLOY_SECRET_KEY` inside that container before the command.
Use the existing `admin create` or `tokens create` command.
Do not put credentials in the connection record.

```bash
app=$(jq -r '.resources[] | select(.kind == "container" and (.name | endswith("-app"))) | .name' "$state/connection.json")
read -r -s -p 'Administrator password: ' password
podman exec --user=10001 "$app" sh -c '
  DURPDEPLOY_SECRET_KEY=$(cat /data/secret-key)
  export DURPDEPLOY_SECRET_KEY
  exec /usr/local/bin/durpdeploy admin create --email "$1" --password "$2"
' bootstrap admin@local.test "$password"
unset password
```

For Docker, replace `podman` with the selected Docker command.

Use `logs` to inspect the app or proxy.
For a restart, restart the recorded app container with the selected engine.
An existing state directory cannot be used for another `up` operation.
After a failed launch, read `build.log`, `app.log`, `caddy.log`, and `cleanup.log` in that directory.
If cleanup is unconfirmed, retry `down` after the engine responds.

```bash
make dev-stack ACTION=down STATE="$HOME/.local/state/durpdeploy/thread-a"
```

`down` deletes that stack's containers, network, image tag, and volumes, including its database.
It verifies ownership labels and recorded resource IDs before deletion.
It keeps the local state directory for diagnostics.
It does not change the runtime socket or other stacks.
If you need the database, export it before `down`.
Certificates expire after seven days.
Create a new stack when its certificate expires.

Run `make dev-stack-test` for the two-worktree API and browser regression.
The test retains healthy examples and prints their URLs and state directories.
Read [manual E2E](manual-e2e.md#isolated-development-stacks) for repeat and teardown instructions.

### Thread-owned stacks and automatic teardown

Import the project scripts from `t3.json` into T3 Code.
Start a thread with its own worktree.
Run the start script or use these commands from that worktree:

```bash
make dev-stack-thread
make dev-stack-thread ACTION=status
make dev-stack-thread ACTION=logs
bash scripts/dev_stack_thread.sh logs caddy
```

The wrapper registers one stack in the worktree's ignored `.local/dev-stack.json` file.
The registration contains the canonical worktree path and the private state directory.
The stack state stays under `${XDG_STATE_HOME:-$HOME/.local/state}/durpdeploy/worktree-stacks/`.
Read `state_dir` from the registration to find `connection.json` and diagnostic logs.
The wrapper always builds the current worktree.
An existing registration prevents a second stack from replacing the first stack.

The T3 cleanup script has `runOnSettle: true`.
T3 runs it in the thread's worktree when the thread settles.
T3 skips this hook for threads without their own worktree.
Completing an agent turn does not invoke the hook.
Keep the thread unsettled while you need the stack for manual review.
Settling the thread deletes its registered containers, network, image tag, and volumes, including the database.

The wrapper checks the registration and connection record against the current worktree.
The launcher verifies resource ownership and the worktree label before deletion.
Repeated cleanup succeeds when no stack is registered.
If cleanup fails, the registration remains available for another attempt.
After the engine recovers, retry the cleanup command:

```bash
make dev-stack-thread ACTION=settle
```

Use the same command to remove a registered stack before settling the thread.
After cleanup succeeds, use `make dev-stack-thread` to start a new stack.
Do not copy registrations between worktrees.
Retained regression fixtures use the standalone launcher and remain outside this hook.
The hook does not install a timer or a host service.
For older worktrees without the wrapper, the hook succeeds only when no stack registration exists.

## Development encryption key

For local development, `make dev` reuses the encryption key from `.env` or
`DURPDEPLOY_SECRET_KEY`. When neither is configured, it creates a private,
gitignored `.local/dev-secret-key` once and reuses it across restarts. Keep this
file with the development database; losing it makes encrypted values unreadable.
An existing `/etc/durpdeploy/key` remains the server's first choice. This change
cannot recover values encrypted with a previously discarded temporary key.

## Populated demo

Run `make demo` for an isolated HTTPS server, a paired agent, and retained
manual-test fixtures. Follow the [demo lifecycle](manual-e2e.md#populated-demo)
for the engine socket, runtime verification, credentials, refresh, and teardown.
The launcher leaves the server, agent, and proxy running.

## End-to-end tests

Use a disposable test instance for `make e2e-test`. The suite retains fixtures
and creates or uses the fixed administrator account `admin@durp.info` with
password `password`, in addition to the configured primary test admin.

`make e2e-test` exercises the SQLite database of an already-running server; it
does not build or start one. Override the target with
`DURPDEPLOY_BASE_URL=https://localhost:8443 make e2e-test` (the local internal
CA is accepted automatically) or set `DURPDEPLOY_DB` when the running SQLite
server uses a non-default database path. The harness expects the configured
`E2E_ADMIN_EMAIL` to already be an `admin`; if it is missing, it will use
`durpdeploy admin create` through `DURPDEPLOY_E2E_CLI` (defaulting to
`./durpdeploy` when that binary is executable). If the CLI path is unavailable,
build DurpDeploy first and set `DURPDEPLOY_E2E_CLI` to an executable binary.
Bare SQLite paths use WAL, foreign keys, and a 5-second `busy_timeout` by
default. Explicit DSN query options are preserved. Use
`make e2e-test-isolated` for a clean-room local build-and-start workflow.
CI runs its own containerized E2E lifecycle.

The running-instance suite covers API and web CRUD, authorization, request
validation, interpreters, file handoff, secret masking, verification, rollback,
runbooks, charts, themes, and mobile layouts. Browser checks require Docker or
Podman. Fixtures remain available and test schedules stay disabled. See
[manual checks](manual-e2e.md) for retained examples and expected results.

The harness builds a pinned Terraform test image on the server's engine. Set
`DURPDEPLOY_CONTAINER_RUNTIME` and `DURPDEPLOY_CONTAINER_URL` to match a
non-default engine or socket. Pending artifact plans expire after 24 hours.
Re-run the retained release to produce a fresh plan.

Startup/recovery, other databases, external package repositories, OIDC, and
remote-agent protocols have dedicated fixture suites. One running-instance
suite does not replace those configuration-specific checks.

## Go and documentation checks

Generate missing embedded assets before any Go check:

```bash
make templ-generate swagger-ui-copy
go vet ./...
go test -v -count=1 -timeout=20m ./...
bash scripts/check-agent-documentation-contract.sh
bash scripts/check-mfa-documentation-contract.sh
node scripts/check-oidc-docs.mjs
bash scripts/check-mobile-browser-container-contract.sh
```

Use focused tests during implementation. Run applicable Go, API E2E, and web E2E
suites at final verification. CI runs lint, test, and build stages on pull requests.
SonarCloud is also PR-only. See [security scanning](security-scanning.md) and
[mobile browser CI](mobile-browser-ci.md) for their separate commands and gates.

## Source layout

`cmd/server` is the entry point. `internal/server` registers routes;
`internal/handler` handles requests; `internal/repository` wraps generated SQL;
`internal/runner` coordinates execution and live logs. SQL lives in `queries`
and `migrations`, templ sources in `views`, and embedded assets in `static`.
