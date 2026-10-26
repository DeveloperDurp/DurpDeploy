//go:build e2e

package api_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func stagingCleanupDiagnosticE2E(t *testing.T) (*artifactE2E, db.Deployment) {
	t.Helper()
	binary := fakePodman(t)
	cli, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	cli = []byte(strings.Replace(string(cli), `case "$3" in`, `case "$3" in
volume) if [ "$4" = rm ]; then printf 'volume is in use cleanup-secret-1484\n'; exit 7; fi;;`, 1))
	if err := os.WriteFile(binary, cli, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newArtifactE2E(t, binary)
	f.api(t, "POST", f.base()+"/variables", map[string]any{
		"name": "TOKEN", "value": "cleanup-secret-1484", "secret": true,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Cleanup diagnostic", "script_body": "true",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "cleanup-diagnostic"}, 201), &release)
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	), &deployment)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	deadline := time.Now().Add(10 * time.Second)
	for {
		decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &deployment)
		if deployment.Status == "cleanup_unconfirmed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup status=%s", deployment.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs := string(f.api(t, "GET", path+"/logs.txt", nil, 200))
	if !strings.Contains(logs, "remove staging volume") ||
		!strings.Contains(logs, "volume is in use [REDACTED]") ||
		strings.Contains(logs, "cleanup-secret-1484") {
		t.Fatal("API cleanup export lost its reason or exposed a secret")
	}
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(page, "volume is in use [REDACTED]") ||
		strings.Contains(page, "cleanup-secret-1484") {
		t.Fatal("web cleanup export lost its reason or exposed a secret")
	}
	return f, deployment
}

func TestDeploymentStagingCleanupDiagnosticAPIWebE2E(t *testing.T) {
	stagingCleanupDiagnosticE2E(t)
}
