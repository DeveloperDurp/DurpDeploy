//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestPendingLogLimitCancelsContainerE2E(t *testing.T) {
	// Given: a container emits an over-budget newline-free stream and then waits.
	f := newArtifactE2E(t)
	cleanupArtifactRuntime(t, f.h)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "newline-free", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"script_body": "head -c 1048577 /dev/zero | tr '\\000' X\nsleep 60\n",
	}, 201)
	var release db.Release
	if err := json.Unmarshal(
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": "log-limit"},
			201,
		),
		&release,
	); err != nil {
		t.Fatal(err)
	}
	// When: the public API starts the deployment.
	var deployment db.Deployment
	if err := json.Unmarshal(
		f.api(
			t,
			"POST",
			f.base()+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
			},
			201,
		),
		&deployment,
	); err != nil {
		t.Fatal(err)
	}
	// Then: the attempt fails without waiting for sleep, drops raw output, and cleans up.
	logs := awaitStagingTerminal(
		t,
		f,
		fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
		"failed",
	)
	if !strings.Contains(
		string(logs),
		"pending log output exceeded safety limit",
	) ||
		strings.Contains(string(logs), "XXXXXXXXXX") {
		t.Fatalf("unsafe pending-output outcome: %s", logs)
	}
	filter := "--filter=label=io.durpdeploy.namespace=" + os.Getenv(
		"DURPDEPLOY_CONTAINER_RUNTIME",
	) + ":" + os.Getenv(
		"DURPDEPLOY_CONTAINER_NAMESPACE",
	)
	if containers := stagingRuntime(
		t,
		"ps",
		"--all",
		"--quiet",
		filter,
	); containers != "" {
		t.Fatalf("attempt left containers: %s", containers)
	}
}
