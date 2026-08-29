# Mobile browser CI

`mobile:browser` is a serialized GitLab test job with
`resource_group: mobile-browser`. It builds `Dockerfile.mobile-browser` and
runs `/usr/local/bin/mobile-browser-container`. The ordinary Alpine lint,
test, and build jobs do not run this suite.

The image stays pinned to `mcr.microsoft.com/playwright:v1.61.1-noble`, which
matches the committed Playwright 1.61.1 lockfile. It copies Go 1.26, installs
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
add it to the Make target, container entrypoint, or GitLab job.

## Evidence and receipts

The Go test creates `.omo/evidence` itself with mode `0700` and writes its
receipt with mode `0600`. A direct successful run writes the default receipt
to `.omo/evidence/task-3-mobile-readability-receipt.json`. That ignored path is
an output, not a necessary checkout artifact.

The container supplies a run-specific receipt name, then copies it on exit to
`/artifacts/<run-id>/mobile-readability-<run-id>.json`. The harness also writes
its JSON reports and PNG screenshots below that run directory. Local Docker
runs export it as `artifacts/mobile/<run-id>/`. GitLab copies `/artifacts` and
uploads `artifacts/mobile/` with `when: always` for one week. The container
does not copy source or evidence into an image layer.

## GitLab Docker-in-Docker

`mobile:browser` uses `docker:24` with `docker:24-dind`, the `docker` service
alias, `--tls=false`, `DOCKER_TLS_CERTDIR=""`, and
`DOCKER_HOST=tcp://docker:2375`. The job waits up to 30 seconds for
`docker info` before building and creating the test container. If the daemon
stops during cleanup, `after_script` records the diagnostic. Thus, it prevents
a misleading secondary artifact-copy failure.

The runner needs a privileged Docker executor, or a Kubernetes runner that can
run privileged pods and services. GitLab copies the checkout into a created
container because Docker-in-Docker cannot bind-mount the job checkout into the
service daemon. It then starts the shared entrypoint, copies `/artifacts`, and
removes the container.

## Static contract check

Run the lightweight contract check after changing the image, entrypoint,
strictness wiring, receipt creation, Make target, or GitLab job:

```bash
bash scripts/check-mobile-browser-container-contract.sh
```

It checks deterministic templ installation and generation, the exact strict
tagged-test path, explicit baseline behavior, Go-side evidence creation, and
the shared Docker wiring. It does not build an image or run Chromium.
