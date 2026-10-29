# Startup configuration

Server startup and administrative commands use the central configuration loader.
Environment variables override YAML values, followed by built-in defaults.
The server captures settings before creating identities, migrating the database, or starting listeners.

Token telemetry uses the resolved concurrency, interval, and write timeout.
Both HTTP listeners use the resolved header, read, write, and idle timeouts.
Live log streams use the resolved per-event write timeout.
Artifact clients use the captured fetch, dial, TLS, and response-header timeouts.

Agent polls check the shared policy digest before heartbeat updates or dispatch.
Custom remote execution requires a matching policy from every target agent.
The server accepts nondefault telemetry, HTTP, server container, and transfer admission settings.
It also accepts all artifact download, ZIP safety, and transport settings.
It accepts the default step timeout, maximum step timeout, and maximum retries.
It accepts staging payload, entry, and archive budgets.
It accepts aggregate merge archive, entry-visit, and agent-output budgets.
It accepts all six log settings described below.

## Agent policy handshake

The mTLS poll endpoint uses the `X-DurpDeploy-Resource-Policy` header.
The digest covers execution, artifact, staging, transfer idle timeout, and container settings.
Server-only admission, merge, HTTP, log, and token telemetry settings do not affect this digest.
Both peers must use the same complete shared snapshot, including artifact transport timeouts.

An absent header permits only the default shared policy for legacy agents.
An advertised policy must match the server snapshot exactly.
Repeated, empty, malformed, unknown, and mismatched headers return `409` before heartbeat updates or dispatch.
Rejections leave waiting claims unchanged and do not echo untrusted header values.
Successful `200` and `204` polls return one matching acknowledgement header.
The existing mTLS certificate and pairing checks still authenticate every request.

The server stores accepted advertisements with the agent capability snapshot.
The record survives a server restart. A legacy default poll removes the explicit advertisement.
Claim preparation compares the request policy with the stored snapshot while holding the agent claim lock.
A stale poll receives no work when another poll changes that agent's policy advertisement.
Revocation, re-pairing, and registration deletion remove the stored advertisement.
Unchanged reports update the heartbeat without rewriting capability records.

Custom execution, container, staging, and package limits support remote dispatch after a matching agent poll.
Queue admission validates every selected fan-out target and rolls back the queue when any target has an incompatible policy.
Claim preparation rechecks the stored policy under the agent claim lock before preparing a token or encrypted payload.
Missing or incompatible records reject custom policies, including jobs without a package.
Configure the same shared values on the server and agents before restarting them.
Agent environment variables use the `DURPDEPLOY_AGENT_LIMITS_` prefix and the same field suffixes as server variables.

## Log settings

Set log budgets before server startup. Environment variables override YAML values.
Use the `limits.logs` YAML section for these settings.

| Environment variable | YAML field | Default | Purpose |
| --- | --- | --- | --- |
| `DURPDEPLOY_LIMITS_LOGS_PAGE_BYTES` | `page_bytes` | `1048576` | JSON page and browser retention bytes |
| `DURPDEPLOY_LIMITS_LOGS_LINE_BYTES` | `line_bytes` | `16384` | UTF-8 preview bytes per text field, including truncation markers |
| `DURPDEPLOY_LIMITS_LOGS_PREVIEW_BATCH` | `preview_batch` | `64` | Database rows per preview query |
| `DURPDEPLOY_LIMITS_LOGS_PENDING_BYTES` | `pending_bytes` | `1048576` | Server script output buffer bytes, including each incoming write |
| `DURPDEPLOY_LIMITS_LOGS_EXPORT_BATCH` | `export_batch` | `256` | Database rows per complete export or full stream batch |
| `DURPDEPLOY_LIMITS_LOGS_PAGE_ROWS` | `page_rows` | `500` | Rows per history page and browser retention window |

All six settings require positive integers. The line budget must be at least 128 bytes.
The page budget must cover 18 times the line budget plus 2048 bytes.
This allowance covers JSON escaping, three text fields, row metadata, and the response envelope.
The byte budget can reduce the number of returned rows below the row budget.
Page responses report the captured `limit`, `byte_limit`, and `line_byte_limit`.
Deployment and runbook pages use these values for history and live retention.

Previews repair invalid UTF-8 and shorten oversized text with a marker.
Complete streams and exports retain the persisted output.
Changing preview budgets does not change stored rows or impose a total log storage quota.
Export batches close database rows before writing to a slow client.
Exports retain their initial row watermark while a deployment adds new output.

The pending buffer retains server script output until redaction can safely process it.
Excess pending output cancels the attempt, discards ambiguous bytes, and records `pending log output exceeded safety limit`.
The pending buffer also bounds each incoming write before newline processing.
Small values can reject an incoming chunk even when it contains complete lines.
Agent protocol event and batch bounds remain protocol constants.
These server-only log settings permit remote execution with unchanged shared policies.

Higher budgets can increase query memory, browser memory, and pending output memory.
Smaller batches can increase database query counts.
Restart the server after changing log settings.
Inspect captured values with `GET /api/v1/admin/resource-config` as an admin.

## Staging and merge settings

Set `DURPDEPLOY_LIMITS_STAGING_PAYLOAD_BYTES` to change the staging snapshot content budget.
Set `DURPDEPLOY_LIMITS_STAGING_ENTRIES` to change the distinct snapshot entry budget.
Set `DURPDEPLOY_LIMITS_STAGING_ARCHIVE_BYTES` to change the staging TAR archive budget.
Use `limits.staging.payload_bytes`, `limits.staging.entries`, and `limits.staging.archive_bytes` in YAML.
The defaults are 536870912 payload bytes, 20000 entries, and 570425344 archive bytes.
Entries include files, explicit directories, and implicit parent directories.

Snapshot publication, downloads, merges, and approval capture and restore enforce the captured budgets.
Lower budgets can reject saved snapshots and approval contexts without changing their hashes or encrypted chunks.
Staging filesystem capacity equals payload bytes plus one host page for every permitted entry.
The runtime rounds filesystem capacity to whole pages.
Its inode budget is twice the permitted entry count plus two.
Helper memory adds the configured overhead to the calculated filesystem capacity.
Higher budgets can increase filesystem capacity, helper memory, temporary disk use, and validation work.

The filesystem allowance includes page rounding and directory overhead.
It is larger than the logical content budget checked during snapshot validation.
Local steps can share this allowance until snapshot capture or deployment completion.
Snapshot ingestion, canonical output, uploads, downloads, and merges enforce the archive budget.
Approval capture bounds its saved canonical context before encryption.
Approval restore checks the saved size before decrypting its chunks.
Approval decisions return `409` when the saved context exceeds the current archive budget.
Raise the budget or cancel that deployment before creating a new plan.
TAR headers, padding, and explicit or canonical parent directories consume archive bytes.
Container TAR output can include padding beyond its file contents.
Allow enough archive bytes for payload and framing. Startup requires at least 1024 bytes beyond the payload budget.

Startup migrations remove the historical database archive ceiling without changing stored hashes or encrypted chunks.
The database still requires positive sizes. The server enforces the captured archive budget.
Rollback restores the historical ceiling only if all saved snapshot sizes fit that ceiling.
An unsafe rollback fails atomically and preserves the migrated schema and stored data.
Remote queueing and claims require matching stored agent policies for custom staging budgets.
Restart the server after changing these settings.

Set `DURPDEPLOY_LIMITS_MERGE_ARCHIVE_BYTES` to change aggregate input archive bytes per remote step.
Set `DURPDEPLOY_LIMITS_MERGE_ENTRY_VISITS` to change aggregate entry visits per merge.
Set `DURPDEPLOY_LIMITS_MERGE_AGENT_OUTPUTS` to change the permitted agent outputs per merge.
Use `limits.merge.archive_bytes`, `limits.merge.entry_visits`, and `limits.merge.agent_outputs` in YAML.
The defaults are 2147483648 bytes, 200000 entry visits, and 64 outputs.
All three values must be positive.

Archive bytes count the base once and every agent output, including identical outputs.
Entry visits count the base once initially, then base entries plus output entries for each agent.
Archive and output-count checks precede input decryption and spooling.
Entry-visit checks precede each output's comparison and content copying.
Excess input fails the step with `staging merge budget exceeded` before downstream execution.
Normal deployment cleanup removes owned transfers, staging, and workload resources.

These server-only budgets permit remote work with unchanged shared policies.
The staging budgets still govern each archive and the final merged tree.
Higher merge budgets can increase database reads, temporary storage, and comparison work.
Restart the server after changing merge settings.
Create a new deployment to repeat a failed step with the new budgets.

Set `DURPDEPLOY_LIMITS_ARTIFACT_DOWNLOAD_BYTES` to change the server ZIP download budget.
Use `limits.artifact.download_bytes` in YAML.
The default is 314572800 bytes, or 300 MiB.
The client bounds declared lengths and streaming bodies before ZIP validation.
ZIP validation and server extraction use the same captured download budget.
ZIP validation and extraction also use captured extracted-byte, entry-count, staging-node, and metadata-read budgets.
Larger downloads can increase temporary disk use and approval-container temporary filesystem capacity.
Lower budgets can reject existing pinned packages before execution.
The server preserves stored pins and removes failed download spools.
Remote package dispatch requires matching download, ZIP safety, and transport budgets on the server and target agents.
The complete shared policy must match for work with or without a package.

Set `DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS` to change the default server step timeout.
Use `limits.execution.default_timeout_seconds` in YAML.
The default is 300 seconds. The value must be positive and cannot exceed the configured maximum step timeout.

Set `DURPDEPLOY_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS` to change the maximum step timeout.
Set `DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES` to change the maximum retries after the first attempt.
Use `limits.execution.max_timeout_seconds` and `limits.execution.max_retries` in YAML.
The defaults are 86400 seconds and 10 retries.
Zero maximum retries permits one attempt and rejects steps that request retries.
Forms, API validation, template application, release snapshots, and execution use the same captured limits.

If you lower the maximum below 300 seconds, lower the default timeout in the same configuration.
Existing snapshots remain unchanged. New deployments reject snapshots that exceed the current policy before execution.
Correct project steps before refreshing a release that exceeds the new maxima.
Refresh validates the replacement steps and preserves existing deployment snapshots.
Legacy releases without server container images still require a new release.
Runbook forms preserve retry counts above JavaScript's exact integer range as decimal strings.
The public API retains numeric retry fields.

A zero step timeout selects this captured default when execution starts.
An explicit positive step timeout keeps its saved value.
The server does not rewrite stored step or release snapshots.

Each retry receives a new timeout. Higher defaults can keep scripts and their resources active longer.
Lower defaults can end existing zero-timeout steps sooner.

Restart the server after changing the default.
Remote queueing and claims require matching stored agent policies for custom execution limits.

Set the ZIP safety budgets with these environment variables:

- `DURPDEPLOY_LIMITS_ARTIFACT_EXTRACTED_BYTES`
- `DURPDEPLOY_LIMITS_ARTIFACT_FILES`
- `DURPDEPLOY_LIMITS_ARTIFACT_STAGING_NODES`
- `DURPDEPLOY_LIMITS_ARTIFACT_METADATA_BYTES`

Use their corresponding `limits.artifact` keys in YAML.
The `files` budget counts ZIP entries, including explicit directories.
The `staging_nodes` budget counts distinct files and directories, including implicit parents.
Approval-context capture also uses the staging-node budget.
Saved approval contexts must pass the current node budget before restore or downstream execution.
Validation preserves their stored hashes and encrypted chunks.
The metadata budget counts parser reads rather than extracted content or exact process memory.
Higher extracted-byte and node budgets increase staging capacity and helper memory limits.
Artifact helper capacity equals extracted bytes plus one host page for every permitted staging node.
Docker artifact volumes also receive twice the permitted node count plus two inode units.
The default capacity now covers all 19,999 nodes rather than only 10,000 ZIP entries.
Podman artifact volumes retain their existing storage behavior and use the configured helper memory limit.
Lower budgets can reject saved packages without rewriting their stored pins.

## Sources and precedence

Use environment variables to configure startup settings without a file.
Use `DURPDEPLOY_CONFIG_FILE` to select an optional server YAML file.
Set an absolute file path in production.
Relative paths use the process working directory.

The loader resolves each setting in this order:

1. Explicit environment variable.
2. Value from the selected YAML file.
3. Built-in default.

An explicitly empty environment variable overrides its YAML value.
An unset variable permits YAML or default resolution.
Existing setting validators retain their empty-value rules.
An empty file selector disables file loading.

The loader rejects unreadable selected files, unknown keys, duplicate keys, null values, aliases, merge keys, and additional YAML documents.
Resource integers require YAML integer scalars or decimal environment values.
Fractional CPUs accept a numeric scalar or environment value.
The loader rejects non-finite CPU values and unsafe arithmetic.

Use [the complete YAML example](../durpdeploy.example.yaml) as the server settings reference.
Keep the OIDC section absent when OIDC is disabled.
Any supplied OIDC setting requires the complete OIDC configuration.

Protect configuration files that contain credentials with private permissions.
Supply secrets through environment variables when practical.
Do not print the full configuration or commit files that contain real credentials.
Parser errors omit configuration values because those values can contain credentials.

Restart the process after changing its configuration.
The configuration snapshot remains fixed for the process lifetime.
Configuration loading does not change the process environment.

## Inspect resource settings

Call `GET /api/v1/admin/resource-config` with a global administrator bearer token.
The response contains the captured `limits` fields as a JSON object without a wrapper.
It excludes credentials, connection strings, file paths, and other startup settings.
The response uses `Cache-Control: no-store`.
Environment changes after startup do not change this response.

Project-scoped tokens and roles below administrator receive `403`.
Requests without a valid bearer token receive `401`.
The endpoint has no write operation.
It reports server settings, without attesting to agent compatibility or external runtime enforcement.
Matching stored agent policies remain required for custom remote execution.

## Existing server settings

These mappings preserve existing environment variable names.
The loader records explicit presence for existing validators, including OIDC validation.

| YAML key | Environment variable | Historical default or behavior |
| --- | --- | --- |
| `addr` | `DURPDEPLOY_ADDR` | `:8080` |
| `db` | `DURPDEPLOY_DB` | SQLite DSN in the example |
| `url` | `DURPDEPLOY_URL` | Empty disables WebAuthn |
| `secret_key` | `DURPDEPLOY_SECRET_KEY` | Required key source; no generated default |
| `trusted_proxies` | `DURPDEPLOY_TRUSTED_PROXIES` | Empty trusts loopback prefixes |
| `audit_retention_days` | `DURPDEPLOY_AUDIT_RETENTION_DAYS` | `180`; applies to audit pruning |
| `extra_scrub_patterns` | `DURPDEPLOY_EXTRA_SCRUB_PATTERNS` | Empty; comma-separated regular expressions |
| `execution_boundary` | `DURPDEPLOY_EXECUTION_BOUNDARY` | No default |
| `oidc.issuer` | `DURPDEPLOY_OIDC_ISSUER` | Unset disables OIDC when other OIDC settings are absent |
| `oidc.client_id` | `DURPDEPLOY_OIDC_CLIENT_ID` | Required when OIDC is enabled |
| `oidc.client_secret` | `DURPDEPLOY_OIDC_CLIENT_SECRET` | Required when OIDC is enabled |
| `oidc.admin_group` | `DURPDEPLOY_OIDC_ADMIN_GROUP` | Required when OIDC is enabled |
| `oidc.maintainer_group` | `DURPDEPLOY_OIDC_MAINTAINER_GROUP` | Unset creates no maintainer mapping |
| `oidc.deployer_group` | `DURPDEPLOY_OIDC_DEPLOYER_GROUP` | Required when OIDC is enabled |
| `oidc.viewer_group` | `DURPDEPLOY_OIDC_VIEWER_GROUP` | Required when OIDC is enabled |
| `oidc.display_name` | `DURPDEPLOY_OIDC_DISPLAY_NAME` | `SSO` |
| `oidc.group_claim` | `DURPDEPLOY_OIDC_GROUP_CLAIM` | `groups` |
| `oidc.require_email_verified` | `DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED` | `true` |
| `smtp.host` | `DURPDEPLOY_SMTP_HOST` | Empty disables email |
| `smtp.port` | `DURPDEPLOY_SMTP_PORT` | Empty; no implicit SMTP port |
| `smtp.from` | `DURPDEPLOY_SMTP_FROM` | Empty |
| `smtp.user` | `DURPDEPLOY_SMTP_USER` | Empty disables SMTP authentication |
| `smtp.pass` | `DURPDEPLOY_SMTP_PASS` | Empty |
| `container.enabled` | `DURPDEPLOY_EMBEDDED_AGENT_ENABLED` | `true` |
| `container.runtime` | `DURPDEPLOY_CONTAINER_RUNTIME` | Auto-detect Docker or Podman |
| `container.url` | `DURPDEPLOY_CONTAINER_URL` | Selected runtime's existing socket fallback |
| `container.namespace` | `DURPDEPLOY_CONTAINER_NAMESPACE` | `durpdeploy` |
| `agent_listener.listen_addr` | `DURPDEPLOY_AGENT_LISTEN_ADDR` | `0.0.0.0:23187` |
| `agent_listener.public_url` | `DURPDEPLOY_AGENT_PUBLIC_URL` | `https://localhost:23187` |
| `agent_listener.identity_dir` | `DURPDEPLOY_AGENT_IDENTITY_DIR` | `.agent-identity` |
| `litestream.check_command` | `DURPDEPLOY_LITESTREAM_CHECK_COMMAND` | Empty disables the check |
| `litestream.check_interval` | `DURPDEPLOY_LITESTREAM_CHECK_INTERVAL` | `1h`; used when the check is enabled |

The key resolves from `DURPDEPLOY_SECRET_KEY`, then `secret_key` in YAML, then the legacy `/etc/durpdeploy/key` fallback.
An explicitly empty environment value suppresses the YAML key and permits the legacy file fallback.
An invalid nonempty key fails without trying another key source.

An explicitly configured key now takes precedence over the legacy key file.
Use the key that decrypts the existing database before restarting the server.
Use the same startup configuration for key rotation and server startup.

Administrative commands load the selected YAML file and use the same resolved database as the server.
Audit pruning retains the explicit `--days` override.
Help and version commands do not require valid runtime configuration.

The server rejects invalid origins, OIDC settings, listener syntax, database syntax, proxy prefixes, scrub expressions, and enabled runtime configuration.
An unavailable container engine does not prevent remote-agent operation.
Inactive OIDC, SMTP, container, and backup features retain their existing disable behavior.

## Resource settings

Construct each environment variable from its YAML path.
Replace each dot with an underscore.
Convert the path to uppercase.
Add the `DURPDEPLOY_` prefix.

For example, `limits.execution.max_retries` maps to `DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES`.
Every resource setting uses this rule without exceptions.

| YAML key | Default | Purpose |
| --- | ---: | --- |
| `limits.execution.max_retries` | `10` | Maximum retries after the first attempt |
| `limits.execution.max_timeout_seconds` | `86400` | Maximum configured step timeout |
| `limits.execution.default_timeout_seconds` | `300` | Timeout used when the step timeout is zero |
| `limits.artifact.download_bytes` | `314572800` | Maximum downloaded ZIP bytes |
| `limits.artifact.extracted_bytes` | `536870912` | Maximum extracted content bytes |
| `limits.artifact.files` | `10000` | Artifact file-count budget |
| `limits.artifact.staging_nodes` | `19999` | Artifact staging-node budget |
| `limits.artifact.metadata_bytes` | `16777216` | Maximum artifact metadata bytes |
| `limits.artifact.fetch_timeout_seconds` | `300` | Artifact fetch deadline |
| `limits.artifact.dial_timeout_seconds` | `30` | Connection deadline |
| `limits.artifact.tls_timeout_seconds` | `10` | TLS handshake deadline |
| `limits.artifact.header_timeout_seconds` | `30` | Response-header deadline |
| `limits.staging.payload_bytes` | `536870912` | Snapshot content budget |
| `limits.staging.entries` | `20000` | Snapshot entry budget |
| `limits.staging.archive_bytes` | `570425344` | Snapshot TAR byte budget |
| `limits.merge.archive_bytes` | `2147483648` | Base and agent-output archive bytes per step |
| `limits.merge.entry_visits` | `200000` | Aggregate merge entry visits |
| `limits.merge.agent_outputs` | `64` | Agent outputs per merge |
| `limits.transfer.global` | `4` | Concurrent staging and package transfers per server process |
| `limits.transfer.per_agent` | `1` | Concurrent staging and package transfers per authenticated agent per server process |
| `limits.transfer.idle_timeout_seconds` | `300` | Transfer inactivity deadline |
| `limits.container.pids` | `128` | Workload container PID limit |
| `limits.container.tmpfs_bytes` | `67108864` | Workload temporary filesystem bytes |
| `limits.container.helper_pids` | `32` | Staging helper PID limit |
| `limits.container.helper_cpus` | `1` | Staging helper CPU quota |
| `limits.container.helper_memory_overhead_bytes` | `134217728` | Helper memory above derived staging capacity |
| `limits.http.json_bytes` | `4194304` | JSON request-body limit |
| `limits.http.form_bytes` | `16777216` | General form-body limit |
| `limits.http.password_form_bytes` | `65536` | Password form-body limit |
| `limits.http.repository_form_bytes` | `65536` | Package-repository form-body limit |
| `limits.http.read_header_timeout_seconds` | `5` | HTTP request-header deadline |
| `limits.http.read_timeout_seconds` | `30` | HTTP request deadline |
| `limits.http.write_timeout_seconds` | `60` | Non-streaming response deadline |
| `limits.http.idle_timeout_seconds` | `60` | HTTP keep-alive idle deadline |
| `limits.http.stream_write_timeout_seconds` | `60` | Per-event streaming write deadline |
| `limits.logs.page_bytes` | `1048576` | Preview response byte budget |
| `limits.logs.line_bytes` | `16384` | Preview text-field byte budget |
| `limits.logs.preview_batch` | `64` | Rows read per preview batch |
| `limits.logs.pending_bytes` | `1048576` | Pending log-write buffer budget |
| `limits.logs.export_batch` | `256` | Rows read per complete export batch |
| `limits.logs.page_rows` | `500` | Maximum rows per log-history page |
| `limits.token_telemetry.concurrency` | `8` | Concurrent best-effort token telemetry writes |
| `limits.token_telemetry.interval_seconds` | `60` | Minimum token telemetry update interval |
| `limits.token_telemetry.write_timeout_seconds` | `5` | Token telemetry database-write deadline |

Container PID limits count processes and threads within each container.
The server applies `limits.container.pids` to workload containers, including verification and rollback attempts.
The server applies `limits.container.helper_pids` to staging helper containers.
Use `DURPDEPLOY_LIMITS_CONTAINER_PIDS` and `DURPDEPLOY_LIMITS_CONTAINER_HELPER_PIDS` for environment-only configuration.
Both settings require a positive integer and a restart.
The defaults remain 128 and 32.

```bash
export DURPDEPLOY_LIMITS_CONTAINER_PIDS=256
export DURPDEPLOY_LIMITS_CONTAINER_HELPER_PIDS=64
```

Higher values permit more concurrent processes and threads in each container.
Lower values can prevent interpreters or staging operations from starting.
The container runtime must provide an effective PID controller.
Host-mode agent scripts do not receive a container PID budget.

The server also applies `limits.container.tmpfs_bytes` to each workload's `/tmp` filesystem.
Approval generation and approved-context execution add the artifact download budget to that size.
The server applies `limits.container.helper_cpus` and `limits.container.helper_memory_overhead_bytes` to staging helpers.
Helper memory equals the staging filesystem capacity plus the configured overhead.
These settings require a restart. Workload CPU and memory remain controlled by the external runtime policy.

```bash
export DURPDEPLOY_LIMITS_CONTAINER_TMPFS_BYTES=134217728
export DURPDEPLOY_LIMITS_CONTAINER_HELPER_CPUS=0.5
export DURPDEPLOY_LIMITS_CONTAINER_HELPER_MEMORY_OVERHEAD_BYTES=134217728
```

Higher filesystem and helper memory values can increase host memory use.
Lower helper CPU values can slow transfer and cleanup operations.
The runtime rounds filesystem capacity to its allocation units.

Remote queueing and claims reject incompatible agent policies before issuing work.
The deployment log reports the policy incompatibility.
Configure matching container limits on the server and target agents before remote execution.
Already issued claims retain their original execution policy.

Telemetry limits apply to each API router in one server process.
Full admission capacity skips usage recording without delaying authentication.
Database errors or admission limits can delay or omit usage updates.
Authentication and token revocation are checked on every request.

Artifact timeouts apply to server-side repository tests, package downloads, and pinned-package verification.
The fetch timeout bounds each complete request, including response-body reads and redirects.
The dial timeout bounds each connection attempt to a validated destination address.
DNS resolution and multiple connection attempts remain bounded by the fetch timeout.
The TLS timeout bounds the TLS handshake.
The header timeout bounds the wait for response headers after sending a request.
The first applicable timeout ends the request.

Increase artifact timeouts to permit slower repositories.
Lower artifact timeouts to release stalled requests sooner.
Restart the server after changing a timeout.
Existing destination, proxy, TLS verification, credential, redirect, and cleanup rules remain active.
Artifact timeout settings do not change agent-client or staging-transfer inactivity deadlines.

Increase the interval to reduce write frequency and permit older usage timestamps.
Increase concurrency to permit more simultaneous database writes.
Increase the write timeout to hold admission slots longer during database delays.

HTTP timeouts apply to the browser/API listener and the mTLS agent listener.
The read timeout limits the whole request, including its headers and body.
The header timeout cannot exceed the read timeout.
The write timeout bounds ordinary responses.
Live log streams replace that timeout with a fresh deadline for each event.
Quiet log streams can remain open between events.

Increase HTTP timeouts to allow slower clients and longer ordinary responses.
Lower HTTP timeouts to release stalled connections sooner.
The stream write timeout bounds one event write and flush.
Increase that timeout to allow slower consumers and delay stalled-stream revocation.
Restart the server after changing a timeout.

HTTP body limits count encoded request bytes, including escaping and form overhead.
The JSON limit also applies to `/api/lint`, public API health requests, and empty-body actions.
The general form limit includes multipart bodies.
Login, reauthentication, and password changes use the password form limit.
Package-repository forms use their separate form limit.
Both specialized form limits must not exceed the general form limit.

Increase body limits to permit larger requests and increase per-request memory use.
Higher password form limits also increase memory held by queued authentication requests.
Password changes must fit the equivalent encoded browser form under the configured password limit.
Invalid JSON, authorization, CSRF, and viewer restrictions retain their existing behavior.
Restart the server after changing a body limit.

Byte settings use bytes.
Timeout settings with `_seconds` use whole seconds.
CPU settings use CPU units and permit fractions.
Zero retries retain one execution attempt.
A zero step timeout selects the configured default timeout.
Resource policy timeouts must remain positive.

All integer settings must fit the platform integer range with room for a sentinel value.
Timeout seconds must fit Go's duration range after conversion.
The default timeout cannot exceed the maximum timeout.
Per-agent transfer concurrency cannot exceed global transfer concurrency.

Artifact staging nodes must cover the artifact file count.
The staging archive must allow its payload plus at least 1024 framing bytes.
The password form budget cannot exceed the general form budget.
The HTTP header timeout cannot exceed the HTTP read timeout.

Log lines require at least 128 bytes.
Log pages require room for an escaped preview row and 2048 metadata bytes.
The loader budgets eighteen encoded bytes per preview text byte across three text fields.
Helper CPU quotas must be finite and at least `0.000000001` CPU units.
Derived temporary filesystem sizes, inode counts, and helper memory must fit the platform integer range.

Larger budgets can increase memory, disk, and container demand.
Transfer concurrency applies to each server process, without cluster-wide coordination.

Staging uploads and downloads share the configured global and per-agent transfer permits.
The defaults allow four requests globally and one request per authenticated agent.
Excess requests receive `429` with `Retry-After: 1` before expensive transfer work starts.
Success, failure, cancellation, and disconnection release the request's permit.
Changing a limit requires a server restart.

`limits.transfer.idle_timeout_seconds` controls staging and package-transfer socket inactivity on the agent listener.
The default is 300 seconds. Each transfer read or write resets its deadline.
This setting does not change the separate upstream package-fetch timeout or an agent's local client timeout.
Higher concurrency permits more simultaneous spools and database work.
Longer idle timeouts let stalled peers hold resources for more time.
Preview budgets do not limit total persisted log storage.
Normal workload CPU and memory quotas retain their existing behavior.

## External settings

The application does not replace operating-system or third-party configuration.
Preserve runtime socket fallbacks, `HOME`, XDG directories, proxy variables, TLS trust, and database-driver environment variables.
Keep deployment-script variables and stored project settings outside the startup configuration file.
Configure matching shared values on all peers, then restart the server and agents.
