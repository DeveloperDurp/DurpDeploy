#!/usr/bin/env bash
set -euo pipefail
# Real API/web flow with an HTTPS fixture and the configured runtime endpoint.
# Requires Docker/Podman and a non-loopback interface.
templ generate
make swagger-ui-copy
go test -tags=e2e -count=1 -run '^(TestArtifactsAPIWebContainerE2E|TestArtifactCredentialRotationE2E|TestArtifactSourceReplacementE2E|TestDeploymentStagingCleanupE2E)$' ./internal/handler/api
