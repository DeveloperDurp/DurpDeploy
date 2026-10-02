# Issue 35: bounded and strict request bodies

Status: proposed, awaiting approval. This draft PR contains the plan only.
Implementation, tests, and API changes have not started.

Issue: https://github.com/DeveloperDurp/DurpDeploy/issues/35

## Request

Bound API request bodies, reject unknown fields in closed schemas, and require
exactly one JSON document. Oversized requests must return 413; malformed or
trailing input must return 400. Keep large script payloads usable and document
web form and multipart bounds.

## Current behavior

- `internal/handler/api/common.go`: `readJSON` and `readJSONBool` each decode
  once directly from `r.Body`. Neither bounds input nor checks unknown fields
  or EOF. Callers of `readJSON` currently map all decoding errors to 400.
- `internal/handler/api/tokens.go`: `APITokenHandler.CreateToken` bypasses both
  helpers with another direct decoder.
- `internal/handler/lint.go`: `LintHandler.LintScript` also decodes directly.
  Its session-authenticated `/api/lint` route is outside the token API group.
- `internal/server/server.go`: token API routes preserve authentication,
  viewer write blocking, project authorization, and audit middleware. Some
  control actions accept no request body and do not call a JSON helper.
- `internal/auth/csrf.go`: `CSRFMiddleware` calls `PostFormValue`, so web body
  limits must apply before CSRF can parse a form.
- `internal/server/password_form_limit.go`: login and password reauthentication
  already have a 64 KiB limit with established 400/422 error contracts.
- `internal/handler/artifacts.go`: package repository forms already use
  `http.MaxBytesReader` with a 64 KiB limit.
- No explicit multipart upload handlers were found in the main web handlers.
  The proposed form limit still bounds multipart requests.
- `internal/agentserver/artifact.go`: the separate mTLS artifact endpoint already
  uses a 4096-byte limit, unknown-field rejection, and an EOF check. It is not
  part of the public token API change.

## Proposed implementation

1. Add documented constants for a 4 MiB API/lint request limit and a 16 MiB web
   form limit. These count encoded body bytes, including JSON or form encoding
   overhead. Preserve the existing smaller password and package-form limits.
   Wire bounds in `internal/server/server.go` before any body-consuming
   middleware, using `http.MaxBytesReader`. Validate the complete bounded body
   before allowing mutation so unknown-length/chunked and ignored bodies cannot
   bypass the ceiling. Keep the existing authentication and authorization gates.

2. Consolidate decoding in `internal/handler/api/common.go`. Use
   `DisallowUnknownFields` for closed request structs and require EOF after the
   first value; reject top-level null for object request schemas. Keep current
   field-level validation. Use one stable JSON error response for oversized
   input (413) and another for invalid JSON (400), with no raw decoder details.
   Update every `readJSON` caller so size errors cannot be flattened to 400.
   Route `APITokenHandler.CreateToken` through the same helper.

3. Apply equivalent strict decoding to `LintHandler.LintScript` without creating
   an import cycle (`api` already imports `handler`). Reuse a lower-level helper
   only if it reduces the duplicate implementation. Preserve the read-only
   session/viewer behavior of `/api/lint`. Keep bodyless control actions usable
   with an empty body; reject unexpected nonempty input rather than ignoring it.

4. Validate bounded web form parsing before CSRF and handlers can consume it.
   Return 413 for oversized general forms without mutation, including requests
   with header CSRF tokens and multipart encoding. Preserve existing login and
   reauthentication error contracts. Verify normal script forms remain usable.

5. Add regression coverage using `newAPIHarness`, migrated databases, and the
   real router with API tokens in `internal/handler/api/*_test.go`. Cover known
   and unknown content lengths, limit and limit-plus-one bodies, oversized
   trailing whitespace, unknown top-level and nested fields, multiple values,
   trailing junk, empty/null input, and valid trailing whitespace. Prove rejected
   creates and updates leave database state and successful audit records
   unchanged. Include token creation, nested runbooks, bodyless actions, and a
   valid script request whose entire encoded body is exactly at the ceiling.
   Add web boundary tests under `internal/server` or `internal/handler`.

6. Extend `scripts/e2e_test.sh` with HTTP API and web form contracts, including
   rejected writes and successful large script saves. Update
   `skills/durpdeploy/SKILL.md`, Swagger error documentation in
   `internal/handler/api/docs.go`, and affected route response annotations.
   Regenerate `internal/swagger/spec.json` with the existing target.

7. Run focused tests during implementation. At final verification generate
   templ and Swagger UI assets, run formatting checks, `go vet ./...`,
   `go test -v -count=1 ./...`, and the isolated E2E suite. Complete the required
   correctness, security, and QA review of the implementation. Push fixes and
   monitor CI, SonarCloud, and the requested Codex review until the latest
   implementation passes. Leave merging to the user.

## Risks and decisions for approval

- The proposed ceilings are product policy: 4 MiB for JSON and lint requests,
  16 MiB for general encoded forms. Runbooks share the JSON ceiling across all
  steps. A script's usable size is the ceiling minus its encoded envelope.
- Unknown-field rejection deliberately breaks clients that send extra fields.
  Dynamic map keys remain permitted where the request schema defines a map.
- Empty-body operations remain exceptions to the JSON-document requirement;
  endpoints with declared JSON schemas require a document. Unexpected bodies
  on bodyless operations will become errors.
- Complete bounded validation must happen before mutation. Merely wrapping
  `r.Body` or checking `Content-Length` does not protect all paths.
- Existing authentication, CSRF, viewer gates, and audit semantics must remain
  covered. Password forms retain their existing response status contracts.
- Remote main matched the checkout when this plan was prepared. The open Helm
  ingress and logout/MFA PRs may require checking middleware interactions again
  before implementation is ready to merge.

## Out of scope

No database migrations, runner changes, new upload endpoints, new dependencies,
general JSON Schema framework, agent protocol changes, rate-limit redesign,
or merge operation. This draft does not claim remediation or completed tests.
