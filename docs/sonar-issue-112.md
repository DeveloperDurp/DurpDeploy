# SonarCloud issue #112

Scope: the 24 unresolved new-code bugs and vulnerabilities returned for
`developerdurp_durpdeploy`, branch `main`, on 2026-10-06. The baseline remains
previous version (2026-08-14). Gate thresholds and exclusions are unchanged.

## Source fixes

| Finding keys | Rule / location | Change |
| --- | --- | --- |
| AaDx5fiD4wRks0l6WTmC, AaDx5fiD4wRks0l6WTmD, AaDx5fiD4wRks0l6WTmE | javascript:S9383, static/js/app.js | Mark event-launched promises with `void`. Passkey helpers already catch failures; HTMX reports request errors through its events. Regenerate the committed bundle with esbuild. |
| AaCfcEDpFlmS90VF4jrf, AaCfcEDpFlmS90VF4jre | secrets:S6698 / S7539, scripts/e2e_db_test.sh | Remove implicit password-bearing DSNs and SQL Server client passwords. Require supplied DSNs and SQLCMDPASSWORD. Remove matching credentials from Makefile container setup and readiness checks; bind database ports to loopback. |
| AaAA5rY4ETc0FGsiMUbY, AaAA5rY4ETc0FGsiMUba, AaAA5rZhETc0FGsiMUbo | githubactions:S8545 / docker:S8545, release.yml / Dockerfile | Run the existing `go tool templ` locked by go.mod/go.sum instead of an independent installation. |
| AaAA5rY4ETc0FGsiMUbW | githubactions:S7636, release.yml | Pass the registry token through a step environment variable and standard input. |
| AaAA5rZhETc0FGsiMUbp | docker:S6505, Dockerfile | Use npm ci --ignore-scripts. Existing platform-specific esbuild packages supply the native binary. |
| AaAA5rXkETc0FGsiMUbH | kubernetes:S6870, deployment.yaml | Supply default ephemeral-storage requests and limits through the existing resources values. PVC storage is separate. |

The removed database values were public development defaults, not evidence
of credentials used in a production deployment. Existing containers can
still contain those values: stop or replace them only after preserving any
needed manual-test data. Use new secret-manager credentials when recreating
them. This PR does not delete, rotate, or modify existing databases.

## Proposed reviewed false-positive dispositions

These are proposals pending maintainer review and SonarCloud disposition.
They do not accept a confirmed vulnerability.

| Finding key | Rule / location | Source-specific rationale |
| --- | --- | --- |
| AaAPNwUAeYN0teSpHiDi | gosecurity:S5146, fixture_protocol.go | The test IdP compares the supplied redirect URL with its configured callback before building the redirect. An unregistered redirect is rejected by TestFixtureAuthorizeRejectsInvalidRequest. |
| AaAPNwT0eYN0teSpHiDh | go:S5542, fixture_token.go | rsa.SignPKCS1v15 signs SHA-256 JWTs using RS256, the required test protocol operation. It does not encrypt attacker-controlled plaintext. The fixture generates its own 2048-bit key. |
| AaAPNwifeYN0teSpHiDr | plsql:NullComparison, migrations/024_oidc.sql | This is SQLite SQL. An empty string is not NULL; the comparison validates nonempty claim content. Applying Oracle PL/SQL empty-string semantics would change the constraint. |
| AaAPNwdyeYN0teSpHiDq | plsql:NullComparison, migrations/mssql/009_oidc.sql | This is SQL Server SQL, which also distinguishes an empty string from NULL. The existing constraint is intentional. |
| AaCfcEClFlmS90VF4jrc | go:S5332, oidc-browser-fixture/harness.go | The oidctest-tagged browser fixture binds the application callback listener to 127.0.0.1 on an ephemeral port. The fixture IdP uses TLS. This HTTP callback does not cross a network boundary. |
| AaCfcEClFlmS90VF4jrd | go:S5332, oidc-browser-fixture/harness.go | The readiness probe addresses the same loopback-only temporary application listener. |
| AaAA5rLkETc0FGsiMUW2 | go:S2092, login_mfa_pending.go | Secure is set from h.cookieSecure. HTTPS DURPDEPLOY_URL enables it; local HTTP development is supported. Production operators must configure the documented HTTPS public URL. An unset URL does not guarantee a loopback-only listener. |
| AaAA5rNQETc0FGsiMUXL | go:S2092, login_session.go | This expires an empty pending-login cookie, preserving the same conditional Secure policy as cookie creation. |
| AaAA5rM1ETc0FGsiMUXI | go:S2092, security.go | This expires an empty pending-login cookie with h.cookieSecure, not a hardcoded false flag. |
| AaAA5rMKETc0FGsiMUW- | go:S2092, security_passkey_verification_helpers.go | This clears an empty WebAuthn challenge cookie using the same conditional Secure policy as creation. |
| AaAA5rNYETc0FGsiMUXM | go:S2092, auth.go | Logout expires an empty session cookie with h.cookieSecure; the session creation policy is unchanged. |
| AaAA5rIXETc0FGsiMUWV | go:S2077, api/admin.go | The private countRows helper receives only the literal identifiers users, projects, and deployments. No request input supplies the formatted SQL identifier; the endpoint is admin-protected. |
| AaAA5rZZETc0FGsiMUbn | docker:S6471, Dockerfile.mobile-browser | This is the disposable CI browser-test image, not the published application runtime. It needs to copy the checkout and install test fixtures. The mobile job does not mount a container-engine socket. The production Dockerfile uses USER 10001:10001. |

## Completion evidence

The CI development-database contract checks loopback port bindings, rejects
missing credentials before container replacement, and rejects missing DSNs
before contacting the server. Existing Go, browser, API E2E, image build,
Helm, and SonarCloud checks remain required.

The PR gate does not prove the main-branch gate has recovered. After merge,
verify the latest main analysis has reliability A and security A, and that
coverage, duplication, maintainability, and hotspot review still pass.
Keep issue #112 open until that evidence and reviewed dispositions exist.
