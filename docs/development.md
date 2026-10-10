# Development and testing

## Tooling

Use Go 1.26+, Node.js, and the templ CLI. Install sqlc only when changing queries.
Run `make install-hooks` once per clone. Read [AGENTS.md](../AGENTS.md) for generated
code, migrations, authorization, and required verification rules.

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

Configure the ephemeral proxy without installing Caddy on the host:

```bash
DEV_HTTPS_PROXY_CONTAINER=my-dev-proxy \
DEV_HTTPS_PROXY_PORT=9443 \
DEV_HTTPS_PROXY_BACKEND=host.docker.internal:8080 make dev
```

The container engine must support `host-gateway`; startup fails clearly if the
host backend cannot be reached through that mapping. Docker is preferred when
available, with a healthy Podman engine used otherwise.

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
