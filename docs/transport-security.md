# Transport verification

This suite verifies a disposable deployment of the checked-out source behind the shipped Caddy configuration.
It does not scan production, shared infrastructure, operator databases, or deployment targets.
It does not issue public certificates or run deployment scripts.

## Policy

Browser and API connections support TLS 1.2 and TLS 1.3.
TLS 1.2 permits ECDHE with AES-128-GCM, AES-256-GCM, or ChaCha20-Poly1305.
RSA and ECDSA certificate variants use the corresponding cipher suites.
TLS 1.3 permits AES-128-GCM, AES-256-GCM, and ChaCha20-Poly1305.
The policy rejects SSL, TLS 1.0, TLS 1.1, CBC, and static RSA key exchange.
The versioned cipher identifiers are in `.security/transport-policy.json`.

Certificates must match the fixture hostname and chain to its private test CA.
Certificates must be valid and permit TLS server authentication.
Fixtures exercise RSA-2048 and ECDSA P-256 public keys.
Wrong-name and untrusted-CA controls must fail.

Caddy sends `Strict-Transport-Security: max-age=86400` only over HTTPS.
Browsers retain this HTTPS requirement for one day.
The policy does not include subdomains or preload registration.
The application sends `X-Content-Type-Options: nosniff` for API and web responses.
Intentional local HTTP development does not set HSTS or Secure cookies.
Forwarded scheme headers cannot change the configured cookie policy.
The real-proxy matrix covers direct untrusted peers, untrusted proxy hops, and an explicitly trusted CDN hop.
The trusted case selects the nearest untrusted address and rejects the spoofed leftmost address.
The public audit API supplies the observed client identity.

Framing protection reuses the policy from merged MR !66.
The final proxy check requires `Content-Security-Policy: frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN`.
Missing or changed framing headers fail the suite.

## Intentional listeners

The stock Compose stack publishes Caddy TCP ports 80 and 443.
Port 80 redirects to HTTPS.
The stack does not publish Caddy UDP port 443.
The internal Caddy UDP listener supports QUIC without creating another host publication.
The Caddy administration API stays on loopback port 2019.

The app HTTP listener uses port 8080 inside the Compose network.
The dedicated agent listener uses TCP port 23187 and TLS 1.3.
It requires an active, paired, pinned client certificate.
Restrict the production agent port to approved agent networks.
The fixture rejects clients without certificates and with unpaired certificates.
Existing agent lifecycle suites cover pairing and revocation.

Web administration and `/api/v1/admin/*` remain authenticated application routes.
They are not separate public administration listeners.
The fixture expects no debug, metrics, database, or runtime API listener.
SQLite, WAL, and SHM files are not network services or web assets.
External PostgreSQL and SQL Server deployments remain operator-owned services outside this scan.

## Run checks

Start the selected local Docker or rootless Podman socket.
Run `make transport-security-quick` for API, web, browser, TLS-client, forwarding, socket, and publication checks.
Run `make transport-security` for the additional RSA and ECDSA testssl.sh scans.
Run `make transport-security-contract` for detection, redaction, inventory, and CI boundary controls.
Run `bash scripts/transport_cleanup_test.sh` for real scanner failure, browser timeout, and cancellation controls.

The runner accepts only `quick` or `full` mode.
It accepts no scan URL, host range, or port override.
It derives targets from owned container records and engine inspection.
Each run creates a private internal network, synthetic account, database, identity, and certificates.
On Docker, fixture services also join an owned ingress network for loopback port publication.
Only the app, Caddy, and the forwarding fixture join that ingress network.
Published fixture ports bind only to loopback.
The application disables its embedded workload agent and receives no runtime socket.
Scanner containers run without capabilities, credentials, runtime sockets, or external network access.
The browser and its Go test runner share an owned container on the same internal network.
Only synthetic login credentials enter that browser through standard input.
The browser uses the pinned runtime and trusts only the fixture leaf public key.
Its debugging listener stays on container loopback and has no published port.
The parent removes the browser after cancellation or a test timeout.
The orchestrator retains the existing local runtime authority to create and remove owned resources.

The Go probe replaces Nmap with maintainer approval for issue #50.
It checks exactly these TCP ports on each owned app and Caddy container:
`80,443,1433,2019,2375,2376,5432,6060,8080,9090,23187`.
It uses TCP connections without raw sockets or service detection.
Positive listener controls prevent an unreachable target from appearing safe.
Complete TCP and UDP socket inventories also check IPv6 listeners and unexpected ports outside this list.
Engine inspection independently verifies published ports and socket mounts.
The fixture uses IPv4 networking and does not certify arbitrary IPv6 routing or operator overrides.

Each TCP connection has a one-second deadline.
Each target has a one-minute total budget.
Each testssl.sh scan has a five-minute budget.
The GitLab job has a 30-minute budget.
Cleanup runs after success, failure, cancellation, and ordinary termination signals.
SIGKILL or engine failure can prevent cleanup.
If cleanup fails, retain the private recovery record and retry its printed teardown command.

## Evidence and fixture exceptions

Sanitized reports are under `artifacts/transport/`.
CI retains them for seven days, including failures.
Reports identify the source commit, stage, policy results, and cleanup failures.
Browser failures record an allowed check phase, a failure category, and the exit status.
Raw browser errors remain private.
They also record a source-file digest and disclose application image overrides.
Source changes during a run make its result incomplete.
Use a fresh application build for final verification.
They omit passwords, bearer tokens, cookies, private keys, certificate contents, and raw scanner findings.
Browser evidence contains only synthetic authenticated pages and logout screens.
Raw scanner output and credentials remain in private temporary files.
Confirmed cleanup removes those files.
Missing tools, malformed reports, incomplete coverage, or failed cleanup cannot pass.

The private fixture certificates expire after seven days.
They intentionally lack production revocation services.
The policy permits only the exact short-lived, currently valid certificate findings for this fixture.
These dispositions do not accept expired certificates or production certificate risk.
The system OpenSSL lacks a GOST engine.
The policy records this exact limitation because GOST ciphers are outside the approved cipher set.
Other scanner capability problems fail the check.
CI also verifies rejection of a failed scanner and cleanup after termination.
Termination controls wait until the isolated browser is ready.
An additional control forces the browser test deadline to expire.
Expected failure and cancellation summaries are under `artifacts/transport/controls/`.
The suite does not test production ACME renewal, OCSP availability, or certificate transparency.

The source and tool pins are recorded in `.security/transport-tools.md`.
Nmap was excluded after its exact-source license check failed the repository dependency gate.
New findings require contextual review.
Do not add blanket suppressions or accept real risk without a maintainer decision.

## Manual and scheduled CI

Select the tested source branch in the GitLab manual pipeline form.
Set `HTTPS_SECURITY_ONLY=true`.
Confirm that the graph contains only `ci-runtime` and `transport-security`.
This selector excludes release, publication, SonarCloud, and unrelated test jobs.
Ordinary merge request pipelines run the quick suite with existing gates.
Scheduled and manual security pipelines run the full suite.
Do not create or enable a schedule without operator approval.
Use a schedule owner with the minimum project permissions.
Do not add production credentials to these jobs.

## Optional Burp and Nuclei investigations

Obtain approval for the exact disposable fixture, route list, tool version, and request budget.
Use synthetic credentials and a separate browser profile.
Scope certificate trust to that profile.
Restrict tools to the printed fixture origin and its isolated network.
Exclude deploy, runbook execution, pairing, backup, upload, and destructive administration routes unless separately approved.
Disable out-of-band callbacks, template updates during scans, and external discovery.
For Nuclei, pin and review each template before use.
Limit investigations to five minutes, one request per second, and 300 requests.
Stop on unexpected targets, authentication failure, or unintended state changes.
Review findings before applying any fix or suppression.
Export only redacted evidence.
These tools are optional investigations and are not mandatory CI dependencies.
