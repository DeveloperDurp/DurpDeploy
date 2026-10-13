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
	f := newRestoreFixture(t)
	p := f.start(t)
	f.login(t)
	f.seed(t)
	f.replicate(t)
	f.api.api(t, "POST", f.api.base()+"/steps", map[string]string{
		"name": "Interrupted work", "script_body": "echo interrupt-ready; sleep 300",
		"interpreter": "bash", "execution_target": "local",
		"container_image": "docker.io/library/bash:5.2",
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
			t.Fatalf(
				"interruption fixture did not start its container work: %s",
				logs,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
