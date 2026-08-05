# syntax=docker/dockerfile:1
# Multi-stage build for durpdeploy.
# Builder generates templ files, bundles CSS/JS, and compiles a static Go binary.
# Runtime is a minimal Alpine image with a non-root user and bash for step scripts.

# Stage 1: builder
FROM golang:1.26-alpine AS builder

# Install build tooling (npm for Tailwind/esbuild, make for the Makefile, git
# and ca-certificates for Go module proxy / npm registry HTTPS fetches), then
# install the templ CLI pinned to a specific version for reproducible builds.
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates git make npm && \
    go install github.com/a-h/templ/cmd/templ@v0.3.1020

WORKDIR /build

# Copy Go dependency files first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy Node dependency files and install them deterministically before copying
# the full source tree, so node_modules is cached independently of source edits.
COPY package.json package-lock.json ./
RUN npm ci

# Copy the full source tree (views, static, migrations, queries, etc.).
COPY . .

# Generate templ files, build CSS/JS bundles + swagger-ui assets, then
# compile the Go binary in the same layer. CGO_ENABLED=0 is required because
# the project uses modernc.org/sqlite, which is pure Go and does not need
# CGO. -w -s strips debug info and the symbol table; -trimpath removes build
# paths.
#
# ponytail: swagger-ui-copy is the only step that materializes
# static/swagger-ui/ (the //go:embed pattern in static/static.go needs the
# files at build time; the directory is .gitignore'd). tailwind-build and
# js-build do not produce it.
RUN make templ-generate tailwind-build js-build swagger-ui-copy && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -trimpath -o /out/durpdeploy ./cmd/server

# Stage 2: runtime
FROM alpine:3.20

# Install runtime essentials (CA certificates for HTTPS notifications, bash
# because the deployment runner executes step scripts via os/exec and Alpine
# base only provides busybox /bin/sh), then create a non-root user with a
# stable UID. No shell, no home, no password.
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates bash && \
    adduser -D -u 10001 durpdeploy

# Data directory for the SQLite database and WAL files. Chown to the runtime
# user and declare it a volume so it can be mounted from the host.
WORKDIR /data
RUN chown durpdeploy:durpdeploy /data
VOLUME ["/data"]

# Copy the binary from the builder. Keep it owned by root so it cannot be
# tampered with at runtime, and make it world-executable.
COPY --from=builder /out/durpdeploy /usr/local/bin/durpdeploy
RUN chmod 0755 /usr/local/bin/durpdeploy

# Drop to the non-root user for all subsequent instructions and runtime.
USER 10001

# The application listens on port 8080 (hardcoded in cmd/server/main.go).
EXPOSE 8080

# Use ENTRYPOINT so the binary is the fixed executable for subcommands such as
# `admin create`, `audit prune`, and `secret-key rotate`, as well as the default
# HTTP server.
ENTRYPOINT ["/usr/local/bin/durpdeploy"]

# Probe the /login endpoint. It returns a 303 redirect when the server is alive,
# which is enough for an orchestrator to consider the container healthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["wget", "-q", "-O", "/dev/null", "http://localhost:8080/login"]
