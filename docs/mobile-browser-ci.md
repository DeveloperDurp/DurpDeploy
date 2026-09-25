# Mobile browser CI

`mobile-browser` is a serialized test job in
`.github/workflows/ci.yml` with a `ci-mobile-browser` concurrency
group. It builds `Dockerfile.mobile-browser` and runs
`/usr/local/bin/mobile-browser-container`. The ordinary lint, test,
and build jobs in `release.yml` do not run this suite.

The image stays pinned to `mcr.microsoft.com/playwright:v1.61.1-noble`, which
matches the committed Playwright 1.61.1 lockfile. It copies Go 1.26.8, installs
`templ@v0.3.1020` with `GOBIN=/usr/local/bin`, runs `npm ci`, and supplies the
locked Chromium and its OS dependencies. Keep the image and lockfile versions
aligned when changing Playwright.

## Run the tagged test on the host

The direct tagged test runs on the host, not in Docker:

```bash
go test -tags=mobilebrowser -run '^TestMobileBrowserReadability$' -count=1 -v ./internal/handler
```

Before running it on a fresh checkout, the host needs Go, the `templ` CLI,
Node, `node_modules/playwright`, Playwright's downloaded Chromium, generated
templ output, and generated Swagger UI assets. For example:

```bash
npm ci
templ generate
make swagger-ui-copy
npx playwright install chromium
go test -tags=mobilebrowser -run '^TestMobileBrowserReadability$' -count=1 -v ./internal/handler
```

The test fails if Node, the locked Playwright package, or its Chromium
executable is unavailable. It does not skip and does not fall back to a system
browser. Docker is not a prerequisite for this direct command.

## Run the shared Docker path

Run the same suite with container-provided Go tooling, Node dependencies,
Chromium, templ generation, and Swagger assets:

```bash
make mobile-browser-container
```

This command needs Docker and a running Docker daemon on the host. It does not
need host Node, host Playwright, host Chromium, or a host templ installation.
It builds `durpdeploy-mobile-browser:local`, bind-mounts the checkout at
`/workspace`, and writes exported results to `artifacts/mobile/local-*/`.
Set `MOBILE_BROWSER_IMAGE` or `MOBILE_BROWSER_RUN_ID` only to choose an image
tag or a distinct artifact directory.

## Strict checks and baseline diagnostics

The normal Go test always sets `MOBILE_STRICT=1`. Strict mode is the contract:
missing selectors, unreadable geometry, and interaction failures fail the test.

`MOBILE_BASELINE=1` is an explicit harness diagnostic only. The harness treats
it as a baseline when `MOBILE_STRICT` is anything other than `"1"`, including
when it is absent or `"0"`. The normal tagged test sets it to `"1"`. Use it
only while you diagnose the harness with its necessary environment inputs. Do not
add it to the Make target, container entrypoint, or CI job.

## Evidence and receipts

The Go test creates `.omo/evidence` itself with mode `0700` and writes its
receipt with mode `0600`. A direct successful run writes the default receipt
to `.omo/evidence/task-3-mobile-readability-receipt.json`. That ignored path is
an output, not a necessary checkout artifact.

The container supplies a run-specific receipt name, then copies it on exit to
`/artifacts/<run-id>/mobile-readability-<run-id>.json`. The harness also writes
its JSON reports and PNG screenshots below that run directory. Local Docker
runs export it as `artifacts/mobile/<run-id>/`. CI copies `/artifacts` and
uploads `artifacts/mobile/` with `if: always()` for seven days. The container
does not copy source or evidence into an image layer.

## CI runner Docker

`mobile-browser` runs on a GitHub-hosted `ubuntu-latest` runner whose docker
daemon runs on the runner host, so there is no Docker-in-Docker service, no
`DOCKER_HOST`/TLS plumbing, and only a one-line `docker info` guard before
building. The job copies the checkout into a created container instead of
bind-mounting it, starts the shared entrypoint, copies `/artifacts` back, and
removes the container.

## Static contract check

Run the lightweight contract check after changing the image, entrypoint,
strictness wiring, receipt creation, Make target, or CI job:

```bash
bash scripts/check-mobile-browser-container-contract.sh
```

It checks deterministic templ installation and generation, the exact strict
tagged-test path, explicit baseline behavior, Go-side evidence creation, and
the shared Docker wiring. It does not build an image or run Chromium.
