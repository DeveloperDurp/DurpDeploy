# AGENTS.md

Single-binary Go app: deploy bash scripts against environments with live SSE logs.
Stack: Go 1.25 + chi + SQLite (modernc.org/sqlite, pure Go, no CGO) + sqlc + goose + templ + HTMX + Alpine + Tailwind/DaisyUI.

## Critical build gotcha

`*_templ.go` are **gitignored** (see `.gitignore`). A fresh checkout cannot `go vet`/`go test`/`go build` until templ runs:

```bash
templ generate        # required before any go command on a fresh checkout
npm install           # required only if regenerating css/js bundles
make build            # templ-generate -> tailwind-build -> js-build -> go build
```

CI runs `templ generate` as `before_script` for every stage — mirror that for any local `go vet`/`go test`/`go build` after pulling or after editing `views/*.templ`.

## Generated code — do not hand-edit

- `internal/db/*` is sqlc output. Edit `queries/*.sql`, then `sqlc generate` (config in `sqlc.yaml`). `internal/repository/repository.go` is hand-written and wraps `*db.Queries`.
- `*_templ.go` is templ output. Edit `views/*.templ`, run `templ generate`.
- `static/css/tailwind.min.css` and `static/js/app.bundle.js` are built by `make tailwind-build` / `make js-build` and **are committed**; `go build` works without npm. Only regenerate them when CSS/JS source changes.

## Migrations

`migrations/*.sql` are goose-formatted (`-- +goose Up` / `-- +goose Down`) and embedded via `migrations/embed.go`. Next file: `003_*.sql`. Migrations **auto-run on startup** via `internal/migrate/migrate.Run` — no manual step. Tests use `:memory:` SQLite + `migrate.Run`.

## Running / testing

```bash
./durpdeploy                       # listens on :8080 (hardcoded), creates durpdeploy.db in CWD
go test -v -count=1 ./...          # CI's exact command
go test -run TestName ./internal/handler/...   # single test, single package
./e2e_test.sh                      # bash end-to-end: builds, runs server, curl happy/cancel/validation paths (~10s+)
```

CI stage order: `lint` (`go vet ./...` + `gofmt -l .` must be empty) → `test` → `build`. Go fails CI if any file isn't gofmt'd.

## Conventions agents get wrong

- **Add routes in `internal/server/server.go` only.** All chi routes are registered there; handlers live in `internal/handler/*`.
- **`internal/handler/logs_test.go` has its own inline SQL schema** (stale — missing `step_templates`). New tests should use `migrate.Run(":memory:?_pragma=foreign_keys(1)")` like `internal/db/smoke_test.go`, not duplicate the schema inline.
- **DSN is fixed** in `cmd/server/main.go`: `durpdeploy.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`. Database file, WAL, and SHM are gitignored.
- **Deployment runner** (`internal/runner/runner.go`) runs bash steps sequentially via `os/exec`, streams logs through `LogBroker` (SSE). Tracks in-flight cancels in a `map[int64]context.CancelFunc` keyed by deployment ID. **Auth is at the HTTP boundary, not the runner** — the runner still executes as the server's user; per-step sandbox lands in P1-3. No parallel step execution, bash only.
- **Releases are immutable snapshots** of steps + variables (stored as `steps_json`); a release does not track later edits to steps/variables. Refresh endpoint re-snapshots.
- Templ tags render to HTMX swaps; handlers return 303 for POST redirects, 422 for validation failures (see `e2e_test.sh` for the contract).

## Viewer role — UI gating pattern

Viewers (`role = "viewer"`) are read-only. Defense is **two layers**, both required:

1. **CSRF middleware** (`internal/auth/csrf.go`) — rejects any state-changing request from a viewer at the protocol layer.
   - HTMX requests: returns `200` + `HX-Trigger` carrying a `makeToast` event (so the same red toast the rest of the app uses fires, and the page stays put — no error overlay, no full reload).
   - Non-HTMX form submits: returns `403` + a self-contained styled page (back link, no broken browser error screen).
2. **CanWrite templ guard** — hides write affordances so viewers never have a reason to hit the middleware.
   - Helper: `pages.CanWrite(ctx)` (and the parallel `components.canWrite(ctx)` for the components package). Returns `false` for viewers.
   - Buttons: wrap each write affordance in `if CanWrite(ctx) { <button>...</button> }`. Affects New / Edit / Delete / Reorder / Save Template / Approve / Cancel / Re-run / Toggle buttons across every page.
   - Form pages: wrap the entire form in `if CanWrite(ctx) { @FormBody(...) } else { @ViewerForbiddenMessage(...) }`. The `ViewerForbiddenMessage` is in `views/pages/permissions.templ` and takes `(title, action, what, backHref, currentPath)`.

**Why both layers?** The CSRF middleware is the security boundary (always fires on the actual write attempt). The CanWrite templ guard is the UX layer (so a viewer never sees a useless "New Project" form, never clicks a button that would just toast-block them). Skipping the templ guard leaves working dead-end UI; skipping the middleware leaves a security hole. Both are required.

**What about routes that are already admin-gated or project-gated?** Those still get the CanWrite guard for defense in depth. For example, `/admin/users/*` is gated by `RequireRole("admin")` so a viewer never reaches the form; the CanWrite guard is belt-and-suspenders. For routes a viewer can actually reach (`/environments/new`, `/lifecycles/new`, `/templates/new`), the CanWrite guard IS the defensive layer — without it, a viewer would see a working-looking form that toasts on submit.

**Pattern for new form pages:** the `UserFormPage`, `ProjectFormPage`, `EnvironmentForm`, `LifecycleFormPage`, `TemplateForm`, `ScheduledDeploymentFormPage`, `DeployFormPage`, and the `StepForm`/`StepEditRow` components are the reference. Each follows the same shape: outer `if !CanWrite(ctx) { @ViewerForbiddenMessage(...) } else { @Base + @FormBody }`.

**Tests:** `TestAuth_ViewerHTMXReturnsToast` (HTMX toast path), `TestAuth_ViewerCannotPost` (non-HTMX 403 path), `TestUsers_ViewerSeesForbiddenOnFormPages` (templ guard renders the message on /environments/new, /lifecycles/new, /templates/new).

## Security model

P0 shipped a multi-user model that is **not optional**. Every protected route in `internal/server/server.go` goes through three middleware, in this order, on the protected group:

1. `auth.AuthMiddleware(repo)` — session cookie → user in context. Redirects to `/login` on miss.
2. `auth.CSRFMiddleware()` — rejects POST/PUT/PATCH/DELETE without a valid CSRF token, and rejects any `viewer`-role user (read-only by design). Viewer rejections are surfaced as a toast (HTMX) or a styled 403 page (non-HTMX) — see "Viewer role — UI gating pattern" below.
3. `audit.Middleware(repo)` — records every *successful* state change to `audit_log`. CSRF rejections and 4xx responses are **not** audited (intentional — failed logins should not be enumerable).

New state-changing routes **must** be added to the `actionMap` in `internal/audit/audit.go` so the audit log gets a stable action name. The fallback heuristic (method + first path segment) is lossy.

**What we defend against:** unauthenticated deploys, CSRF on a teammate's browser, replay-with-stolen-CSRF (tokens are per-session random 16 bytes), password DB leak (argon2id), accidental writes by a viewer (UI gating + CSRF gate). See `docs/attack-drill.md` for the live drill.

**What we defend against (P0 + P1):**
- ~~Per-project authorization~~ — **shipped (P1-1).**
- ~~Admin users management~~ — **shipped (P1-2).**
- ~~Secret encryption at rest~~ — **shipped (P1-3).**
- ~~Runner sandboxing~~ — **shipped (P1-4).**
- Log redaction — **P1-5 (next).** Naive string replacement is in; regex-based scrubber is next.

Until P1 lands, the practical threat model is "the same access as you" — a malicious authenticated teammate has the same power as the operator. That is the contract for a small-team tool. See `docs/roles.md` for the role matrix and `docs/attack-drill.md` for the concrete failure modes.

**Do not "simplify" by:** removing the CSRF check, inlining `VerifyPassword`, replacing argon2id with bcrypt/SHA, putting the session token in a URL parameter, skipping the `audit_log` insert on a new route, or merging `audit.Record` into a handler without going through the middleware. Each of these is a documented attack vector in `docs/attack-drill.md`.

## UI design — table layout conventions

All pages with data tables **must** follow this pattern. Tables scale with the browser window and have consistent column widths across pages.

**Table base class**: `table table-zebra table-fixed w-full`

- `table-fixed` makes columns respect percentage widths (without it, browser auto-sizes to content)
- `w-full` makes the table fill its container
- Custom CSS in `static/css/input.css` enforces `width: 100% !important; table-layout: fixed` on all `.table` elements

**Column width pattern** — use percentage widths on `<th>` so columns scale with window:

| Columns | Pattern |
|---|---|
| 3 cols | `w-1/4` + `w-auto` + `w-1/4` (or fixed for last) |
| 4 cols | `w-1/5` + `w-2/5` + `w-1/5` + `w-1/5` |
| 6 cols | `w-1/6` × 6 |
| Steps (Order, Name, Script) | `w-16` + `w-1/5` + `w-auto` |
| Steps (Order, Name, Script, Actions) | `w-16` + `w-1/5` + `w-auto` + `w-96` |

**Actions column**: `w-96` (24rem) for tables with 5 buttons (↑, ↓, Edit, Save Template, Delete), `w-48` (12rem) for tables with 2 buttons.

**Cell overflow**: Add `truncate` to `<td>` cells with text content. Add `whitespace-nowrap` to date/timestamp cells. Buttons in actions cells use `whitespace-nowrap` to stay in one row.

**Layout container**: Use `w-full px-4 sm:px-6 lg:px-8` (no `max-w-screen-*` — tables fill the full viewport). Never use Tailwind's `container` class for the main content area.

**Button text in tables**: Keep short to fit one row. "Save as Template" → "Save Template". Use `btn-xs` for table row buttons.

## Ponytail (lazy senior) rules — active by default

Lazy = efficient, not careless. Read the whole flow first, then pick the highest rung that holds:

1. **Does this need to exist at all?** Speculative need = skip it, say so in one line. (YAGNI)
2. **Already in this codebase?** Reuse the helper/util/type/pattern a few files over. Re-implementing what's here is the most common slop.
3. **Stdlib does it?** Use it. (`database/sql.NullString`, `slog`, `embed`, `os/exec`, `net/http`)
4. **Native platform feature covers it?** DB constraint over app code; chi middleware over hand-rolled; HTMX attributes over JS.
5. **Already-installed dependency solves it?** Use it. Never add a new dep for what a few lines can do.
6. **Can it be one line?** One line.
7. **Only then:** the minimum code that works.

Rules:

- No unrequested abstractions: no interface with one impl, no factory for one product, no config for a value that never changes.
- No boilerplate, no scaffolding "for later" — later can scaffold for itself.
- Deletion over addition. Boring over clever (clever is what someone decodes at 3am).
- Fewest files possible. Shortest working diff wins — **but only after understanding the problem**. The smallest change in the wrong place isn't lazy, it's a second bug.
- **Bug fix = root cause, not symptom.** Before editing, grep every caller of the function. One guard in the shared function beats a guard in every caller; patching only the path named in the ticket leaves siblings broken.
- Mark deliberate simplifications with a `// ponytail:` comment naming the ceiling and upgrade path, e.g. `// ponytail: global lock, per-deployment locks if throughput matters`.
- Complex request? Ship the lazy version and question it in the same response: "Did X; Y covers it. Need full X? Say so." Never stall on an answer you can default.
- Two stdlib options, same size? Take the one correct on edge cases. Lazy = less code, not flimsier algorithm.
- Output: code first, then at most three short lines — what was skipped, when to add it. No essays, no feature tours.

Never lazy away: input validation at trust boundaries (HTTP handlers, `e2e_test.sh` contract), error handling that prevents data loss (release snapshots, deployment cancel), security, accessibility basics, anything explicitly requested.

Never lazy about understanding the problem — the ladder shortens the solution, never the reading. Trace the whole flow end to end first, then climb.