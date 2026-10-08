# Security scanning

The Security gates workflow runs on pull requests, main, Mondays at 06:23
UTC, and manual dispatch. It complements SonarCloud; it does not replace
the existing test or quality gates. No production credentials are needed.

Install the pinned, task-local tools and reproduce the gates:

```bash
make security-tools
make security-scan-test
make security-scan
```

Requirements: Go (able to download the official 1.26.8 toolchain), npm,
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
  bypass this gate. Generated Go is excluded; hand-maintained application
  and test source is analyzed. Compile/load errors, empty analysis, and
  invalid reports fail.
- Gitleaks fails on credentials in the current source tree or Git history.
  PRs scan base SHA..head SHA plus the merge checkout's source tree. Main,
  scheduled, manual, and default local scans inspect all available history.
  Checkout fetches full history. Ignored binaries, databases, and reports
  are not source inputs. To reproduce a PR range locally, export
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

## Evidence

Safe reports are written to `artifacts/security/` and retained for seven days
in CI. Gosec and Gitleaks reports contain rules, locations, and dispositions,
not source snippets, matches, or secret values. Raw output and diagnostic
logs stay in a private temporary directory removed on exit. Govulncheck
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
