# Manual checks after `make e2e-test`

## Dedicated agent port

Run `make agent-e2e-sqlite` to check explicit ports and the default dedicated port `23187`.
Keep port `23187` available before running this isolated suite.
The suite checks web pairing, API deployments, polling, and streamed logs with the pinned standalone agent source.
Browser screenshots and request results appear under `.omo/evidence/continue-remote-agent-rollout/`.
The isolated suite removes its temporary servers and databases.

For an existing installation, follow the [port migration](agents.md#migrate-the-dedicated-agent-port) instructions.
Open **Admin, Agents** and confirm that the existing agent returns to healthy status.
Run a deployment against its environment through the web UI.
Confirm that streamed logs arrive and the deployment succeeds.
Repeat deployment through `POST /api/v1/projects/{id}/deployments`.
Confirm that `GET /api/v1/deployments/{id}/status` reports success.
Keep the deployment links for repeat checks.
This port check creates no schedules.

## Four permission levels

Keep the printed **Permission role project**, release link, and four account logins.
The suite retains deployment history and creates no schedules for these examples.
Sign in with each printed account.
Confirm that Viewer cannot deploy or edit content.
Confirm that Deployer can deploy the existing release and read its logs.
Confirm that Deployer cannot edit, delete, refresh, or create project content.
Confirm that Maintainer can author content and execute runbooks.
Confirm that Maintainer cannot approve deployments or manage global users.
Retry and cancel a deployment with the Deployer account.
Confirm that the retry retains the original deployment snapshots.
Open each forbidden form through its direct URL.
Confirm that it shows a permission message.
Submit a forged configuration request through the browser or API.
Expect a browser HTMX toast, a native browser `403`, or an API JSON `403`.
Read [roles](roles.md) for membership ceilings and upgrade behavior.

## Project deployment summaries

Create two projects that share one environment.
Deploy a release from the first project through `POST /api/v1/projects/{id}/deployments`.
Wait for the deployment to succeed.
Deploy six releases from the second project into the same environment.

Read `GET /api/v1/projects/{id}/deployments?env_id={environment_id}` for the first project.
Confirm that the API lists only that project's deployments, newest first.
When creation times match, confirm that larger deployment IDs come first.

Open the dashboard, project card, project environment table, and successful deployment detail.
Confirm that each view shows the first project's own release and status.
Repeat at desktop and phone widths.
Repeat with a Viewer and a Deployer.
Repeat with a lifecycle assigned to the project.

Create another project without deployments.
When an environment is available, expect **No deployments yet** on its card.
When no environments are available, expect **No environments**.
After a failed attempt, confirm that the environment table retains the earlier successful release.
These checks require no schedules.

```bash
bash scripts/go_test.sh -race -count=1 -run '^TestProjectEnvironmentDeploymentHistory$' ./internal/repository
bash scripts/go_test.sh -tags=e2e -race -count=1 -run '^TestProjectPanelScopeAPIWebE2E$' ./internal/handler/api
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -run '^TestProjectPanelScopeBrowserE2E$' ./internal/handler/api
```

## Variable identifier guidance (A13)

Open a disposable project's variable forms at phone and desktop widths.
Confirm that Name guidance explains the ASCII format, 255-character limit, first character, and reserved names.
Enter `bad name` and attempt to submit.
Confirm that validation retains the draft and explains the required format.
Create `Mixed_2` with an empty value.
Edit that variable and attempt to rename it to `1BAD`.
Confirm that rejection leaves the saved variable unchanged.
Correct the name and save again.
Repeat invalid creation and updates through the public variable API.
Expect `422` for NUL, equals signs, embedded whitespace, leading digits, hyphens, Unicode, and names longer than 255 characters.
Confirm that invalid requests store no changes.
Confirm that empty values, masked secrets, and environment overrides still work.
Keep all examples for repeat checks.
These checks require no schedules.

```bash
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -run '^Test(VariableNames(APIWeb|Browser)E2E|ProjectVariableNamesRejectInvalidWrites)$' ./internal/repository ./internal/handler/api
```

## Duplicate release recovery (A12)

Create a disposable project and a release named `edge-v1`.
Open the releases page at phone and desktop widths.
Submit `edge-v1` again.
Confirm that the error retains `edge-v1` and instructs you to enter a different version.
Confirm that the page shows error feedback and creates no duplicate release.
Change the version and submit again.
Confirm that the new release appears beside the original release.
Post the original version through `POST /api/v1/projects/{id}/releases`.
Expect `409` with the same recovery instruction.
Change the API version before retrying.
Expect `201` and a new release ID.
Confirm that the original release snapshot remains unchanged.
Keep all examples for repeat checks.
These checks require no schedules.

```bash
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -run '^TestReleaseDuplicateRecovery(APIWeb|Browser)E2E$' ./internal/handler/api
```

## Release variable guidance (A11)

Create a disposable project with a lifecycle and a stage environment.
Create a project variable named `VALUE` with value `old`.
Create a lifecycle variable named `SHARED` with value `initial`.
Create a step that prints both variables.
Create a release.
Change the project value to `new` and the lifecycle value to `current`.
Open the release at phone and desktop widths.
Confirm that separate Project variables and Lifecycle variables instructions explain their refresh behavior.
Confirm that the table retains the saved project value `old`.
When secret variables exist, confirm that their values remain masked.
Deploy the existing release without Refresh.
Expect `project=old shared=current` in its logs.
Select Refresh on the release.
Accept the confirmation.
Confirm that the table shows `new`.
Deploy the refreshed release.
Expect `project=new shared=current`.
Read the release through `GET /api/v1/projects/{id}/releases/{relId}` before and after Refresh.
Confirm that its project variable values agree with the visible table.
Keep all examples for repeat checks.
These checks require no schedules.

```bash
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -run '^TestReleaseVariableGuidance(APIWeb|Browser)E2E$' ./internal/handler/api
```

## Deployment re-run inputs (A10)

Create a disposable project with a non-secret variable named `RERUN_VALUE` set to `base-one`.
Create a step that prints the variable with an identifying script marker.
Create a release, then deploy it in a disposable environment.
Keep the original deployment link and logs.
Change the project variable to `base-two`.
Change the step marker, then refresh the release.
Change the project variable to `base-three` without another refresh.
Open the original deployment, then select Re-run.
Confirm that the message identifies current release and lifecycle variables, original steps, and unchanged original logs.
Select Cancel, then confirm that no deployment was created.
Select Re-run again, then accept the confirmation.
Confirm that the new deployment prints `base-two` with the original script marker.
Read the original logs, then confirm that they remain unchanged.
Repeat at phone and desktop widths.
Repeat through `POST /api/v1/deployments/{id}/redeploy`.
Keep all deployment examples for repeat checks.
These checks require no schedules.

```bash
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -run '^TestRerunInputs(APIWeb|Browser)E2E$' ./internal/handler/api
```

## Reachable Step form actions (A09)

Open a disposable project's Steps page at 375 × 844.
Select Edit for a retained step.
Scroll through the form.
Confirm that Save and Cancel remain visible above the scrolling fields.
Focus Container image, then scroll it toward the top of the fields.
Confirm that the action header does not cover the field or its focus indicator.
Repeat with a shortened viewport at 375 × 420.
Repeat at desktop width and through Add Step.
Change Timeout and Max retries, then select Cancel.
Confirm that the API still returns the original values.
Submit an empty Name with changed Timeout and Max retries.
Confirm that validation keeps both lower fields.
Correct Name, then select Save.
Confirm that `GET /api/v1/projects/{id}/steps/{stepId}` returns the saved values.
Keep the project and step for repeat checks.
These checks create no executions or schedules.

```bash
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -run '^TestStepFormActionsBrowserE2E$' ./internal/handler/api
```

## Swagger request target

Open `/api/swagger/` through the instance's HTTPS proxy on a non-default port.
Before authorization, confirm that **API target** shows the current HTTPS origin and `/api/v1`.
Expand `GET /healthz` in Swagger.
Select **Try it out** and **Execute**.
Expect status `200` and `{"ok":true}` from the current instance.
Confirm that **Request URL** uses the proxy's HTTPS host and port.

Repeat through the direct HTTP listener.
Confirm that the request uses its HTTP host and port.
Before authorization, execute `GET /projects` and expect `401`.
Authorize a valid token and repeat the request.
Expect the token's permitted projects.
Clear authorization after the check.

Run the focused API and browser regressions:

```bash
bash scripts/go_test.sh -count=1 -run '^TestSwaggerInstanceAPIE2E$' ./internal/handler/api
bash scripts/go_test.sh -tags=e2e,packagebrowser -count=1 -run '^TestSwaggerInstanceBrowserE2E$' ./internal/handler/api
```

## Project saves after permission changes

Use a project Maintainer and an administrator in separate browser sessions.
Open **Edit Project** as the Maintainer.
Enter an unsaved description.

In the administrator session, change the Maintainer's project membership to Viewer.
Submit the open project form.
Expect an error toast and a visible error message inside the form.
Confirm that no success toast appears.
Confirm that the form, description, and page URL remain unchanged.

Read `GET /api/v1/projects/{id}` as the administrator.
Confirm that the saved description remains unchanged.

Restore the project membership to Maintainer.
Save the retained form.
Confirm that the description now appears on the project page and through the API.

Repeat the check with a project Deployer downgrade.
Repeat the check with global Viewer and Deployer downgrades.
Restore both roles after each check.
Repeat through the project edit dialog.
These checks create no schedules.

Run the focused API and browser regressions:

```bash
bash scripts/go_test.sh -count=1 -run '^TestProjectStaleSaveAPIWebE2E$' ./internal/handler/api
bash scripts/go_test.sh -tags=e2e,packagebrowser -count=1 -run '^TestProjectStaleSaveBrowserE2E$' ./internal/handler/api
```

## Project-scoped API tokens

Run `make e2e-test` against the running server.
Keep the printed selected-project, denied-project, and token management links.
The suite retains `e2e-token-a-*`, `e2e-token-b-*`, and `e2e-token-foreign-*` projects.
It also retains token metadata for all four roles and a revoked token.
These scenarios create no schedules.

Open `/settings/tokens` as an administrator.
Confirm that a new token defaults to **All accessible projects**.
Confirm that the permission selector defaults to `viewer` for either scope.
Create an all-project token for each role and check the same permission limits.
Confirm that permission-limited tokens cannot create or edit tokens through the API.
Select **Selected projects**.
Select **Choose projects**.
Search for each printed selected project and select its checkbox.
Confirm that filtering preserves checked projects outside the current results.
Select **Done** and confirm that both names appear below the picker button.
Create a token for each supported role.
Copy each bearer value from its one-time banner.
Return to the list and confirm that the bearer value disappears.

Submit a selected-project token without any projects.
Expect a validation message and no new token.
Use **Edit scope** to change a token's role and project selection.
Confirm that the list shows the saved role and projects.
Confirm that editing scope does not display the bearer value again.

Check the page at widths of 375, 430, 540, 768, 980, and 1280 pixels.
Confirm that each form control fills its row without overlap.
Below 1280 pixels, confirm that token cards show names, access details, and actions.
Open **Edit scope** on a phone and save a changed selection.
Confirm that **Choose projects** opens a full-screen picker on the phone.
Confirm that long project names wrap and each project row is easy to tap.
Confirm that the selection count, **Clear selection**, and **Done** remain visible while scrolling.
Search for an unmatched name and confirm that the picker shows an empty result message.
Press Enter in the search field and confirm that the token form does not submit.
Close the picker and confirm that focus returns to **Choose projects**.
Select **All accessible projects** in the new token form.
Confirm that the project control disappears and the permission selector stays visible.
Select **Selected projects** and confirm that the project control returns.

Call `GET /api/v1/projects` with each token.
Expect only selected projects and matching pagination totals.
Request the denied project and its deployments, variables, releases, runbooks, packages, artifacts, and logs.
Expect denial without unrelated metadata.
Attempt project creation, editing, and deletion with Viewer and Deployer tokens.
Expect `403`.

Deploy with the deployer token in a selected project.
Expect the owner's existing membership and approval rules to apply.
Request `/api/v1/admin/stats` or create another token with any scoped token.
Expect `403`, including for administrator-owned tokens.
Remove the owner's project membership and repeat the request.
Expect denial unless the owner remains a global administrator.

Open a log stream with a scoped token.
Change its selection to remove that project through the owner's browser session or a global token.
Expect the stream to close within the existing authorization recheck interval.
Revoke the token and repeat an API request.
Expect `401`.

Run the isolated API and browser regressions with these commands:

```bash
bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestProjectTokens.*APIE2E$' ./internal/handler/api
bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=5m -run '^TestProjectTokensBrowserE2E$' ./internal/handler/api
```

## Project member roles

Open the printed **Member role editor** link as the global administrator or printed project administrator.
Select **Edit role** for an existing member.
Confirm that the selector shows the current project role.
Select another role, then select **Cancel**.
Open the editor again and confirm that the saved role remains selected.

Select `admin`, then select **Save**.
Confirm that the role changes and a success message appears.
Repeat with `viewer`, `deployer`, and `maintainer`.
Repeat inside the project edit dialog and at a phone viewport width.
Confirm that unsaved project settings remain unchanged.

Read `GET /api/v1/projects/{id}/members` after each change.
Use `PUT /api/v1/projects/{id}/members/{userId}` with a JSON `role` to repeat API changes.
Confirm that the membership creation time, global application role, and second project membership remain unchanged.

Use a project deployer, viewer, and user outside the project to repeat denied attempts.
Confirm that role controls remain hidden and forged API or web requests cannot change the role.
Use the printed viewer account from the retained checks.
Keep all test accounts and project memberships for repeat checks.

Run `bash scripts/go_test.sh -run '^TestProjectMemberRolesAPIWebE2E$' ./internal/handler/api` for API and web contracts.
Run `bash scripts/go_test.sh -tags=e2e,packagebrowser -run '^TestProjectMemberRolesBrowserE2E$' ./internal/handler/api` for rendered browser coverage.

## Software build identity

Run `durpdeploy version`, `durpdeploy --version`, and `durpdeploy -v` without runtime configuration.
Confirm that each command exits successfully with the same version, build number, and source commit.
Call `GET /api/v1/version` with a bearer token.
Confirm that the response matches the executable identity.
Open **Version** in the account menu as an administrator and a viewer.
Confirm that both users can read the same build information at `/settings/version`.
Repeat at a mobile viewport width.

Open the Agents list and an agent detail page.
Confirm that the reported software version matches the fleet API.
For an agent without a reported version, confirm that the page shows **Not reported**.
Keep protocol identifiers separate from software versions.
Retain the agents and accounts for repeat checks.

Run `bash scripts/build_version_test.sh` to verify tagged and development executable output.
Run `bash scripts/go_test.sh -run '^TestBuildInfoAPIWebE2E$' ./internal/handler/api` for API and web permissions.
Run `bash scripts/go_test.sh -tags=e2e,agentbrowser -run '^TestBuildInfoBrowserE2E$' ./internal/handler/api` for rendered browser coverage.

## Manual deployment step panels

Open running, waiting, failed, and completed deployments.
Confirm that each step panel starts collapsed.
Confirm that step names, status badges, and the current-step summary remain visible.
Select a step heading to expand its panel.
Select the heading again to collapse its panel.
Repeat with a keyboard and a phone.

Keep one panel open and another panel closed while live logs arrive.
Confirm that each panel keeps your selection through step changes, failure, and completion.
Open the closed panel and confirm that it shows retained live output.
Reload the page and confirm that each step panel starts collapsed again.
Manual selections apply while the current page remains open.

Retain the deployments and releases for repeat checks.
Run `TestDeploymentStepPanelsBrowserE2E` for automated browser coverage.
Run `TestDeploymentStepLogsAPIWebE2E` and `TestDeploymentStepLogOutcomesAPIWebE2E` for the existing API contracts.

## Step execution locations

Create a release with a server container step, an agent host step, and an agent container step.
Assign the agents to the deployment environment.
Use distinct step names and a short delay to inspect live output.
Deploy the release through the public API or web form.

Check the execution location entry before each step's script output.
Confirm that the physical hostname matches the machine that runs the step.
Confirm that server steps identify their container name and image.
Confirm that agent steps identify the selected agent and distinguish `container` from `host/shell`.

Configure one retry.
Repeat the deployment.
Confirm that each attempt has its own location entry and container name.
Assign two matching agents to one remote step.
Confirm that the fan-out logs identify both agents and execution locations.

Reload the deployment page after completion.
Read `GET /api/v1/deployments/{id}/logs`.
Download `/deployments/{id}/logs.txt`.
Confirm that both surfaces retain the same execution locations.
Repeat the check with a viewer account.

For a containerized host agent without runtime information, set `DURPDEPLOY_AGENT_HOSTNAME` before starting the agent.
Confirm that the configured physical hostname appears in its logs.
Without this setting, an unavailable physical hostname appears as `unknown`.

Retain the project, release, environment, deployments, and agent assignments for repeat checks.
Keep schedules disabled.
Automated API and web coverage uses `TestExecutionLocationAPIWebE2E`.
Rendered browser coverage uses `TestDeploymentStepLogsBrowserE2E`.

```bash
AGENT_TEST_SOCKET=unix:///run/user/1000/podman/podman.sock DURPDEPLOY_AGENT_E2E_BINARY=/absolute/path/to/durpdeploy-agent bash scripts/go_test.sh -tags 'e2e agentstaging' -race -count=1 -timeout=5m -run '^TestExecutionLocationAPIWebE2E$' ./internal/agentserver
```

## Deployment staging transfer

Run the transfer timeout API checks with the current server and agent builds:

```bash
AGENT_TEST_SOCKET=unix:///run/user/1000/podman/podman.sock DURPDEPLOY_AGENT_E2E_BINARY=/absolute/path/to/durpdeploy-agent bash scripts/go_test.sh -tags 'e2e agentstaging' -count=1 -timeout=5m -v -run '^TestDeploymentStagingTransferPausesStepTimeoutAPIE2E$' ./internal/agentserver
```

The download and upload checks delay transfers beyond the three-second step timeout.
Both deployments must succeed. The slow script must fail. All three checks must confirm staging cleanup.

Build both the server and a staging-capable agent.
Assign two agents to the test environment.
Give each agent a distinct selector label.
Create a deployment with a server container step that writes a file under `DURPDEPLOY_STAGE_DIR`.
Add an agent host step that reads this file and writes another file.
Add a container step on the second agent that reads both files.
Add a server container step that reads the final staging files.
Deploy the release and confirm that every step succeeds.

Create a parallel agent step that changes the same file differently on each agent.
Deploy this release and confirm that the deployment fails with `conflicting staging changes` in its logs.
Repeat with identical changes and confirm that the deployment succeeds.
Repeat with different file names and confirm that the next step receives both files.

After success, failure, or cancellation, request `GET /api/v1/deployments/{id}/staging`.
Confirm that the endpoint returns `[]`.
Request `GET /api/v1/admin/audit?action=publish_staging` with an administrator token.
Read `details.String` as JSON.
Check its `agent_id` for each agent that published staging.
Confirm that agent staging directories and deployment staging volumes are empty.
Start another deployment and confirm that its staging directory contains no previous files.

Use the isolated API suite in `docs/deploy.md` to repeat these checks automatically.

Add a host step that starts a detached writer with `setsid` and then exits.
Log the detached child's process ID before the step exits.
Deploy the release and confirm that the detached process stops before the next step starts.
Repeat with a double-forked child and with deployment cancellation.
Confirm that all logged child process IDs are absent after completion.

Add a host step that moves its workspace outside the staging root.
Confirm that publication fails and the moved directory contains no files.
Repeat with a step that moves the entire staging root.
If supervisor confirmation fails, restart the agent through its service manager.
Confirm that restart cleanup purges moved staging directories before it sends the saved failed result.

## Releases with a deferred package pull

Repeat the staging transfer checks with an HTTPS ZIP repository attached to the project.
Read the package through `ARTIFACT_PATH` in each server and agent job.
Copy its files into `DURPDEPLOY_STAGE_DIR` before modifying them.
Confirm that agent host jobs and agent container jobs receive the same pinned package.
Confirm that containers reject writes to `/artifacts`.
Confirm that later jobs receive staging outputs from each execution location.

Check cleanup after success, failure, cancellation, and conflicting parallel changes.
Replace the upstream ZIP after pinning and confirm that the deployment fails.

Run the package transfer API checks against the current agent binary:

```bash
AGENT_TEST_SOCKET=unix:///run/user/1000/podman/podman.sock DURPDEPLOY_AGENT_E2E_BINARY=/absolute/path/to/durpdeploy-agent bash scripts/go_test.sh -tags 'e2e agentstaging' -count=1 -timeout=5m -v -run '^TestDeploymentStagingPackageAPIE2E$' ./internal/agentserver
```

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


## Bounded log history

Startup indexes step metadata and backfills existing log history once.
Allow extra startup time for databases with large log histories.

Create a disposable project with a Bash container step.
Use this script to produce more than two pages of output:

```bash
for i in $(seq 1 1200); do
    printf 'history-%04d\n' "$i"
done
```

Deploy the release and open its deployment page.
Expand the step panel.
Confirm that the page shows at most 500 recent entries.
Confirm that the viewer retains at most 1 MiB of previews.
Select **Older logs** and confirm that earlier entries appear.
Confirm that the page states that live updates are paused.
Confirm that completed step states remain visible on older pages.
Select **Latest logs** to return to recent output.
Select **Export all logs** and confirm that both `history-0001` and `history-1200` appear.
Repeat these checks with a runbook containing the same step.
Repeat navigation with the keyboard and a narrow browser window.

Request `GET /api/v1/deployments/{id}/logs/page`.
Confirm that `items` contains at most 500 entries with increasing IDs.
Confirm that the JSON response contains at most 1 MiB, including its envelope.
Check `byte_limit` and `line_byte_limit` for the documented limits.
Pass `next_before` as `?before=ID` to read an older page.
Confirm that pages do not overlap.
Repeat with `/api/v1/projects/{id}/runbook-executions/{executionId}/logs/page`.
Confirm that the existing `/logs` endpoint still returns complete history.
Use a nonmember account and confirm that page and export requests return `403`.

Repeat the deployment and runbook checks with oversized lines:

```bash
line=$(printf '%020000d' 0)
for i in $(seq 1 180); do
    printf 'long-%03d:%s:complete-tail-%03d\n' "$i" "$line" "$i"
done
```

Confirm that oversized lines show a truncation marker.
Confirm that the viewer can retain fewer than 500 entries because of the byte budget.
Check that previews contain at most 16 KiB per text field, including their markers.
Confirm that shortened API entries contain `truncated: true`.
Page through the history and confirm that each persisted ID appears once.
Export all logs and confirm that every `complete-tail-*` marker remains present.
Use `format=structured&preview=true` to request bounded live previews through the public API.
Omit `preview` and confirm that the full stream preserves complete lines.

Retain the project, release, runbook, and execution history.
Keep schedules disabled.
Automated checks include `TestDeploymentLogHistoryPagesAPIWebE2E` and `TestRunbookLogHistoryPagesAPIWebE2E`.
Browser checks include `TestDeploymentLogHistoryBrowserE2E` and `TestRunbookLogHistoryBrowserE2E`.
`TestOpenLogStreamRevocationAPIWebE2E` checks open streams after session or token revocation.
`TestRunbookLogRevocationBrowserE2E` checks browser access after project membership removal.

### Runbook deletion

Use disposable runbooks for deletion checks.
Retain the corresponding project, completed executions, and logs.
Retain a second runbook with the same steps for repeat checks.
Keep schedules disabled after testing.

1. Create a runbook with two saved versions.
2. Execute the first version and wait for completion.
3. Create a schedule for the runbook.
4. Open the runbook detail page and select **Delete runbook**.
5. Check the runbook identity and deletion effects in the confirmation.
6. Cancel confirmation and check that the runbook remains available.
7. Repeat confirmation and accept deletion.
8. Check that the runbook leaves the active list.
9. Open the retained execution and check its original logs.
10. Check that Retry is absent.

Repeat deletion with `DELETE /api/v1/projects/{id}/runbooks/{runbookId}` and an empty body.
Expect `204` without a response body.
Expect `404` for repeated deletion and inaccessible runbooks.
Expect `409` while executions or cleanup remain unfinished.
Expect `403` for viewer API writes.
Check that viewer pages omit the Delete action.
Check that web writes without valid CSRF tokens fail.

Deletion retains saved versions, backing releases, artifact pins, completed executions, and logs.
Deletion disables schedules and prevents future execution, retry, version saving, and schedule creation.
Deleted names remain reserved within the project.
Existing project and environment deletion policies still apply to retained history.

Automated checks include `TestRunbookDeletionAPIWebE2E` and `TestRunbookDeletionBrowserE2E`.

## Server step resource policy

Server-side deployment steps have no DurpDeploy-imposed RAM ceiling or CPU
quota. PID limits, temporary-filesystem bounds, and other isolation remain.
Helper/infrastructure containers and remote agents retain their existing
resource policy; the agent follow-up is
[issue #14](https://github.com/DeveloperDurp/durpdeploy-agent/issues/14).

For a manual check on a running instance, create a disposable project through
`POST /api/v1/projects`, add a local Bash step using
`harbor.durp.info/dockerhub/library/bash:5.2` through `POST /api/v1/projects/$PID/steps`, and use
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

## Phase 1 regression checks

Create two projects with different members.
Sign in as each member and inspect `/` and `/dashboard/deployments`.
Verify every deployment section and count excludes the other project.
Sign in as an administrator and verify both projects remain visible.

Open deployment and runbook log streams with browser sessions and API tokens.
Remove the stream user's project membership through the API.
Verify responsive streams close within approximately two seconds.
Verify reconnect requests fail and new output does not appear.
A stalled consumer can delay closure up to 62 seconds.

Configure project and global notification credentials through their API endpoints.
Inspect GET and PUT responses and both settings pages.
Verify credential fields remain empty and configured indicators remain visible.
Save blank fields to preserve credentials.
Enter replacements to update credentials.
Use clear flags or **Clear credential** controls to remove credentials.
Verify delivery history does not expose provider credential URLs or tokens.

On each settings page, enter a Gotify URL without `http://` or `https://`.
Select **Save**.
Verify an error toast appears and all entered values remain.
Correct the URL.
Select **Save**.
Verify the **Notification settings saved** toast appears.

Enter an invalid Slack or Discord webhook URL.
Select its **Clear credential** control.
Select **Save**.
Verify the credential shows **not configured**.

Deploy a trusted disposable script that exceeds one MiB of output without a newline.
Verify the attempt fails, reports its pending-output limit, and removes its container.
Verify raw pending output does not appear in stored or streamed logs.

Keep retained schedule examples disabled after checks.
Enable a disposable schedule through its existing toggle control to repeat scheduling checks.

## List and authentication checks

Schedules show 50 entries per page.
Create 51 disabled schedules in a disposable project.
Open the second page with **Next**.
Select **New Schedule**.
Select **Cancel**.
Confirm that the second page remains selected.
Edit one schedule and select **Save**.
Confirm that the second page remains selected.
Toggle the schedule twice to leave it disabled.
Delete the last entry on the last page.
Confirm that the list shows the preceding page.
Compare the list with `GET /api/v1/projects/{id}/schedules?limit=50&offset=50`.

Home deployment panels refresh every five seconds.
Home charts refresh every 30 seconds.
Open browser network tools and filter requests by `/dashboard/activity`.
Confirm that deployment panel refreshes do not send extra chart requests.
Open **Projects** and confirm that chart requests stop.
Return to Home and confirm that chart requests resume.

Passkey names use a limit of 255 UTF-16 code units after whitespace trimming.
Most characters use one unit.
Some symbols, including many emoji, use two units.
Open **Security** and enter a passkey name.
Confirm that the field limits input to 255 units.
Submit an oversized name through the session-authenticated registration or rename endpoint.
Confirm that the response is `422` and the credential remains unchanged.

Open navigation at phone, tablet, and desktop widths.
Check **Security**, role-dependent **Tokens**, theme switching, and **Logout**.
Open a lifecycle with multiple stages.
Move a stage and confirm that keyboard focus remains usable.
Confirm that boundary stages show only their available move controls.
Use a viewer account and confirm that write controls remain hidden.

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
proxy image `harbor.durp.info/dockerhub/library/caddy:2-alpine`.

### Loopback-only demo

Start the rootless Podman socket before launch.
Run `DEMO_LOCAL_ONLY=1 DURPDEPLOY_CONTAINER_RUNTIME=podman make demo` for access from this computer only.
The HTTPS URL uses `https://127.0.0.1:<random-port>`.
The HTTPS proxy, HTTP backend, and agent listeners bind to loopback.
The certificate includes the `127.0.0.1` IP address and `localhost` name.
The launcher skips the firewall watcher, even when its helper is installed.
Docker and rootful Podman are unsupported in this mode.
Set `DEMO_LOCAL_ONLY` to `0` or `1`.
Server-only refresh preserves the listener addresses and URL.
For full refresh, use `DEMO_LOCAL_ONLY=1 DURPDEPLOY_CONTAINER_RUNTIME=podman make demo-refresh-full DEMO_DIR=/absolute/demo/path`.

### Optional firewall access

The helper trusts the authorized local operator to select ports.
This scope intentionally permits any TCP port from 32768 through 60999.
The helper does not maintain an administrator-approved list of demo ports.
Grant this permission only to operators trusted to expose services in that range.
Leases expire after 120 seconds without renewal.

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

The variable-scope browser checks run in isolated CI fixtures.
The populated demo does not run these checks or create their `variable-before-deploy-*` projects.

For manual variable-scope checks, create a disposable project with zero deployments.
Test direct links and the project's Variables menu.
For lifecycle-bound projects, select a stage environment.
Create `first-scoped-release` after you set the variable scopes.
Inspect the release through `GET /projects/{id}/releases/{release_id}`.

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
| Pairing recovery during database contention | Run `bash scripts/go_test.sh -race -run 'TestPrepareAgentPairingRecovery|TestAgentPairingRecoveryContentionAPIWebE2E' ./internal/repository ./internal/agentserver`. Repeat the disposable agent rejoin check above. | Temporary SQLite write contention retries within the existing limit. API pairing returns 201, and web confirmation redirects to the recovered agent. Cancellation preserves the previous pairing. Invalid codes and identity conflicts remain rejected. |
| Login, sessions, and CSRF (F0) | Sign out and request `/` without following redirects; sign in with the E2E admin. Submit POST `/projects` with the session cookie but omit the CSRF token (edit the request in developer tools). | Anonymous GET redirects to login (303); login redirects to the app; protected pages load; missing CSRF rejected (403). |
| Happy path, steps page, scoped variables (F3.1) | `TestProject`, release `1.0.0`, environment `dev`. Deploy it and open Steps and live logs. | Success; logs contain `default-variable=hello`. |
| Variable scopes before deployment (#159) | Create a project with zero deployments. Add a global variable named `DEFAULT`. Create another variable with an environment scope. Edit that variable's environment scope. Select Override on `DEFAULT` to create a scoped override. Repeat on desktop and phone. | Eligible scopes are selectable in all three forms. The API accepts eligible scopes before deployment. For lifecycle-bound projects, the API rejects environments outside the lifecycle. The first release retains scoped values. Later variable edits do not change the release snapshot. |
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
| Audit identities (A08) | Open `/admin/audit` as an admin at 390 and 1280 pixels. Filter by an exact current name or uppercase email. Combine the filter with User ID. Follow an existing user or project target. Inspect a deleted target. Request `/api/v1/admin/audit?user=EMAIL` with an administrator token. | Actor names, emails, and IDs remain visible. Target links open existing records. Deleted targets retain IDs without links. Filters preserve values. The API includes `actor_name`, `actor_email`, and `target_url`. Non-admin access remains forbidden. |
| Ambiguous audit targets (A08) | Create a release in a disposable project. Inspect its `create_release` entry in `/admin/audit` and `/api/v1/admin/audit?action=create_release`. | The entry retains its recorded ID without a target link. Older creation events can contain enclosing project IDs. Release refresh, deployment creation, runbook creation, and runbook execution also suppress ambiguous links. |
| Users on phones (A04) | Open `/admin/users` as an admin. Set the viewport to 390 pixels. Use a disposable account with a long email and name. Open Reset MFA. Check the target email. Select Cancel. | Full identities wrap. Roles and actions do not overlap. Each action stays with its account. Edit opens the correct account. Self-reset and self-delete are absent. |
| New user role (A05) | Open `/admin/users/new` as an admin. Create a disposable user without changing Role. Check the account through `/api/v1/admin/users`. Repeat with Admin selected. Submit mismatched passwords before correcting them. | The initial role is Viewer. The API confirms the saved role. Admin requires an explicit selection. Validation preserves that selection. Role explanations remain visible. Existing edit forms retain their roles. |
| User form accessibility (A06) | Open `/admin/users/new` as an admin at 390 and 1280 pixels. Click Email, then press Tab. Inspect control names in the accessibility tree. Submit mismatched passwords. Correct the passwords and create the account. Repeat validation from its Edit page. Check the saved account through `/api/v1/admin/users`. | Every control has an associated label and accessible name. Email receives focus when its label is clicked. Tab moves to Name. The error summary has the alert role and receives focus. Rejected forms retain non-secret values and clear passwords. Corrected submissions save successfully. Rejected edits do not change the account. |
| Permission guidance (A07) | Open a project as a global Deployer with project Admin membership. Inspect its overview, Steps, Releases, project menu, and account menu. Repeat as a global Maintainer with project Viewer membership. Follow Project member settings. Compare `/api/v1/users/me` and `/api/v1/projects/{id}/members`. Try a rejected configuration write. | The effective role follows the lower role. Missing actions have explanations. Global limits point to Users. Project limits point to member settings. Restricted members can read the roster but cannot change roles. API roles agree with the overview. Rejected writes do not persist. |
| Token, health, project/environment/step/release API (A1–6) | Mint a token. GET `/healthz` with and without it. Inspect `e2e-api-project`, environment `dev`, `long-step`, and release `v1` through list/detail endpoints. Create disposable counterparts through POST. | Health 200; create returns IDs; lists and details show matching names. |
| File handoff and retry | Deploy `stage-handoff` release `1` through the web and API. Inspect its producer/consumer scripts and logs. | Both succeed; logs show retry and `stage-handoff-content-and-size-ok`; each deployment starts with fresh staging data. |
| Reserved staging variable validation | In `stage-handoff`, create or rename a variable to `DURPDEPLOY_STAGE_DIR`; try selecting it in a new step's variable list through web and API. | Variable writes rejected (422); step rejected (web 422, API 400). Existing `LIMITED` variable is retained for rename checks. |
| Interpreter forms, containers, variables | `e2e-interpreters`: inspect new/edit step forms, current PowerShell step, releases `mixed-v1` and `powershell-immutable`, and deployment pages. Deploy both releases. | Choices Bash/Python/PowerShell; current interpreter selected; mixed logs show `bash-e2e` and `python-e2e=container-value`; PowerShell logs show `powershell-e2e=container-value` without prompts or escape sequences. |
| Interpreter validation and release refresh | POST a step with `interpreter: "/bin/sh"` (400). Edit the Python step to PowerShell, then refresh the used `mixed-v1` release through the API (200) or its web Refresh button (303). Inspect the release and original deployment, then re-run the original mixed deployment. | Invalid interpreter rejected; refreshed release shows PowerShell; original deployment and its rerun still execute Python. API alias `powershell` normalizes to `pwsh`. Refresh is available after failed or successful deployments. Active or unconfirmed runs and buffered agent logs return 409 until they finish. |
| Template forms, history, save/insert | Open `python-template` in Templates and its History; inspect Python and PowerShell versions. In interpreter project use Save Template and insert an existing template. | Form selections/history preserve interpreters; save/insert preserves `pwsh`; API template GET/PUT/history agree with the web. |
| Agent container placement (#99) | In a disposable project, add a step with Run step on = Agent, Agent execution mode = Container, image `harbor.durp.info/dockerhub/library/alpine:3.20`, and variable limit `DEPLOY_ENV`. Save a template and a release. Change the mutable step to Host, then inspect the template history and the original release through the API. Repeat in a runbook with one container and one host step. | Host is the default; container mode requires an image. Modes, images, and limits survive create/edit and immutable snapshots. Agent-host steps reject images; server-container steps reject agent container mode. |
| Agent/3 execution and cleanup (#99) | Assign a ready agent/3 agent to an environment and deploy the retained container release. Inspect `/admin/agents/{agent-id}` and deployment logs. If the agent reports `cleanup_unconfirmed`, restore its same local engine and let it reconcile before polling again. | Health shows modes, runtimes, and container interpreters. Bash, Python, and PowerShell use their selected images and limits. Unconfirmed cleanup blocks queued work and rerun; readiness confirms cleanup separately and preserves the original result. Agent/1 and agent/2 cannot claim container steps or confirm cleanup. |
| API cancel/status (A7) | POST a deployment of e2e-api-project release `v1`, GET its status, POST `/deployments/{id}/cancel` while running. | Running → cancelled; cancel response 200. |
| API log streaming (A8) | Deploy e2e-api-project `v2`; GET `/deployments/{id}/logs/stream?format=ndjson`. | Each replayed/live line is a JSON object with a `line` field. |
| Project/environment queue isolation | Deploy e2e-api-project `queue-check` to dev, then deploy `v2` to dev. While the first runs, deploy TestProject `1.0.0` to dev and e2e-api-project `queue-check` to test. Cancel both slow deployments after inspection. | Only the second dev job in e2e-api-project queues. The other project and test job run independently. Cancelling the dev head admits its next queued job. Release snapshots and printed deployment links remain available. |
| API failure paths (A9) | POST `/projects` without a token. GET a nonexistent project ID. Sign in as the printed `e2e-viewer-…@test.local` with password `e2e-viewer-pass-1234`. To repeat the API viewer check, promote that user to Maintainer. Mint its token. Demote the user to Viewer before POST `/projects`. | No token returns 401. A missing project returns 404. A Viewer write returns 403. Admin changes invalidate sessions. Sign in again for web checks. |
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
# SMTP delivery warnings

Configure the server to use a disposable SMTP relay without STARTTLS.
Clear `DURPDEPLOY_SMTP_USER` for this unauthenticated relay.
Set the project's email recipients in **Settings → Notifications**.
Run a deployment that publishes a notification.
Open `/admin/notifications` as an administrator.
Expect `email: ok: sent without TLS` with a warning badge.
Open the event details and confirm the same warning.
Request `GET /api/v1/admin/notifications` with an administrator API token.
Expect the email result to contain `ok: sent without TLS`.
If the relay uses verified STARTTLS, expect `ok` instead.
If TLS certificate verification fails, expect `failed: delivery failed` without a plaintext retry.

# Shared lifecycle variables

On a release with a deployment awaiting approval, click **Refresh** and accept
the confirmation. The release page must remain open and show **Unable to
refresh this release**, with the snapshot lock reason, cancellation/completion,
cleanup and log sync guidance, and a **View project deployments** link.
The API refresh must return `409` while that deployment is active. Cancel a
disposable deployment or let it complete, then retry: the API returns `200`
and the web refresh returns to the release page with the error cleared.
The retained demo has a disposable pending-approval case at
`/projects/4/releases/9`, with `/deployments/25` awaiting approval; check its
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
Global administrators manage shared values.
Project Maintainers can override their own values.
Viewers and Deployers see no write controls.
Unassigned projects inherit nothing.

The refreshed demo is `https://citadel.durp.loc:45465`, with data in
`tmp/demo.zU6HfZ` and its temporary certificate at `tls/cert.pem`.
Credentials are in that directory's `login.txt`. Stop only this demo with
`make demo-stop DEMO_DIR=/absolute/path/to/tmp/demo.zU6HfZ`.
Previous `tmp/demo.ugv3mx` and `tmp/demo.pX7hhv` histories are retained on disk.

The hands-on scope-change example is lifecycle
[`Live scope manual`](https://citadel.durp.loc:45465/lifecycles/6), project
[`Live scope manual`](https://citadel.durp.loc:45465/projects/24), and release
[`before-scope-edit`](https://citadel.durp.loc:45465/projects/24/releases/36).
Deployment [42](https://citadel.durp.loc:45465/deployments/42) prints
`LIVE=all-stages` before the shared edit. The same release then prints
`LIVE=unset` in prod in deployment
[43](https://citadel.durp.loc:45465/deployments/43), and `LIVE=dev-only` in dev in
deployment [44](https://citadel.durp.loc:45465/deployments/44).
The TOKEN output is redacted. Repeat by editing LIVE on the lifecycle page and
deploying release 36 to Live scope prod or Live scope dev, without refreshing it.
These examples create no schedules; all retained schedules remain disabled.

Both retained `shared-v1` releases contain only project-owned variables. Their
successful deployments use `shared-updated`, which the suite set after creating
the releases. Change a shared variable from All stages to dev, then deploy the
same release to another stage: the variable must be absent there. Deploy to dev:
its current value must appear. No release refresh is needed. Re-runs, rollbacks
and saved runbook versions also resolve current lifecycle variables when creating
a new execution. Queued and active executions retain their captured values.
Inspect the logs: the shared secret is redacted. To check
environment precedence, add a lifecycle REGION override for the stage; it wins
over an unscoped project override, while a project override for that same stage
wins over the lifecycle value. These examples have no enabled schedules.
Lifecycle assignment grants execution access to shared secrets.
Only global administrators can attach a lifecycle to a project.
Project administrators with global Maintainer permission can retain or remove an existing assignment.
As a Maintainer, create or update a project with a new lifecycle ID.
Confirm a 403 response without a project change.

## Database TLS policy

Use a disposable database when changing TLS settings for this check.
Keep the matching application encryption key.
See [connection settings](deploy.md#database-tls-and-permitted-exceptions) for PostgreSQL and SQL Server options.

1. Configure verified TLS with the trusted CA and matching database hostname.
2. Start the application.
3. Call `GET /api/v1/admin/database-security` with an administrator bearer token.
4. Expect `policy: tls_verified` without a `warning` field.
5. Create a disposable project through the API.
6. Confirm that the project appears in the project list.
7. Stop the disposable application.
8. Configure an incorrect CA or hostname.
9. Start the application and expect certificate validation to fail.
10. Restore the correct settings before continuing.

To check the supported exceptions, repeat startup with unverified TLS or plaintext settings.
Expect a `database transport security warning` in the startup log.
Expect `tls_unverified` or `plaintext_allowed` with a `warning` in the administrator API response.
The application must still permit the disposable project operation.
The warning must omit credentials, database addresses, and certificate paths.
The API reports configured policy, not the encryption of each live connection.
The public health endpoints must not expose this information.
Anonymous API access returns `401`.
Viewer and deployer tokens return `403`.

Run the isolated native checks with the local runtime socket:

```bash
DOCKER_HOST=unix:///run/user/1000/podman/podman.sock \
  DURPDEPLOY_TLS_BACKEND=postgres make database-tls-test
DOCKER_HOST=unix:///run/user/1000/podman/podman.sock \
  DURPDEPLOY_TLS_BACKEND=mssql make database-tls-test
```

These isolated checks remove their disposable databases and certificates.
They do not modify retained demo projects or enable schedules.
