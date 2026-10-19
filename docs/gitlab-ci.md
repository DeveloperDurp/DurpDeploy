# GitLab CI

The pipeline runs on the DMZ Kubernetes executor with one privileged pod for each job.
The runner supports Linux AMD64 and accepts untagged jobs.
The runner manager limits concurrency to ten jobs.
ARM64 build and scan coverage is removed.

## Pipeline events

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
Each job writes cache updates to its own container layer.
The pipeline does not upload unusable local GitLab cache archives.
Same-project merge requests reuse separate browser and helper build caches under `ci-cache/mr-<number>`.
Merge request jobs never write shared protected caches.
Protected and other pipelines do not import merge request caches.
Builds check upstream base references before using cached layers.
Build timing evidence records browser and helper build durations.
Product images and charts remain in GHCR.

The disposable CI images use root to prepare checkout ownership and test fixtures.
The helper also starts its private Docker daemon and switches to the rootless Podman user.
SonarCloud rule `docker:S6471` has an exception for these two Dockerfiles only.
The browser image follows the reviewed policy in [SonarCloud issue #112](sonar-issue-112.md).
Other security rules and quality gate thresholds remain active.
The published application image uses `USER 10001:10001`.
The browser image installs templ from `go.mod` and `go.sum` with `-mod=readonly`.
It disables npm lifecycle scripts with `npm ci --ignore-scripts`.

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
Browser jobs pull Playwright before browser startup deadlines start.
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
It verifies rootless execution before it runs the agent tests.
The job temporarily allows exit code `77` when `user.max_user_namespaces=0`.
Other Podman failures fail the pipeline.
Remove `allow_failure:exit_codes` after runner nodes enable user namespaces.
Rootless Podman requires enabled user namespaces on the runner nodes.
Ask the runner administrator to enable user namespaces if the job reports this prerequisite failure.

Keep `runUntagged` enabled for this runner.
Keep the existing privileged Kubernetes job setting for the nested runtimes.
Use a registry cleanup policy for old `ci:pipeline-*` helper image tags.
Apply the same retention policy to intermediate `ci-browser:pipeline-*` tags.
Expire `ci-cache/mr-<number>` repositories after their merge requests close.

### Scheduled MR cache cleanup

The `cache-cleanup` job selects only this project's exact `ci-cache/mr-<number>` registry paths.
It preserves open merge requests and merge requests with active head pipelines.
It waits 24 hours after a merge request closes or merges.
Before deletion, it checks the merge request state again.
GitLab deletes registry repositories asynchronously.
The report records deletion requests, not completed storage reclamation.

The job uses `CI_JOB_TOKEN` for registry and merge request API requests.
It does not require a separate cleanup token.
Merge request pipelines run its tests and a read-only dry run.
Reports are available in `artifacts/cache-cleanup/report.json` for seven days.

After merging, create a daily pipeline schedule targeting protected `main`.
Set the schedule variable `CI_CACHE_CLEANUP` to `true`.
Set `CI_CACHE_CLEANUP_DRY_RUN` to `true` for the first run.
Inspect the report before enabling deletion.
To enable deletion, set `CI_CACHE_CLEANUP_DRY_RUN` to `false` on that schedule.
Use a schedule owner with permission to delete this project's registry repositories.
Cleanup schedules run only `cache-cleanup`.
Normal security schedules retain their existing jobs.

Keep the built-in `pipeline-.*` cleanup policy for old pipeline image tags.
The scheduled job preserves helper images, browser images, release images, and open merge request caches.
Retain helper images while their pipelines remain available for retries.
The source scan excludes `.cache/` from application analysis.

## Parallel test groups

The agent image job builds the locked agent once for Docker and rootless Podman checks.
Both runtimes verify the pipeline, producing job, commit, module version, checksum, and artifact hashes before loading that archive.
Each runtime retains its execution contract and API tests.

The CI image precompiles locked dependencies with normal and race instrumentation.
Coverage jobs reuse normal dependency objects, and race jobs reuse race dependency objects.
Go still validates and compiles source-specific application code in each isolated job.
Dependency locks, Go version, probe source, and image definitions control cache invalidation.

Each package browser shard shares one Chromium process.
Each test creates a separate browser context and disposes it after its assertions.
Browser contexts isolate cookies and local storage.
Tests that change browser-wide certificate settings retain private Chromium processes.
Standalone browser checks retain their owned-container fallback.

Fixture preload checks pull an image only when it is absent from the private runtime.
Application image pulls and pull-failure assertions retain their existing behavior.

The E2E and SQLite MFA jobs reuse the scanned application image from their pipeline.
Each job verifies the archive checksum, commit, tree, pipeline, build identity, and producing job before loading the image.
These jobs extract the static server binary for shell E2E and MFA browser checks.
Local checks build from the checkout unless `DURPDEPLOY_E2E_BINARY` selects an existing binary.
Container E2E builds its image unless `DURPDEPLOY_E2E_IMAGE` selects an existing image.
Go tests that run application handlers in-process retain their existing builds and race settings.

The browser job runs four shards: navigation, resources, execution, and agents.
Each shard retains the existing test commands and assertions.
Browser evidence uses a separate directory for each shard.
The rollout job runs four shards: contracts, two artifact groups, and verification.
Artifact groups split the compiled test inventory and require every selected test to pass.
New artifact gate tests enter one group automatically.
The image verification job waits for all browser and rollout shards.

Restore cleanup checks retain genuine Go timeouts and separate TERM interruption checks.
The timeout is two minutes for SQLite and four minutes for PostgreSQL and SQL Server.
The fixture requires at least 30 seconds of timeout headroom after workload startup.
Job logs report startup time and remaining timeout headroom.
Cleanup checks still verify owned resources and preserve an unrelated container.

After assertions finish, disposable database fixtures use no graceful shutdown wait during removal.
Cleanup still uses an independent bounded context and fails if container removal fails.
Explicit shutdown, recovery, timeout, and interruption checks keep their existing behavior.
This change does not affect retained running-server examples or application shutdown.

Unit tests run four shards: repository, database, handlers, and core.
New unit-test packages enter the core shard automatically.
Coverage runs five shards: API, handlers, repository, migrations, and core.
Every Go package belongs to one shard.
New packages enter the core shard unless a specific group includes them.
Each shard uses `-coverpkg=./...` and retains a separate coverage report.
SonarCloud waits for all five shards and requires all five reports.
The scanner reads the reports together and waits for the same quality gate.

The runner keeps its existing ten-job concurrency limit.
Shards run as runner slots become available.
Runner storage and persistent distributed cache configuration require separate GitOps work.

## CI variables

Configure variables in **Settings > CI/CD > Variables**.
Keep credential values out of repository files and command output.

| Variable | Environment scope | Requirements |
| --- | --- | --- |
| `SONAR_TOKEN` | `sonar`, when supported | Mask the token. Allow analysis of trusted merge request source. |
| `GHCR_USER` | `production` | Protect the variable. Use the GHCR publishing account. |
| `GHCR_TOKEN` | `production` | Mask and protect the token. Allow package publication only. |

Publication uses GitLab's short-lived `CI_JOB_TOKEN` to read merge request metadata, job results, and image artifacts.
Do not configure a separate GitLab read token.
The E2E job checks these API permissions and downloads version metadata with its real job token.

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

## Publication and retained evidence

The image job builds and scans Linux AMD64 OCI archives.
It retains sanitized image evidence for seven days, including failed scan evidence.
The retention job keeps successful archives and source metadata for thirty days.
Source and configuration security reports remain available for seven days.
Browser and agent evidence remains available for seven days.

Publication requires the latest successful matching merge request pipeline.
It checks the latest image and retention jobs, source tree, source references, archive hash, and AMD64 platform metadata.
Squash merge commit identifiers can differ when their source trees match.
Missing, expired, failed, or mismatched artifacts stop publication.
The publisher separately verifies the current pipeline archive and embedded build identity.
It publishes the scanned image bytes without rebuilding them.
Chart publication requires successful image publication.

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
