# Manual checks after `make e2e-test`

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

Projects, environments, lifecycle stages, release snapshots, deployment logs,
templates, and test users remain in the database. Steps removed during tests
remain in immutable release snapshots: deploy the named release to repeat
them. Temporary cookies and plaintext API tokens are removed. Stage files
are temporary execution data; rerun the release to reproduce file handoff.

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
| Login, sessions, and CSRF (F0) | Sign out and request `/` without following redirects; sign in with the E2E admin. Submit POST `/projects` with the session cookie but omit the CSRF token (edit the request in developer tools). | Anonymous GET redirects to login (303); login redirects to the app; protected pages load; missing CSRF rejected (403). |
| Happy path, steps page, scoped variables (F3.1) | `TestProject`, release `1.0.0`, environment `dev`. Deploy it and open Steps and live logs. | Success; logs contain `default-variable=hello`. |
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
| Scheduler (F3.11) | Open TestProject's Schedules. Enable its retained `* * * * *` schedule, wait for a minute tick, then disable it. | A successful `1.0.0` deployment appears with `Scheduled:` in its note. The harness disables schedules on exit. |
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
The Steps table must show normal script previews. Logs must include Terraform's
plan output, with green additions, red removals, and yellow modifications,
both live and after a reload. The resource review uses matching Add/Remove/Modify
badges and red/green Before/After borders. Sensitive values remain masked by
Terraform and DurpDeploy's project-secret scrubber, including old values of removed resources.
Pending artifact approvals expire after 24 hours. Their
release stays available for a fresh plan. Use the same container runtime and
image store as the server when running the harness. No retained schedule
should run unattended after manual checks.

The pending Terraform example owns its project's `dev` execution slot. New dev
deployments and runbooks for that project queue until you approve, reject, or
cancel it. Other projects in dev and that project in test/prod can run at the
same time. The suite
creates this example after its execution checks. Approval waits in earlier
projects no longer block the new run's projects; release snapshots,
deployment history, and runbook versions remain available. Each run leaves a
fresh pending example for manual approval checks.
