# Manual checks after `make e2e-test`

## Releases with a deferred package pull

1. Attach a package repository to a disposable project. Use a missing version
   or an unreachable repository such as `https://asdf/{version}`.
2. Enter a version and click **Create Release**. Creation succeeds without a
   network request. Release detail shows **Package awaiting first pull**.
3. Deploy the release. A missing package, DNS/TLS/authentication failure or
   invalid ZIP fails the deployment before any steps execute. Inspect its logs.
4. Repeat via `POST /api/v1/projects/{id}/releases` with
   `{"version":"missing"}`: expect `201`, with the package still attached.
   `GET /api/v1/projects/{id}/releases/{releaseId}/artifact` returns
   `pending: true`, empty `sha256`, and `size: 0`.
5. Publish the package and deploy again. The first valid pull locks its
   checksum and size before steps start; the artifact API reports
   `pending: false`. Repeated pulls reject different bytes at that URL.
6. Replace the package bytes and click **Refresh** (or
   `POST /api/v1/projects/{id}/releases/{releaseId}/refresh`).
   Refresh validates and replaces the release checksum, updates steps/variables,
   and refuses active/unconfirmed deployments. New deployments use the refreshed
   pin; re-runs and rollback retain the original deployment pin.
7. Save a runbook from a pending release to check shared first-pull pinning.
   Refreshing or deleting the source release preserves the copied generation.

Retain the project, releases, runbooks and deployment history for repeat checks.
Keep schedules disabled. Automated API/web/container coverage is
`TestReleaseDeferredPackageAPIWebE2E`; rendered browser coverage is
`TestReleaseDeferredPackageBrowserE2E`. Run `scripts/e2e_artifacts.sh` for API checks.


## Server step resource policy

Server-side deployment steps have no DurpDeploy-imposed RAM ceiling or CPU
quota. PID limits, temporary-filesystem bounds, and other isolation remain.
Helper/infrastructure containers and remote agents retain their existing
resource policy; the agent follow-up is
[issue #14](https://github.com/DeveloperDurp/durpdeploy-agent/issues/14).

For a manual check on a running instance, create a disposable project through
`POST /api/v1/projects`, add a local Bash step using
`docker.io/library/bash:5.2` through `POST /api/v1/projects/$PID/steps`, and use
`printf payload > "$DURPDEPLOY_STAGE_DIR/data"; sleep 120` as its script.
Create its release and deployment through the corresponding project API
endpoints. During execution, inspect its `durpdeploy-$DID-*` step container:

```bash
podman inspect --format '{{json .HostConfig}}' STEP_CONTAINER
```

Use `docker inspect` on Docker hosts. Expect `Memory` and `NanoCpus` to be `0`,
`CpuQuota` to be `0` or `-1`, `PidsLimit` to be `128`, and `ReadonlyRootfs` to
be `true`. Check step containers rather than the staging keeper. External
host/cgroup limits can still constrain execution. Add a second step that reads
`"$DURPDEPLOY_STAGE_DIR/data"` to repeat the check with artifact handoff.
Let each deployment finish, or cancel through
`POST /api/v1/deployments/$DID/cancel` and wait for confirmed cleanup. Retain
the project, release, and history for repeat checks; no schedule is needed.

The isolated API regression check exercises live containers and both steps:

```bash
go tool templ generate
make swagger-ui-copy
DURPDEPLOY_CONTAINER_RUNTIME=podman \
DURPDEPLOY_CONTAINER_URL="unix://${XDG_RUNTIME_DIR}/podman/podman.sock" \
go test -tags=e2e -race -count=1 -run '^TestDeploymentStepResourcesE2E$' ./internal/handler/api
```

## Populated demo

For a complete local demo, run `make demo` (or ask a coding agent to run it).
The launcher builds the server, serves `https://citadel.durp.loc:<random-port>`
through Caddy with a certificate for that hostname, pairs a
separate containerized agent, verifies a remote deployment, and runs
`make e2e-test` against the same server. It then exits while the app and agent
remain running. Sign in as `admin@durp.info` with the generated `demo-…`
password printed by the launcher. The login details are also in the private
`login.txt` in its printed data directory.

Each invocation uses its own database, server identity, encryption key, agent
state volume and container names, so multiple demos can run together. The
HTTPS port listens on the computer's network interfaces; `citadel.durp.loc`
must resolve to this computer. The plaintext backend stays on loopback
(Podman) or Docker's private bridge. The agent's authenticated TLS listener
also has a random port and is reachable by the local container runtime.
The exact HTTPS origin configures secure cookies and passkeys; TOTP and
password login remain available. Each demo uses a temporary self-signed
certificate in its private `tls/cert.pem`; browsers may require trusting it.
The script validates that certificate and hostname on its API calls without
disabling TLS verification, and passes the certificate to `make e2e-test`
through `DURPDEPLOY_E2E_CA_FILE`. Requirements: Linux with `setsid`, Go, Node/npm,
make, OpenSSL,
Python 3, SQLite CLI, curl, and working local Podman or Docker. The existing development agent
image is pulled by `make dev-agent`; Caddy uses the existing development
proxy image `docker.io/library/caddy:2-alpine`.

Optional firewalld access: install `scripts/demo-firewall-helper.sh` root-owned
at `/usr/local/libexec/durpdeploy-demo-firewall` (mode 0755), and authorize only
that executable through a passwordless sudoers entry for your account. Run
`bash scripts/install-demo-firewall.sh` once as your regular account; it asks
for sudo authentication and validates its sudoers file before installing it.
To undo authorization, remove `/etc/sudoers.d/durpdeploy-demo` with sudo;
existing leases expire within 120 seconds when renewal fails. Future
`make demo` runs automatically start an unprivileged watcher. For an existing
demo, run `nohup setsid bash scripts/demo-firewall-watch.sh DEMO_DIRECTORY
>DEMO_DIRECTORY/firewall.log 2>&1 </dev/null &`. The helper grants TCP ports
32768-60999 in the `public` zone using a tagged runtime rich rule. It removes
only that tagged rule before renewing its fixed 120-second lease every 30
seconds; this briefly interrupts permission for new connections during renewal.
The watcher checks the original HTTPS container identity every second and
releases the rule when it stops. If the watcher dies, the last lease expires
within 120 seconds. Existing port/service permissions remain intact. This
permission allows the authorized account to lease any port in that range;
it does not verify that an individual port belongs to a demo. No permanent
firewall permissions are changed. Rule logging is limited to one per minute.
If setup or renewal fails, inspect `firewall.log`; initial failure aborts a new
demo instead of reporting a reachable URL. Ports outside the allowed range
require a new demo or administrator adjustment of the installed helper.

The demo agent enables `agent/3` container execution through the same local
Docker/Podman Unix socket used by the server. Start that socket before launch
(on Citadel: `systemctl --user start podman.socket`). It verifies Bash, Python
and PowerShell container capabilities, then retains a deployment that runs
all three interpreters remotely as well as a host step. Inspect the printed
agent deployment and `agent-capabilities.json` / `agent-deployment-logs.json`
in its data directory. The rootless Podman agent maps the operator to its
service UID/GID using `keep-id`; `:U` adjusts only the private named state
volume's ownership. Host socket permissions and labels stay unchanged. The
supervisor's SELinux label separation is disabled for socket access; each
container-mode workload retains its seccomp, non-root user, resource limits,
read-only root and no-network boundary, with no socket or host directory mounted. The agent
supervisor has the operator's runtime API authority, as does the server.
Host-mode scripts run inside that supervisor and inherit its socket access;
use only scripts trusted with the operator's account.

For server/template/asset changes, finish script edits first, then run
`make demo-refresh DEMO_DIR=/absolute/demo/path`. It rebuilds the server and
CSS/JS, then restarts only the server on the same ports. URL, login, database,
encryption key, server identity, paired agent, proxy, firewall watcher and
certificate remain in place. No re-pairing or E2E repopulation occurs. Wait for
deployments to finish and confirm cleanup; unfinished or lost remote work blocks
refresh.
The final idle check and old-server shutdown hold a database write barrier;
new deployments cannot be admitted until the replacement is ready to start.
The existing server must be running so its configuration can be retained.
Startup settings are preserved rather than taken from the caller's environment.
Inspect `server-refresh-build.log` and `server.log` if refresh fails.
Replacement startup/health failure restores the previous server when idle and
retains `server-refresh-failed.log` and `bin/durpdeploy.failed` for inspection.
Fix the source and retry the same server-only refresh command.

Database query/schema/migration changes require
`make demo-refresh-full DEMO_DIR=/absolute/old/demo/path`. Also use full refresh
for agent version/configuration or demo startup configuration changes.
It stops the old demo and builds/populates a new one. Return the new URL,
login, data directory and certificate path; the old data remains on disk.

Let active deployments finish or cancel them and wait for confirmed cleanup.
Use the printed stop command, or `make demo-stop DEMO_DIR=/absolute/demo/path`.
Stopping stops the HTTPS proxy, server and agent, and retains the database,
certificate, logs, encryption key and agent identity for
inspection. It does not stop other demos or your development server. Startup
failure stops the resources started by that invocation and retains logs.
`build.log`, `bootstrap.log`, `server.log`, `agent.log`, and `e2e.log` are in
the private data directory. The retained `Demo agent execution` project has
a successful remote deployment and can be run again. All E2E schedules stay
disabled; the table below explains how to enable one for a manual check.

Run `make e2e-test` against your running server. Use the printed project links.
Projects, lifecycles, and templates use stable names with no run ID. Each run
replaces the previous E2E fixtures and their test history. Projects carry a
`Managed by make e2e-test.` description; unmanaged name collisions stop the
suite. The template names `python-template` and `python-step` are reserved
for the interpreter example. Unrelated records are preserved. All projects
reuse the shared `dev`, `test`, and `prod` environments. Environment CRUD
checks create temporary fixtures and delete them. Existing environments from
older runs are left in place. Log in as the
configured `E2E_ADMIN_EMAIL` (default `e2e-admin@test.local`); use the password
you configured, or the default test password `e2e-admin-password-1234`.
The command also ensures the test admin `admin@durp.info` exists.

For concurrent agent work, use a separate worktree and isolated server per
agent. `make e2e-test-isolated` chooses its own browser port, agent listener,
SQLite database, identity directory, and container namespace. Ordinary Go
test processes also replace inherited container namespaces before constructing
runners. Keep generated assets current in each worktree before Go tests.
Startup cleanup can only affect containers belonging to that test process.

Run `DURPDEPLOY_CONTAINER_RUNTIME=podman bash scripts/e2e_parallel_test.sh`
to exercise two simultaneous API/web staging and retry suites. It also checks
valid passkey registration and assertion against each allocated origin,
that an occupied explicit port fails, and that a neighboring container survives
Go test startup. It requires the installed Playwright Chromium browser.
CI runs the same regression with Docker.

The isolated suite prints its chosen URL and namespace. Set
`DURPDEPLOY_E2E_PORT` or `DURPDEPLOY_CONTAINER_NAMESPACE` only when you own
that port or namespace; explicit overrides must be unique across concurrent
runs. Browser and HTTP failure evidence uses a separate run directory by
default. Explicit evidence-directory overrides must also belong to one run.
`make e2e-test` intentionally changes its configured running instance and
retains examples; concurrent agents must each point it at their own instance.

Projects, environments, lifecycle stages, release snapshots, deployment logs,
templates, and test users remain in the database. Steps removed during tests
remain in immutable release snapshots: deploy the named release to repeat
them. Temporary cookies and plaintext API tokens are removed. Stage files
are temporary execution data; rerun the release to reproduce file handoff.

The variable-scope browser checks retain separate `variable-before-deploy-*`
projects and a `variable-scopes-*` lifecycle on each run, and print links to
their Variables pages. These projects have no deployments; keep them that way
when repeating issue #159. Each has a `first-scoped-release` snapshot.

For API checks, create your own token at `/settings/tokens`. Open
`/api/swagger/index.html`, use **Authorize**, and use **Try it out** with IDs
from the printed projects. API paths below start with `/api/v1`. Web checks
use the browser forms; for invalid values excluded by a form, edit the request
in browser developer tools and retain its session cookie and CSRF token.
Use only the disposable E2E examples for edits and deletion checks.

| Scenario | Retained example and manual action | Expected result |
|---|---|---|
| Releases list | Open `TestProject` → Project menu → Releases. Create a new release and open its version link. Repeat on a phone. Use the project’s Deploy action to start a deployment. | The list has Version, Created At, and Actions columns, with no environment selector or Deploy/Force controls. Creation, snapshot details, and deletion still work. Deployment choices remain on the Deploy page. |
| Mobile form focus | On a phone, open New/Edit Project, Environment, Lifecycle, Template, project steps, runbook steps, and the fullscreen script editor. Open Variables and add a variable. Tap a field, then dismiss and reopen the form. Repeat with a desktop mouse and keyboard. | Modals open with a header control focused, and variable forms do not select a field on load or refresh. The phone keyboard stays closed until a field is tapped. Desktop forms retain initial field focus, Tab navigation works, and dismissal restores focus to the opener. |
| Project menu and deployment history | Open `TestProject`, open Project menu, and select Deployment history. Also dismiss the menu with Close, Escape, and an outside click. Repeat as a project viewer and on a phone, resize with the drawer open, then enable reduced motion. Open Runbooks and use Back. | The drawer and its backdrop stay below the top bar and fill the remaining screen height. It slides from the right and back out on dismissal; reduced motion disables travel. Dismissal restores focus to Project menu. Selection opens deployments filtered to TestProject. Viewers have no Deploy or Schedules control. Runbooks has Back as its last header action and returns to the previous page. |
| Delete agent (#143) | In Admin > Agents, pair a disposable idle agent. Cancel Delete once on its detail page, then confirm. Repeat from the list with a previously revoked disposable agent. Recreate a disposable agent to retain a repeatable example. For the API, call `DELETE /api/v1/admin/agents/{id}` with an admin token. Drain and finish any work and log delivery first. | Cancellation leaves it visible; confirmation returns to the list and removes it. Detail GET and repeated DELETE return 404; successful API delete is 204 and audited as `delete_agent`. Historical deployments remain. Unresolved execution/cleanup or buffered logs block deletion (API 409, web error toast) and leave the agent unchanged. Viewer/deployer access is forbidden. |
| Rejoin deleted agent (#143) | Stop the disposable deleted agent after reconciling work. Move only its private `state.json` aside, keep its identity keys, and restart. Try an incorrect code, then pair from Admin > Agents with the current code and unchanged fingerprint; repeat through `POST /api/v1/admin/agents/pair`. | The listener rejects incorrect codes. Approved pairing creates a new registration and agent ID and allows polling again. The old ID remains absent; historical deployments remain with agent references cleared. |
| Login, sessions, and CSRF (F0) | Sign out and request `/` without following redirects; sign in with the E2E admin. Submit POST `/projects` with the session cookie but omit the CSRF token (edit the request in developer tools). | Anonymous GET redirects to login (303); login redirects to the app; protected pages load; missing CSRF rejected (403). |
| Happy path, steps page, scoped variables (F3.1) | `TestProject`, release `1.0.0`, environment `dev`. Deploy it and open Steps and live logs. | Success; logs contain `default-variable=hello`. |
| Variable scopes before deployment (#159) | Open a printed `variable-before-deploy-*` project with zero deployments. In Variables, create a variable, edit its environment, and use Override on `DEFAULT`. Repeat on desktop and phone, through a direct link and the project's Variables menu. Use the lifecycle's stage environment for bound projects. Inspect `first-scoped-release` through `GET /projects/{id}/releases/{release_id}` and the mutable `API_VALUE`. | Eligible scopes are selectable in all three forms. API create/update accept eligible scopes before deployment and reject environments outside a bound lifecycle. `NEW`, `EDIT_ME`, `DEFAULT`, and `API_VALUE` retain their scoped values in the first release; its `API_VALUE` remains `snapshotted` while the mutable value is `changed`. |
| Deployment detail without step definitions (#154) | Open a retained deployment on desktop and phone; inspect its step logs, status, actions, and verification results. Reload after completion, then open its release link and project Steps page. GET `/deployments/{id}`, `/deployments/{id}/logs`, and `/projects/{id}/releases/{relId}` through the API. | Deployment detail has no bottom Steps table, script cards, or step-definition empty message. Step log panels and controls remain usable; the release page and API `steps_json` retain the scripts. |
| Live deployment log timestamps (#163) | Deploy a retained slow release, such as `TestProject` `1.0.1`, and keep its deployment page open while new logs arrive. Record a new entry's timestamp, refresh while running, and compare the same entry. Repeat on desktop and phone. Read `/api/v1/deployments/{id}/logs/stream?format=structured` and compare the entry with `/api/v1/deployments/{id}/logs/{logId}`. | Streamed entries immediately show `[YYYY-MM-DD HH:mm:ss]` in the same gray styling and server timezone as historical entries. Refresh/reconnect preserves timestamps and does not duplicate entries. SSE `created_at` matches the stored log; `display_timestamp` matches the page. Highlighting, step/agent labels, and keyboard disclosure controls remain usable. Cancel the slow example if needed and wait for confirmed cleanup. |
| Deployment notes (F3.1b) | Inspect the original and `smoke-test-audit` deployments; submit another `1.0.0` deployment with a note. | Only the deployment given a note shows it. |
| Dashboard deployment navigation | Open `/`. Click a status or date in any deployment entry, then return and use Tab and Enter. Repeat on a phone and resize the page. | Phone entries are separate rounded cards with all fields visible. Desktop entries use the existing table. The whole card or row opens its deployment; hover and keyboard focus highlight it. Text stays plain, with no added buttons. |
| Cancel (F3.2) | Deploy `TestProject` release `1.0.1`; click Cancel while running. | Cancelled; the retained snapshot still contains `LongStep`. |
| Timeout and rerun (F3.2b/c) | Deploy release `1.0.2`, then use Re-run on the failed deployment. | Both fail from the one-second timeout within 25 seconds; rerun note identifies the original deployment. |
| Retry (F3.2d) | Deploy release `1.0.4`. | Fails after retries; logs show `attempt 1` and `retrying`. |
| Empty project validation (F3.3) | Submit an empty name on New Project (edit the request if browser validation blocks it). | Web response 422; no project created. |
| Missing variable (F3.4) | Deploy release `2.0.0` in `TestProject`. | Success; logs contain `missing-variable-empty`. |
| Lifecycle order (F3.5) | Open the printed Lifecycle promotion project link (`LC-Project`, lifecycle `LC`). Inspect dev → test → prod, create a fresh release, deploy to dev, then try prod before test. Then deploy test and prod. | Early prod rejected (422); dev → test → prod succeeds. The suite verifies the retained lifecycle assignment, stage order, and successful `1.0.0` deployment in each stage through the API. Use a fresh release to repeat promotion. |
| Lifecycle approval (F3.5b) | Open the printed pending approval in `AppProject`; approve it. Deploy `1.0.0` to its Prod environment again to repeat. | Waits in `pending_approval`; approval starts execution and succeeds; recorded approver is the signed-in admin. |
| Force deployment (F3.6) | In `LC-Project`, create a fresh release and select Force when deploying directly to Prod. | Deployment accepted without prior stages. |
| Environment restrictions and deploy pages (F3.7–9) | Inspect TestProject and LC-Project deploy forms. Remove prod from the disposable LC lifecycle, then submit an LC release to prod with and without force. Restore the prod stage after the check. | Free project lists dev/test/prod; LC form excludes prod while its stage is absent; outside environment rejected (422) even with force. |
| Cross-project release (F3.10) | Submit an LC-Project release ID to `DP-cross-proj`'s deploy endpoint. | Rejected (400); no deployment created. |
| Scheduler (F3.11) | Open TestProject's Schedules. Enable its retained `* * * * *` schedule, wait for a minute tick, then disable it. | A successful `1.0.0` deployment appears with `Scheduled:` in its note. The harness disables this schedule after checking it, before later queue and browser checks. |
| User management and audit (F3.12) | `/admin/users`: the printed `e2e-newdeployer-…@test.local` is recreated after the deletion test. Its test password is `newdeployer-pass-1234`. Sign in separately; as admin promote/demote it, then delete it and recreate a disposable user for another pass. Try deleting your own admin account. Inspect `/admin/audit`. | Deployer cannot access admin pages; promotion allows access; role change invalidates old sessions; deletion removes the user; self-delete rejected (422); audit records create/update/delete actions. |
| Token, health, project/environment/step/release API (A1–6) | Mint a token. GET `/healthz` with and without it. Inspect `e2e-api-project`, environment `dev`, `long-step`, and release `v1` through list/detail endpoints. Create disposable counterparts through POST. | Health 200; create returns IDs; lists and details show matching names. |
| File handoff and retry | Deploy `stage-handoff` release `1` through the web and API. Inspect its producer/consumer scripts and logs. | Both succeed; logs show retry and `stage-handoff-content-and-size-ok`; each deployment starts with fresh staging data. |
| Reserved staging variable validation | In `stage-handoff`, create or rename a variable to `DURPDEPLOY_STAGE_DIR`; try selecting it in a new step's variable list through web and API. | Variable writes rejected (422); step rejected (web 422, API 400). Existing `LIMITED` variable is retained for rename checks. |
| Interpreter forms, containers, variables | `e2e-interpreters`: inspect new/edit step forms, current PowerShell step, releases `mixed-v1` and `powershell-immutable`, and deployment pages. Deploy both releases. | Choices Bash/Python/PowerShell; current interpreter selected; mixed logs show `bash-e2e` and `python-e2e=container-value`; PowerShell logs show `powershell-e2e=container-value` without prompts or escape sequences. |
| Interpreter validation and release refresh | POST a step with `interpreter: "/bin/sh"` (400). Edit the Python step to PowerShell, then refresh the used `mixed-v1` release through the API (200) or its web Refresh button (303). Inspect the release and original deployment, then re-run the original mixed deployment. | Invalid interpreter rejected; refreshed release shows PowerShell; original deployment and its rerun still execute Python. API alias `powershell` normalizes to `pwsh`. Refresh is available after failed or successful deployments. Active or unconfirmed runs and buffered agent logs return 409 until they finish. |
| Template forms, history, save/insert | Open `python-template` in Templates and its History; inspect Python and PowerShell versions. In interpreter project use Save Template and insert an existing template. | Form selections/history preserve interpreters; save/insert preserves `pwsh`; API template GET/PUT/history agree with the web. |
| Agent container placement (#99) | In a disposable project, add a step with Run step on = Agent, Agent execution mode = Container, image `alpine:3.20`, and variable limit `DEPLOY_ENV`. Save a template and a release. Change the mutable step to Host, then inspect the template history and the original release through the API. Repeat in a runbook with one container and one host step. | Host is the default; container mode requires an image. Modes, images, and limits survive create/edit and immutable snapshots. Agent-host steps reject images; server-container steps reject agent container mode. |
| Agent/3 execution and cleanup (#99) | Assign a ready agent/3 agent to an environment and deploy the retained container release. Inspect `/admin/agents/{agent-id}` and deployment logs. If the agent reports `cleanup_unconfirmed`, restore its same local engine and let it reconcile before polling again. | Health shows modes, runtimes, and container interpreters. Bash, Python, and PowerShell use their selected images and limits. Unconfirmed cleanup blocks queued work and rerun; readiness confirms cleanup separately and preserves the original result. Agent/1 and agent/2 cannot claim container steps or confirm cleanup. |
| API cancel/status (A7) | POST a deployment of e2e-api-project release `v1`, GET its status, POST `/deployments/{id}/cancel` while running. | Running → cancelled; cancel response 200. |
| API log streaming (A8) | Deploy e2e-api-project `v2`; GET `/deployments/{id}/logs/stream?format=ndjson`. | Each replayed/live line is a JSON object with a `line` field. |
| Project/environment queue isolation | Deploy e2e-api-project `queue-check` to dev, then deploy `v2` to dev. While the first runs, deploy TestProject `1.0.0` to dev and e2e-api-project `queue-check` to test. Cancel both slow deployments after inspection. | Only the second dev job in e2e-api-project queues. The other project and test job run independently. Cancelling the dev head admits its next queued job. Release snapshots and printed deployment links remain available. |
| API failure paths (A9) | POST `/projects` without a token; GET a nonexistent project ID; sign in as the printed `e2e-viewer-…@test.local` (test password `e2e-viewer-pass-1234`). To repeat the API viewer check, promote that test user to deployer, mint its own token, then demote it to viewer before POST `/projects`. | No token: 401; missing project: 404; viewer write: 403. Admin changes invalidate sessions; sign in again for web checks. |
| Swagger (A10) | Open `/api/swagger/`, `/api/swagger/index.html`, and `/api/swagger/spec` without authentication. Inspect verification/rollback security and environment error responses in the spec. | UI/spec 200; Swagger 2.0; verification and rollback require bearer auth; environment POST/PUT document 403 and 422. |
| Terraform artifact approval | `terraform-approval`: inspect printed approved, rejected, and pending deployments. Open the pending review, download the plan, then approve or reject. Deploy `approval-demo` again to repeat either decision. | Real Terraform summary: one added, one modified, one removed resource, zero reads, unverified review label. Approval applies the saved plan, verifies the resulting state and no remaining changes, then succeeds; rejection prevents the saved-plan apply and ends rejected. Downloaded artifact SHA-256 matches the gate. |

Terraform uses only the built-in `terraform_data` resource; it creates no
cloud resources. Each deployment first seeds two disposable resources in its
local state. On its deployment page open **View resource changes** and
check `approval_demo` (added), `modified_demo` (modified), and `removed_demo`
(removed), their Before/After values, public messages,
masked token, and `(known after apply)` values. The same data is available at
`GET /api/v1/deployments/{id}/artifact-gates/0/review`. Viewers must receive
403 for this endpoint and must not see the disclosure. No displayed value
should contain the demo's `TF_VAR_demo_secret` project secret.
The release page's Steps table must show normal script previews. Deployment
details show step logs and verification without a Steps definitions section.
Logs must include Terraform's
plan output, with green additions, red removals, and yellow modifications,
both live and after a reload. The resource review uses matching Add/Remove/Modify
badges and red/green Before/After borders. Sensitive values remain masked by
Terraform and DurpDeploy's project-secret scrubber, including old values of removed resources.
Pending artifact approvals expire after 24 hours. Their release stays
available for a fresh plan. Use the same container runtime and image store as
the server when running the harness. No retained schedule should run unattended
after manual checks.

Decision buttons appear automatically once artifact publication and container
cleanup finish; the review panel polls every three seconds. During publication,
expect a preparing message and no Approve/Reject controls. The public gate
metadata reports `decision_ready: false` until decisions are available.
To check stale-form recovery, open the same pending deployment in two tabs,
approve or reject in one, then submit the older form in the other. The older
submission returns to deployment details with a warning and refreshed gate
state, without recording a second successful decision in the audit log.

The pending Terraform example owns its project's `dev` execution slot. New dev
deployments and runbooks for that project queue until you approve, reject, or
cancel it. Other projects in dev and that project in test/prod can run at the
same time. The suite
creates this example after its execution checks. Approval waits in earlier
projects no longer block the new run's projects; release snapshots,
deployment history, and runbook versions remain available. Each run leaves a
fresh pending example for manual approval checks.
# Shared lifecycle variables

On a release with a deployment awaiting approval, click **Refresh** and accept
the confirmation. The release page must remain open and show **Unable to
refresh this release**, with the snapshot lock reason, cancellation/completion,
cleanup and log sync guidance, and a **View project deployments** link.
The API refresh must return `409` while that deployment is active. Cancel a
disposable deployment or let it complete, then retry: the API returns `200`
and the web refresh returns to the release page with the error cleared.
The retained demo has a disposable pending-approval case at
`/projects/24/releases/36`, with `/deployments/46` awaiting approval; check its
current status before retrying. Keep its release/history after testing.

`make e2e-test` retains a `shared-lifecycle-<run>` lifecycle and two
`shared-project-<run>` projects. The suite prints their editor, variables,
release and deployment links. On `/lifecycles`, tap the lifecycle card to open
its full page, then find **Shared variables** below **Promotion order**. Existing
lifecycles open the page instead of a settings modal on every screen size.
Open the lifecycle's **Shared variables** section
to add an unscoped value or an override for one of its stages. Secret values
show a mask; leaving an existing secret blank keeps its saved value.

Open the first project's Variables page: REGION shows **Project override**
with its lifecycle default, while the second project inherits the shared value.
Choose **Reset to inherited** in the first project to remove only its override.
Reset removes all local rows with that name and scope, including legacy
unscoped duplicates; other scopes and projects keep their values. The public
API supports the same action with `DELETE /api/v1/projects/{id}/variables/{varId}?reset=inherit`.
After removing a lifecycle stage, edit one of its retained variables: Save
must wait for an explicit **All stages** or current-stage selection. A blank
secret field still preserves the saved secret after choosing the scope.
Choose **Override**, enter a project value and save to recreate the example.
Global admins manage shared values; project deployers can override their own
values and viewers see no write controls. Unassigned projects inherit nothing.

Both retained `shared-v1` releases and successful deployments contain the
original value even though the suite changed the lifecycle value before
deployment. Shared changes affect new releases, explicit release refreshes and
new runbook versions. Inspect the logs: the shared secret is redacted. To check
environment precedence, add a lifecycle REGION override for the stage; it wins
over an unscoped project override, while a project override for that same stage
wins over the lifecycle value. These examples have no enabled schedules.
Lifecycle assignment grants execution access to shared secrets. Only global
admins can attach a lifecycle to a project; project admins can retain or
remove an existing assignment. As a deployer, creating or updating a project
with a new lifecycle ID returns 403 without changing the project.
