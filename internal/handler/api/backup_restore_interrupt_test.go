//go:build e2e && backuprestore && linux

package api_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

// The shell acceptance test terminates this fixture while work is active.
func TestBackupRestoreInterruptFixture(t *testing.T) {
	if os.Getenv("DURPDEPLOY_RESTORE_INTERRUPT_FIXTURE") != "1" {
		t.Skip("run scripts/test-backup-restore-cleanup.sh")
	}
	started := time.Now()
	f := newRestoreFixture(t)
	p := f.start(t)
	f.login(t)
	f.seed(t)
	backup := f.backup(t)
	var watermark struct{ ID int64 }
	decodeStepLogTest(t, f.api.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "backup-watermark"}, 201), &watermark)
	backup.awaitFresh(t, watermark.ID)
	f.api.api(t, "POST", f.api.base()+"/steps", map[string]any{
		"name":             "Interrupted work",
		"script_body":      "echo interrupt-ready; sleep 1800",
		"interpreter":      "bash",
		"execution_target": "local",
		"container_image":  "harbor.durp.info/dockerhub/library/bash:5.2",
		"timeout_seconds":  1800,
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api.api(t, "POST", f.api.base()+"/releases",
		map[string]string{"version": "interrupt-v2"}, 201), &release)
	var deployment db.Deployment
	decodeStepLogTest(t, f.api.api(t, "POST", f.api.base()+"/deployments",
		map[string]int64{
			"release_id": release.ID, "environment_id": f.api.environment.ID,
		}, 201), &deployment)
	deadline := time.Now().Add(30 * time.Second)
	for {
		logs := f.api.api(t, "GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID), nil, 200)
		if strings.Contains(string(logs), "interrupt-ready") {
			break
		}
		if time.Now().After(deadline) {
			status := f.api.api(t, "GET", fmt.Sprintf(
				"/api/v1/deployments/%d/status", deployment.ID,
			), nil, 200)
			t.Fatalf(
				"interruption fixture did not start its container work: status=%s logs=%s",
				status,
				logs,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
	deadline, ok := t.Deadline()
	if !ok || time.Until(deadline) < 30*time.Second {
		t.Fatal("interruption fixture requires 30 seconds of timeout headroom")
	}
	t.Logf("restore-interrupt-timing startup=%s headroom=%s",
		time.Since(started).Round(time.Millisecond),
		time.Until(deadline).Round(time.Millisecond))
	t.Logf(
		"restore-interrupt-ready pid=%d namespace=%s:%s session=%s",
		p.command.Process.Pid,
		os.Getenv(
			"DURPDEPLOY_CONTAINER_RUNTIME",
		),
		os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE"),
		os.Getenv("TESTCONTAINERS_SESSION_ID"),
	)
	<-t.Context().Done()
}
