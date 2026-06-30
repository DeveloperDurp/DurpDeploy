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
- **Deployment runner** (`internal/runner/runner.go`) runs bash steps sequentially via `os/exec`, streams logs through `LogBroker` (SSE). Tracks in-flight cancels in a `map[int64]context.CancelFunc` keyed by deployment ID. No parallel step execution, no auth, bash only.
- **Releases are immutable snapshots** of steps + variables (stored as `steps_json`); a release does not track later edits to steps/variables. Refresh endpoint re-snapshots.
- Templ tags render to HTMX swaps; handlers return 303 for POST redirects, 422 for validation failures (see `e2e_test.sh` for the contract).

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