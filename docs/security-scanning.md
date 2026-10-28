# Security scanning

Read [transport verification](transport-security.md) for disposable HTTPS, cookie, forwarding, and listener checks.
Use its security-only selector for manual pipelines without publication jobs.

The GitLab pipeline runs security checks for merge requests, main, tags, manual runs, and configured schedules.
It preserves the existing test and SonarCloud quality gates.
Security jobs do not need production credentials.
Image and chart publication require successful source and configuration scans from the same pipeline.
The scanner fixture suite and API-token regression test run only for merge requests.
See [GitLab CI](gitlab-ci.md) for schedule, credential, and runner setup.

## Bounded fuzzing and race checks

Merge request Go suites replay native fuzz seeds and committed regression inputs.
Every merge request pipeline also runs bounded exploration across five offline targets.
Findings and timeouts fail the required `fuzz` job.
Scheduled pipelines run the same exploration.
Manual pipelines enable exploration when `CI_FUZZ=true`.
Cache-cleanup schedules exclude these jobs.

`FuzzReadJSON` checks closed-schema JSON, chunked input, stable errors, and bounded body reads.
Existing public API regressions check rejected requests before database mutations.
`FuzzValidateZIPPaths` checks entry paths, special entries, duplicates, and tree collisions.
It builds at most two stored entries with fixed payloads and never extracts fuzzed paths.
`FuzzScrubberStreaming` checks chunk boundaries, pending-byte bounds, and synthetic credential redaction.
`FuzzDefaultPatternStreaming` reuses the default-credential regression target added by the redaction fix.
`FuzzReviewJSONSyntax` retains its existing comparison with Go JSON validation.

Fuzz inputs never become scripts, URLs, runtime commands, or network requests.
Targets use synthetic credentials and local fixtures.
The runner removes inherited application settings and credentials before testing.
It preserves only the Go toolchain, cache paths, and process environment needed for compilation.
It uses the pipeline's pinned Go toolchain and runtime image without new dependencies.

Run exploration from a prepared checkout:

```bash
make templ-generate swagger-ui-copy
bash scripts/ci_fuzz.sh
```

Each target has a 30-second exploration budget and a five-second minimization budget.
Two workers share the job's two-CPU and 4-GiB limits.
Each test binary has a two-minute timeout.
Each command has a six-minute deadline and a ten-second termination grace period.
This deadline includes compilation and test-runner cleanup.
The complete job has a 20-minute timeout.
Failures and timeouts fail the job.

Reports in `artifacts/fuzz/` contain reproduction commands, progress, target names, and exit codes.
Raw native output can echo inputs, so the runner discards it after selecting safe diagnostic lines.
Failed synthetic inputs remain unchanged in `artifacts/fuzz/corpus/<target>/` for seven days.
Review those inputs before adding them to `<package>/testdata/fuzz/<target>/`.
Never add real credentials or private payloads to the corpus.

Replay a retained input with the reported package and target:

```bash
bash scripts/go_test.sh -count=1 \
  -run '^FuzzScrubberStreaming/3e1a1c989b26e560$' ./internal/logscrub
```

The retained malformed-byte regression distinguishes exact credentials from different invalid-byte lookalikes.
Go regex treats malformed bytes as replacement runes.
Whole-message matching can therefore redact lookalikes that streaming literal matching preserves.
The test checks exact malformed credentials and their displayed UTF-8 form separately.
It does not require different byte values to be identical credentials.

The existing rollout race gate also selects cancellation and concurrent broker lifecycle tests.
The API/web SSE regression overlaps disconnects with live broker notifications.
These checks reuse the existing race jobs without adding artifact, network, or browser duplicates.
Existing artifact, stream-revocation, and browser checks remain required.

Triage failures against the affected boundary before changing application behavior.
Behavioral fixes require public API regressions and web regressions where applicable.
Update the shipped agent skill when documented API behavior changes.
Do not suppress a real finding without a reviewed justification and maintainer risk decision.

## Image verification and publication

Merge requests, main pushes, release tags, manual runs, and scheduled runs build Linux AMD64 images.
Each build embeds its software version, source commit, and commit-count build number.
Trivy scans each exported OCI image layout before GitLab retains the original archive as a job artifact.
The image policy blocks fixable HIGH and CRITICAL vulnerabilities and scanner errors.
The scanner ignores configuration and ignore files from the checkout.
GitLab retains sanitized reports and CycloneDX SBOMs for seven days, including reports from blocked image scans.
Reports identify the checkout commit, OCI manifest digests, scanner version, and vulnerability database version and timestamps.
The merge request pipeline requires the AMD64 image scan, Go tests, E2E tests, and source security checks.
Each blocking job must pass before the pipeline can succeed.

After merge, the default branch pipeline builds and scans the merged source.
Publication verifies the current run's scanned archives, including pipeline, producing image job, checksum, source tree, version, build number, commit, and platform.
It also verifies the embedded image labels.
Publication does not require merge request artifacts or a matching merge request source tree.
The publisher uses these scanned platform manifests without rebuilding or rescanning.
Helm publication requires successful image publication.
The image publisher sends the scanned image to GitLab and GHCR.
The chart publisher uses only the GitLab registry.

GitLab retains successful image artifacts for 30 days.
Missing, expired, failed, or mismatched artifacts stop publication.
If artifacts expire, rerun the image build and retention jobs before publication.

Image publishing jobs share one queue.
Each pipeline retains its own source-scan jobs and artifacts.
Version tags cannot replace different images.
An older or unrelated source revision cannot replace `latest`.

Image tags replace SemVer build metadata's `+` separator with `_` to prevent collisions with prerelease tags.
Helm uses that image tag for `appVersion` and preserves the original chart version.

The image labels identify the source revision compiled into the executable.
The published image index identifies the final merged revision.

## Container and deployment scanning

The image job scans the AMD64 production image before retaining promotion archives.
Sanitized image evidence is stored in `artifacts/image-evidence/`.
Failed scans cannot retain promotion archives or publish images.
Scanner failures leave an `incomplete` report when the scan runner starts.
Missing artifacts also indicate missing evidence.

The `container-security` job scans the Dockerfile and Kubernetes resources rendered from the Helm chart.
The renderer uses synthetic defaults and references external Secrets.
It does not load operator values, cluster configuration, environment files, or live credentials.
Reports retain check identifiers, severity, and status.
Reports omit source snippets, messages, matches, environment values, and image configuration.
Configuration reports identify the check bundle digest and download timestamp.

Trivy evaluates root users, capability settings, mounts, resource limits, and secret configuration where its built-in checks support them.
Trivy does not support Compose configuration scanning.
The same job runs `scripts/check-agent-compose-contract.sh` for the existing Compose execution and privilege contracts.
These checks do not certify arbitrary operator overrides or live deployments.
The intentional runtime socket mount grants the embedded agent the operator's container authority.
The Compose entrypoint briefly uses root with SETUID and SETGID, then starts the application as UID 10001.

The job also scans `package-lock.json`, including transitive frontend and build dependencies.
The production image scan covers OS packages and Go binary dependencies.
Bundled JavaScript does not expose a reliable lockfile inventory inside the runtime image.
The separate lockfile scan covers that gap without installing or executing scanned packages.

Scheduled and manual security checks rebuild and scan the Linux AMD64 production image with current vulnerability data.
These builds use the selected checkout and the production Dockerfile.
They do not publish images or change infrastructure.
They evaluate fresh builds rather than the bytes of an existing registry tag.
Each job has a 30-minute timeout.
Each scanner invocation has a 10-minute timeout.

### Policy and exceptions

Image and frontend scans block HIGH and CRITICAL vulnerabilities that have a reported fixed version.
Unfixed vulnerabilities and lower severities remain visible in the report.
Configuration scans block every failed HIGH or CRITICAL check.
Lower configuration severities remain advisory.
Missing package inventories, absent configuration coverage, invalid reports, and scanner or database errors fail the check.
This policy extends the existing fixable HIGH/CRITICAL image gate.

No Trivy exceptions are enabled.
The runner rejects inline configuration suppressions and ignores checkout ignore files.
If a finding blocks CI, review its affected package or deployment path before changing the policy.
Accept real risk only through an explicit maintainer decision.
Record the exact check or vulnerability, target, affected version, owner, rationale, and expiry in a reviewed change.
Do not suppress a whole directory, severity, or scanner to clear a finding.

### Local reproduction and fixtures

Install the checksum-pinned tool and run the configuration and dependency checks:

```bash
make trivy-tools
bash scripts/container_scan.sh config . artifacts/container/config
bash scripts/container_scan.sh dependencies . artifacts/container/dependencies
bash scripts/check-agent-compose-contract.sh
make container-scan-test
```

Requirements: Linux AMD64 or ARM64, Bash, curl, tar, sha256sum, jq, realpath, Git, OpenSSL, and Helm 4.2.2.
The first scan downloads the public vulnerability database or check bundle.
Task-local caches stay in ignored `bin/trivy-cache/`.
Trivy 0.76.0 uses fixed SHA-256 archive checksums for each architecture.
The installer retains the Apache-2.0 license and upstream notice beside the binary.
The [exact release SBOM](https://github.com/aquasecurity/trivy/releases/download/v0.76.0/bom.json) lists open-source dependency licenses.
The remaining unclassified dependency, `github.com/xi2/xz`, has a [public-domain license at its pinned revision](https://github.com/xi2/xz/blob/48954b6210f8/LICENSE).

Scan an exported production OCI layout without publishing it:

```bash
mkdir -p artifacts/production-layout
tar -xf /absolute/path/image.tar -C artifacts/production-layout
bash scripts/container_scan.sh image artifacts/production-layout artifacts/container/image
```

The runner produces `scan.json` and `sbom.cdx.json` for completed image scans.
The SBOM retains package identifiers and dependency relationships.
It omits image configuration, file properties, and vulnerability descriptions.
Exit code 0 means complete analysis without blocking findings.
Exit code 1 means complete analysis with blocking findings.
Exit code 2 means analysis or evidence failed.

The Bash fixtures run real Trivy checks against an unsafe Dockerfile, rendered Kubernetes resources, and a vulnerable synthetic lockfile.
They do not execute fixture workloads or publish images.
Fault injection verifies image policy, SBOM retention, report sanitization, missing inventories, database metadata failures, and malformed outputs.
Go CI contract tests verify publication barriers, bounded jobs, evidence separation, and scheduled scan wiring.

After the first successful GitLab merge request pipeline, enable **Pipelines must succeed**.
Preserve branch protection and administrator enforcement.
Update other open merge requests from main before merging them.

Run the artifact and isolated registry checks with:

```bash
DURPDEPLOY_CONTAINER_RUNTIME=podman bash scripts/release_images_test.sh
bash scripts/go_test.sh -race -count=1 ./internal/cicontract
```

The shell suite requires Git, jq, Skopeo, curl, and Docker or Podman.
It uses disposable source repositories, synthetic images, and a loopback-only registry.
The Podman path runs the Docker CLI in a pinned disposable container.
Both paths remove their registry container when the suite exits.

Install the pinned, task-local tools and reproduce the gates:

```bash
make security-tools
make security-scan-test
make security-scan
go test -count=1 ./internal/cicontract
```

Requirements: Go (able to download the official 1.26.10 toolchain), npm,
jq, git, tar, sha256sum, and OpenSSL for fixtures. The Makefile prepares
templ and Swagger assets before Go analysis. Tools live in ignored
`bin/security/`; scanner module versions are verified before every scan.
Pins: govulncheck 1.8.0, gosec 2.29.0, Gitleaks 8.30.1.

## Blocking policy

- Govulncheck fails on reachable vulnerable symbols, with call traces.
  Package/module-only advisories remain visible in its verbose report.
  The official toolchain avoids silently skipping standard-library checks
  with a locally customized Go version string.
- Gosec fails on every new medium/high finding outside reviewed exceptions.
  Low findings remain advisory and visible. Inline `nosec` comments cannot
  bypass this gate. Generated Go and `*_test.go` are excluded; application
  source and ordinary Go fixture-helper packages are analyzed. Gitleaks
  scans test sources as well as application sources. Compile/load errors, empty analysis, and
  invalid reports fail.
- Gitleaks fails on credentials in the current source tree or Git history.
  Merge requests scan base SHA..head SHA and the pipeline checkout source tree. Main,
  scheduled, manual, and default local scans inspect all available history.
  Checkout fetches full history. Ignored binaries, databases, and reports
  are not source inputs. To reproduce a merge request range locally, export
  `SECURITY_GIT_RANGE=<40-character-base-SHA>..<40-character-head-SHA>`.
- Scanner errors, absent tools, unavailable vulnerability databases, and
  malformed reports fail rather than produce a clean result.

## Initial baseline and review

Baseline: main `530b68a79428af82fa09aa6baea5505b3f140ac2`, October 7,
2026. Govulncheck found no reachable vulnerabilities. GO-2026-5932 applies
to the unused `golang.org/x/crypto/openpgp` package and remains informational.
Gosec reported 98 findings: 61 medium/high findings received contextual
false-positive review and 36 low findings remain advisory. One additional
G118 high-severity finding in API-token telemetry was fixed with a five-second
timeout, following maintainer approval. Its public-API regression test covers a
successful usage update and cancellation while the database waits.
Gitleaks found ten historical and five current synthetic test credentials;
the reviewed exceptions cover their exact rule, path, and literal only.

`.security/gosec-exceptions.json` records each reviewed rule, relative file,
line, complete source-file SHA-256, reason, owner, and expiration. Entries
expire January 1, 2027. Changes to an excepted file require re-review.
The exact Gitleaks canaries have the same owner and expiration; their gate
fails when that review expires.
Reasons distinguish operator-controlled script execution, private artifact
staging, bounded HTTP inputs, numeric local redirects, and intentional
development HTTP cookies from exploitable inputs. These are contextual
contracts, not blanket exclusions of runner, authentication, or tests.

Do not refresh hashes to make CI green. Trace the affected input and callers,
verify the stated contract, and update only the reviewed entry. New findings
need a fix or a documented maintainer risk decision; scanner errors need
repair, never suppression. Real credentials require revocation/rotation
and incident review before any narrowly justified history exception.

Issue #145 re-reviewed the two G115 conversions in `internal/logscrub/scrubber.go`.
Only the standard-library regular expression compiler creates their instruction indexes and empty-width flags.
The prefix-buffering fix leaves those conversions unchanged.
The updated source hashes retain the existing reasons, owner, and expiration.

The split-prefix tests use fixed, inert AWS-format and Slack-format canaries.
Their exceptions require the exact rule, test path, and literal or complete source line.
The Slack rule extracts only part of the canary, so its exceptions match complete source lines.
The scanner fixtures reject changed canaries, unrelated credentials, and the same literals outside the reviewed paths.

## Evidence

Safe reports are written to `artifacts/security/` and retained for seven days
in CI. Gosec and Gitleaks reports contain rules, locations, and dispositions,
not source snippets, matches, or secret values. Raw output and diagnostic
scanner logs stay in a private temporary directory removed on exit. Policy
parser errors operate only on safe metadata and may appear in CI. Govulncheck
publishes dependency/advisory metadata and reachability traces. An
`incomplete` marker identifies a failed scan; a missing report is also a
failure, never evidence that analysis passed.

The Bash fixture suite invokes the pinned scanners on temporary files and
an inert local vulnerability database. It proves reachable detection,
unreachable non-blocking behavior, unsafe Go detection, current/history
secret detection, narrow test exceptions, missing-tool/database failures,
invalid analysis/report rejection, and credential-free published evidence.
`TestAPITokenTelemetryE2E` also checks the production authenticated endpoint
over HTTP, without changing its response contract or web behavior.
