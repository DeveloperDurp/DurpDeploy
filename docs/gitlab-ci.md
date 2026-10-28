# GitLab CI

The pipeline runs on the DMZ Kubernetes executor with one privileged pod for each job.
The runner supports Linux AMD64 and accepts untagged jobs.
The runner manager limits concurrency to ten jobs.
ARM64 build and scan coverage is removed.

## Pipeline events

The `dev-stacks` merge request job runs the two-worktree HTTPS regression with the scanned app image.
It checks API and browser access, namespace isolation, teardown, and startup failure cleanup.
It invokes the T3 settle hook and checks worktree ownership, retry, and retained fixture isolation.
It retains connection records and browser screenshots in `artifacts/dev-stacks`.
Local verification uses the same regression with rootless Podman.

Merge requests run lint, Go tests, API tests, browser tests, database parity, and security checks.
Main pushes, tags, manual pipelines, and scheduled pipelines run the shared build and security checks.
Protected main pushes, protected tags, and protected manual pipelines can publish verified images and charts.
Scheduled pipelines never publish.
Feature branch pushes do not create duplicate pipelines.
Superseded merge request jobs can be canceled.
Publication jobs complete without automatic cancellation.

## Job image and container runtimes

The `ci-runtime` job builds `Dockerfile.mobile-browser` and `Dockerfile.ci` with a pinned Docker-in-Docker service.
The service listens on the job pod loopback interface.
It does not expose a node socket.
The job stores its helper image at `$CI_REGISTRY_IMAGE/ci:pipeline-$CI_PIPELINE_ID`.
It stores the intermediate browser image at `$CI_REGISTRY_IMAGE/ci-browser:pipeline-$CI_PIPELINE_ID`.
The helper build uses the pushed browser digest.
It passes the pushed digest through a dotenv report.
Other jobs pull that exact digest to prevent helper tag replacement.
This helper image supplies existing Go, Playwright, Docker, Podman, Helm, and scanner prerequisites.
The helper image preloads dependencies from `go.mod` and `go.sum` into `/opt/go-mod`.
It retains the Go tool build cache in `/opt/go-build`.
The `go-test-cache` job compiles shared browser, race, and coverage API test variants without executing tests.
It exports new Go build objects in an archive limited to 512 MiB.
It excludes dependency objects already present in the helper image.
Test jobs download these objects through the shared GitLab cache.
Each test job restores these objects into its private `/opt/go-build` directory.
All test commands still execute their selected tests with `-count=1`.
The producer restores and uploads a GitLab cache scoped to the project and merge request.
Consumers use the same cache key with `policy: pull` after the producer finishes.
Consumers do not upload the cache or download a separate build-cache artifact.
Protected pipelines do not import merge request caches.
Persistent GitLab caches require the separate runner S3 configuration and a one-day bucket lifecycle policy.
If the cache is unavailable, each job builds from the helper image cache.
The lint job keeps a separate `golangci-lint` analysis cache and does not wait for the Go cache producer.
Its key includes the project, source project, merge request, branch, protection status, Go toolchain, linter installer, and linter configuration.
Only successful lint jobs upload this cache.
The same one-day bucket lifecycle applies.
Same-project merge requests reuse separate browser and helper build caches under `ci-cache/mr-<number>`.
Registry caches export final-image layers with `mode=min`.
Intermediate build stages are rebuilt when their output is absent from the cache.
Merge request jobs never write shared protected caches.
Protected and other pipelines do not import merge request caches.
Builds check upstream base references before using cached layers.
The helper image also builds the pinned security scanners and downloads checksum-verified lint and Trivy binaries.
The existing registry cache retains that tool layer and its license notices.
Job installers check the tool versions before reuse and retain their local installation fallback.
Scanner jobs still analyze the current commit and fetch current vulnerability data.
Build timing evidence records browser and helper build durations.

Product images are published to both the GitLab registry and GHCR.
Application images use `$CI_REGISTRY_IMAGE` and `ghcr.io/developerdurp/durpdeploy`.
Helm charts are published only to `$CI_REGISTRY_IMAGE/charts/durpdeploy`.
The image publisher verifies the same scanned image digests in both registries.
The chart publisher uploads one package and compares its downloaded bytes from the GitLab registry.

The disposable CI images use root to prepare checkout ownership and test fixtures.
The helper also starts its private Docker daemon and switches to the rootless Podman user.
SonarCloud rule `docker:S6471` has an exception for these two Dockerfiles only.
The browser image follows the reviewed policy in [SonarCloud issue #112](sonar-issue-112.md).
Other security rules and quality gate thresholds remain active.
The published application image uses `USER 10001:10001`.
The browser image installs templ from `go.mod` and `go.sum` with `-mod=readonly`.
It disables npm lifecycle scripts with `npm ci --ignore-scripts`.

The application builder and CI tools use Node.js 24.22.1 from digest-pinned official images.
The browser image removes its older Node package before copying the patched distribution.
The copied distribution retains Node and npm dependency license notices.
The independent cache cleanup job uses the same pinned Node version.

Repository image pulls use these Harbor proxy projects:

| Upstream registry | Harbor project |
| --- | --- |
| Docker Hub | `harbor.durp.info/dockerhub` |
| Forgejo | `harbor.durp.info/forgejo` |
| GHCR | `harbor.durp.info/github` |
| GitLab.com | `harbor.durp.info/gitlab` |
| Kubernetes | `harbor.durp.info/k8s` |
| Microsoft | `harbor.durp.info/microsoft` |
| Quay | `harbor.durp.info/quay` |

Docker Hub official images retain the `library/` namespace after the Harbor project.
Existing image tags and digest pins remain unchanged.
Isolated E2E setup pulls interpreter images before deployment execution deadlines start.
Package browser shards use Chromium already installed in the helper image.
Standalone container browser checks pull Playwright before browser startup deadlines start.
Testcontainers uses the Docker Hub proxy for its auxiliary images.
Trivy downloads its databases and check bundle through the GHCR proxy.
Compose builds the application image locally.
Helm and development defaults pull published application and agent images through the GHCR proxy.
Publication checks access GHCR directly to verify current tags before pushing.
The pipeline helper image requires a writable GitLab registry.
Harbor proxy projects accept pulls and do not accept helper-image pushes.
The agent dependency owns additional image references outside this repository.
The CI agent build redirects its Dockerfile base images through Harbor without changing the dependency.

Other jobs start a private Docker daemon inside their job container.
The daemon and test process use the same filesystem paths.
The daemon stores image layers on the Kubernetes build volume.
Use an ephemeral build volume for private Docker data.
The setup preserves the job temporary directory on disk.
This arrangement supports bind mounts, agent socket mounts, and loopback published ports.
The pod removes the private daemon and its containers when the job ends.
The Go test runner also removes containers owned by each test session.

The Podman job runs as `pwuser` with subordinate user and group mappings.
It uses VFS storage and starts `podman system service` without systemd.
The CI helper delegates CPU, memory, and PID controllers inside the current Kubernetes build-container cgroup.
It configures that subtree as the explicit rootless Podman parent.
The helper refuses host roots, pod parents, missing controllers, and reused setup paths.
The existing Kubernetes CPU and memory limits still apply to all descendants.
The job pod teardown removes its cgroup subtree.
The staging capacity test allows ten minutes for transfer and cleanup through VFS.
Its Go suite allows twenty minutes within the forty-five-minute job limit.
It verifies rootless execution before it runs the agent tests.
The job temporarily allows exit code `77` when `user.max_user_namespaces=0`.
Other Podman failures fail the pipeline.
Remove `allow_failure:exit_codes` after runner nodes enable user namespaces.
Rootless Podman requires enabled user namespaces on the runner nodes.
Ask the runner administrator to enable user namespaces if the job reports this prerequisite failure.

Keep `runUntagged` enabled for this runner.
Keep the existing privileged Kubernetes job setting for the nested runtimes.
Use the scheduled CI registry cleanup for old helper images, browser images, and closed merge request caches.

### Scheduled CI registry cleanup

The `cache-cleanup` job covers this project's `ci`, `ci-browser`, and exact `ci-cache/mr-<number>` registry paths.
It preserves open merge requests and merge requests with active head pipelines.
It waits 24 hours after a merge request closes or merges.
Before deletion, it checks the merge request state again.

For `ci` and `ci-browser`, it deletes only `pipeline-<number>` tags.
It waits 24 hours after the corresponding pipeline finishes.
It preserves active pipelines and the latest pipeline of each open merge request.
Before deleting a tag, it checks the pipeline state again.
It preserves unrelated repositories, release tags, and `latest` tags.

GitLab deletes registry repositories asynchronously.
The report records deletion requests, not completed storage reclamation.

The job uses `CI_JOB_TOKEN` for registry and merge request API requests.
It does not require a separate cleanup token.
Merge request pipelines run cleanup unit tests once in `pre-commit`.
Cleanup schedules run these tests before registry inventory or deletion.
Merge requests run a read-only inventory only when the cleanup script, tests, or job configuration changes.
Reports are available in `artifacts/cache-cleanup/report.json` for one day.

After merging, create a daily pipeline schedule targeting protected `main`.
Set the schedule variable `CI_CACHE_CLEANUP` to `true`.
Set `CI_CACHE_CLEANUP_DRY_RUN` to `false` to enable deletion.
For a preview without deletion, set `CI_CACHE_CLEANUP_DRY_RUN` to `true`.
Use a schedule owner with permission to delete this project's registry repositories.
Cleanup schedules run only `cache-cleanup`.
Normal security schedules retain their existing jobs.

The scheduled job also covers old pipeline tags previously handled by the built-in `pipeline-.*` cleanup policy.
If an old pipeline's helper image has expired, create a new pipeline instead of retrying its jobs.
The source scan excludes `.cache/` from application analysis.

## Parallel test groups

The agent image job builds the locked agent once for Docker and rootless Podman checks.
Both runtimes verify the pipeline, producing job, commit, module version, checksum, and artifact hashes before loading that archive.
Each runtime retains its execution contract and API tests.

The CI image precompiles locked dependencies with normal and race instrumentation.
Coverage jobs reuse normal dependency objects, and race jobs reuse race dependency objects.
Go validates reused application objects against its content-addressed build inputs.
Dependency locks, Go version, probe source, and image definitions control cache invalidation.

Each package browser shard shares one Chromium process.
Each test creates a separate browser context and disposes it after its assertions.
Browser contexts isolate cookies and local storage.
Tests that change browser-wide certificate settings retain private Chromium processes.
Standalone browser checks retain their owned-container fallback.
Navigation and permissions preload the image required by private-process browser fixtures before their startup deadlines begin.
The shared browser wrapper removes its owned process group and temporary profile after the shard exits.
The running-server HTTP checks also reuse this wrapper in CI.
Each check keeps its own browser context.
HTTPS checks keep private Chromium processes because certificate overrides affect the entire process.
Screenshot helpers wait for fonts, lazy images, layout, and finite animations within a bounded deadline.
They retain the existing screenshots, viewport sizes, and assertions.

The common API and handler harnesses copy a blank migrated SQLite template into separate on-disk databases.
Each copy uses the real startup connection path and retains independent data and foreign-key enforcement.
Fixture cleanup drains active database connections before it removes database files.
Dedicated migration, persistence, backend, and recovery checks retain their original database setup.
Fixed fixture passwords use one full-strength hash per password and package process.
Each fixture keeps independent users, sessions, CSRF tokens, and API tokens.
Password creation, verification, change, and salt-randomness tests still execute their original operations.

Repository tests share one PostgreSQL server and one SQL Server per package process.
Each case creates an empty database and runs its original migrations.
Case cleanup closes connections before it removes the database.
Package cleanup removes both servers and fails if removal fails.
The shell test runner retains its owned-session cleanup after timeouts and interruption.
Recovery and TLS fixtures keep their dedicated servers.

Fixture preload checks pull an image only when it is absent from the private runtime.
Application image pulls and pull-failure assertions retain their existing behavior.

The E2E and SQLite MFA jobs reuse the scanned application image from their pipeline.
Each job verifies the archive checksum, commit, tree, pipeline, build identity, and producing job before loading the image.
These jobs extract the static server binary for shell E2E and MFA browser checks.
Local checks build from the checkout unless `DURPDEPLOY_E2E_BINARY` selects an existing binary.
Container E2E builds its image unless `DURPDEPLOY_E2E_IMAGE` selects an existing image.
Go tests that run application handlers in-process retain their existing builds and race settings.

The `dev-stacks` job uses the scanned application image and skips the Go cache download and restore.
The `pre-commit` job runs independent release, E2E harness, and cleanup regression checks.
These checks do not wait for the application image build.
Three existing jobs distribute the independent server scenarios without adding jobs or changing runner capacity.
The `e2e` job runs parallel isolation, the disabled-runtime probe, and the existing-server SQLite suite.
The `auth-mfa-sqlite` job also runs the host control-plane scenario.
The `dev-stacks` job also runs the containerized control-plane scenario.
Each scenario keeps its assertions, cleanup checks, and elapsed-time output.
The host and container scenarios both run the runbook browser checks.
Each invocation uses a separate namespace and database.
Interpreter images load before deployment deadlines start.
Run `bash scripts/e2e_server_containers.sh` without arguments to select all server scenarios in an isolated CI job.

The browser job runs five shards: navigation, resources, permissions, execution, and agents.
Each shard retains its test selections and assertions.
Navigation, resources, permissions, and execution each use one Go test process.
These processes retain race detection and isolated browser contexts.
The agents shard retains its separate build-version checks.
The running-server SQLite suite runs in `e2e` instead of repeating in the agents browser shard.
Browser evidence uses a separate directory for each shard.
The rollout job runs four shards: contracts, two artifact groups, and verification.
The full coverage suite owns the twelve ordinary API tests previously repeated without race detection in rollout contracts.
Targeted rollout race checks still run separately.
Artifact groups split the compiled test inventory and require every selected test to pass.
New artifact gate tests enter one group automatically.
GitLab requires all blocking jobs before the pipeline can pass.

Restore cleanup checks retain genuine Go timeouts and separate TERM interruption checks.
The timeout is two minutes for SQLite and four minutes for PostgreSQL and SQL Server.
The fixture requires at least 30 seconds of timeout headroom after workload startup.
Job logs report startup time and remaining timeout headroom.
Cleanup checks still verify owned resources and preserve an unrelated container.

After assertions finish, disposable database fixtures use no graceful shutdown wait during removal.
Cleanup still uses an independent bounded context and fails if container removal fails.
Explicit shutdown, recovery, timeout, and interruption checks keep their existing behavior.
This change does not affect retained running-server examples or application shutdown.

The full Go suite runs once and collects coverage in the same invocation.
It uses six jobs across five package groups: API, handlers, repository, migrations, and core.
Two API jobs split the compiled test inventory without running a test twice.
The split includes examples and fuzz seed tests.
Each coverage job retains the thirty-minute test timeout from main.
The `e2e` tag includes ordinary tests and API E2E tests.
A contract test verifies that this tag includes every ordinary test file.
Targeted race, browser, backend, and stress checks remain separate.
Every Go package belongs to one package group.
New packages enter the core shard unless a specific group includes them.
Each shard uses `-coverpkg=./...` and retains a separate coverage report.
SonarCloud waits for all six jobs and requires all six reports.
The scanner reads the reports together and waits for the same quality gate.

The rootless Podman capacity fixture retains its full 512 MiB payload and 20,000 files.
Its completion wait is ten minutes for VFS copying and staging transfer.
The staging suite timeout is twenty minutes.
Bash execution fixtures use the standard thirty-second verification deadline unless they test timeouts.
These fixtures allow one minute for deployment completion.
Dedicated timeout and cancellation tests retain their existing deadlines.

Security scan jobs do not start a container daemon.
Trivy scans the image archive, configuration, and npm dependencies without running containers.
The image build job retains its private Docker daemon.

### Timing baseline

Pipeline [1386](https://gitlab.durp.info/durfy/apps/durpdeploy/-/pipelines/1386) provides the historical baseline before these changes.
It completed successfully without job retries on October 22, 2026.
Elapsed time from pipeline creation to completion was 36 minutes and 26 seconds.
The four duplicate unit-test jobs consumed 32 minutes and 6 seconds of runner time.
The resources browser shard consumed 27 minutes and 48 seconds.

Compare elapsed time, summed job durations, and queue times separately.
Shared runner load and cold image caches can affect elapsed time.
Record the MR pipeline URL and commit with each measurement.

These changes preserve the runner's current concurrency and job resources.
Shards run as runner slots become available.
Runner storage and persistent distributed cache configuration require separate GitOps work.

## CI variables

For transport-only manual or scheduled pipelines, set `HTTPS_SECURITY_ONLY=true`.
This selector excludes production publication jobs and selects disposable verification targets.
Read [transport verification](transport-security.md) before running these checks.

Configure variables in **Settings > CI/CD > Variables**.
Keep credential values out of repository files and command output.

| Variable | Environment scope | Requirements |
| --- | --- | --- |
| `SONAR_TOKEN` | `sonar`, when supported | Mask the token. Allow analysis of trusted merge request source. |
| `GHCR_USER` | `production` | Protect the variable. Use the GHCR publishing account. |
| `GHCR_TOKEN` | `production` | Mask and protect the token. Allow package publication only. |

GitLab downloads the current pipeline's required image artifacts before publication starts.
Publication does not need a separate GitLab API token.
GitLab registry publication uses the automatic `CI_REGISTRY_USER` and `CI_REGISTRY_PASSWORD` credentials.
GHCR image publication requires the two production variables above.
The image publisher pushes the current pipeline's scanned archive without rebuilding it.
Chart publication does not use GHCR credentials.

Production variables must remain unavailable to unprotected merge request pipelines.
The `production` scope is used only by the two publication jobs.
Security jobs do not select this environment.
Missing credentials fail the corresponding job.
When environment scopes are unavailable, protect publication variables and use the default scope.
The default setup removes an inherited Sonar token before build and test commands.
It removes inherited publication credentials before commands outside the production environment.

SonarCloud waits for its quality gate and fails the pipeline when the gate fails.
Analysis keys use `gitlab-<merge-request-number>` to separate them from historical GitHub pull requests.
Self-managed GitLab supports SonarCloud analysis without native merge request decoration.
Read findings with `make sonar-issues MR=<merge-request-number>` and a local `SONAR_TOKEN`.

Bundled PowerShell and Python templates run through deployment API tests in separate processes.
These processes do not emit coverage for the Go reports.
SonarCloud excludes these script files from coverage and keeps their static analysis.
Go catalog, validation, and copy code remain in the coverage gate.

## Publication and retained evidence

The image job builds and scans Linux AMD64 OCI archives.
The image job retains archives and scan evidence for four hours for downstream jobs.
The retention job runs only in protected publication pipelines.
It keeps successful archives and source metadata for four hours in the same pipeline.
Merge request checks consume the original image artifacts directly.
If the artifacts expire, run a new pipeline before publication.
Source and configuration security reports remain available for seven days.
Browser and agent evidence remains available for one day.
Agent archives, runtime metadata, version metadata, and coverage reports expire after four hours.
If handoff artifacts expire, run a new pipeline instead of retrying downstream jobs.

Consumers exclude downloaded image archives and build metadata from their artifact uploads.
Verification gates wait for required jobs without downloading their artifacts.
Publication downloads archives only in the image publisher.
SonarCloud downloads coverage reports without uploading another copy.
Docker build contexts exclude `artifacts/` to avoid sending downloaded archives to the builder.

Disable **Keep artifacts from most recent successful jobs** in **Settings > CI/CD > Artifacts**.
GitLab otherwise preserves the latest successful artifacts for each ref despite `expire_in`.
This setting and YAML retention changes do not shorten the expiration of existing artifacts.
Review existing artifacts separately before deletion.
Keep archives that pending publication jobs require.

Registry tag deletion does not guarantee immediate disk reclamation.
If registry storage remains high after cleanup, inspect the registry garbage collection configuration.
Use the registry administrator procedure for the installed GitLab version.

Publication uses the current pipeline's scanned image archive.
It does not require merge request artifacts or compare the merged source tree with an earlier merge request pipeline.
The publisher verifies the archive checksum, commit, source tree, pipeline, producing image job, version, build number, and AMD64 platform metadata.
Missing or mismatched artifacts stop publication.

The publisher verifies the embedded build identity.
It publishes the scanned image bytes without rebuilding them.
Image publication must succeed in both registries before chart publication starts.
Chart publication must succeed in the GitLab registry before its job passes.

### Automatic release pages

The `release-page` job runs after publication succeeds on protected default-branch pushes.
Merges into `main` create these pipelines.
The job creates a GitLab Release with the existing software version, such as `v0.0.0-dev.742+g0123456789ab`.
GitLab creates the release tag at the pipeline commit.
The job uses `CI_JOB_TOKEN` through the [Releases API](https://docs.gitlab.com/api/releases/).
It requires no additional secret.

Release notes list every commit since the nearest previous release on the default branch.
The first release includes the full source history.
The notes include commit links and a comparison link when a previous release exists.
Failed publication leaves those changes for the next successful release.
Retrying the job preserves an existing release for the same version and commit.
Merge request, tag, manual, and scheduled pipelines do not create release pages.
Automatic development tags do not start another pipeline.
Release notes remain available in `artifacts/release/notes.md` for one day.

After merge, the default branch pipeline builds and scans the merged source before publication.
Publication requires successful lint, source security, configuration security, and image retention jobs from that pipeline.

## Project settings and cutover

After the first successful merge request pipeline, enable **Pipelines must succeed**.
Keep main and release tags protected before enabling publication credentials.
Create a weekly security schedule for Monday at 06:23 UTC.
Keep the schedule disabled until manual pipeline verification passes.
Select main as the schedule target.
GitLab runs all repository CI workflows.
The four GitHub workflows were removed after pipeline 1062 and its SonarCloud gate passed.
Runner GitOps changes and live product releases require separate work.

## Local checks

Run the contract checks before pushing pipeline changes:

```bash
make templ-generate swagger-ui-copy
bash scripts/check-ci-contract.sh
bash scripts/check-agent-ci-contract_test.sh
bash scripts/check-mobile-browser-container-contract.sh
bash scripts/go_test.sh -count=1 ./internal/cicontract
DURPDEPLOY_CONTAINER_RUNTIME=podman bash scripts/release_images_test.sh
```

Validate the combined configuration with the project CI Lint API.
Inspect merge request, main, tag, manual, and scheduled job graphs.
Verify actual runtime startup through the GitLab job logs.
