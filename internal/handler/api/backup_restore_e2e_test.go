//go:build e2e && backuprestore && linux

package api_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestBackupRestoreAPIWebE2E(t *testing.T) {
	f := newRestoreFixture(t)
	process := f.start(t)
	f.login(t)
	f.seed(t)
	f.api.api(t, "GET", f.api.base()+"/variables", nil, 200)
	before := f.deploy(t)
	f.assertDelivery(t, before.ID)
	releasePath := fmt.Sprintf("%s/releases/%d", f.api.base(), f.releaseID)
	releaseBefore := f.api.api(t, "GET", releasePath, nil, 200)
	logsPath := fmt.Sprintf("/api/v1/deployments/%d/logs", before.ID)
	logsBefore := f.api.api(t, "GET", logsPath, nil, 200)
	var schedule db.ScheduledDeployment
	decodeStepLogTest(t, f.api.api(t, "POST", f.api.base()+"/schedules",
		map[string]any{
			"release_id": f.releaseID, "environment_id": f.api.environment.ID,
			"cron": "* * * * *", "enabled": false, "note": "restore-schedule",
		}, 201), &schedule)

	replica := f.backup(t)
	var watermark struct{ ID int64 }
	decodeStepLogTest(t, f.api.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "backup-watermark"}, 201), &watermark)
	// Restore probes must observe this final committed API write.
	replica.awaitFresh(t, watermark.ID)
	process.stop(t)
	replica.recover(t)
	t.Log("replica recovered the final write after source database loss")
	f.assertWrongKey(t)

	restarted := f.start(t)
	f.login(t)
	f.assertRestored(t, schedule)
	f.assertDeliveryHistory(t, before.ID)
	if after := f.api.api(t, "GET", releasePath, nil, 200); !bytes.Equal(
		releaseBefore,
		after,
	) {
		t.Fatal("restore changed the release snapshot")
	}
	if after := f.api.api(t, "GET", logsPath, nil, 200); !bytes.Equal(
		logsBefore,
		after,
	) {
		t.Fatal("restore changed historical logs")
	}
	// Execute the recovered release with its original encrypted snapshot value.
	after := f.deploy(t)
	f.assertDelivery(t, after.ID)
	t.Log("restored login, secret execution, history, and SMTP delivery passed")
	f.exerciseSchedule(t, schedule)
	f.assertNewWrites(t, watermark.ID)
	restarted.stop(t)
	t.Log("restored scheduler executed once and the schedule is disabled")
}

func (f *restoreFixture) seed(t *testing.T) {
	t.Helper()
	var project, environment struct{ ID int64 }
	decodeStepLogTest(t, f.api.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Recovery drill"}, 201), &project)
	f.api.project.ID = project.ID
	decodeStepLogTest(t, f.api.api(
		t,
		"POST",
		"/api/v1/environments",
		map[string]string{
			"name": "Recovery environment",
		},
		201,
	), &environment)
	f.api.environment.ID = environment.ID
	f.api.api(t, "PUT", f.api.base()+"/notifications", map[string]string{
		"notify_emails": "recipient@example.test",
	}, 200)
	f.api.api(t, "POST", f.api.base()+"/variables", map[string]any{
		"name": "RESTORE_SECRET", "value": f.secret, "secret": true,
	}, 201)
	f.api.api(t, "POST", f.api.base()+"/variables", map[string]string{
		"name": "RESTORE_PUBLIC", "value": restorePublicValue,
	}, 201)
	script := fmt.Sprintf(`set -eu
printf '%%s' "$RESTORE_SECRET" | sha256sum | grep -q '^%x '
printf 'restore-execution-ok\n'
`, sha256.Sum256([]byte(f.secret)))
	f.api.api(t, "POST", f.api.base()+"/steps", map[string]any{
		"name": "Recovered secret", "script_body": script,
		"interpreter": "bash", "execution_target": "local",
		"container_image": "docker.io/library/bash:5.2",
		"variable_names":  []string{"RESTORE_SECRET"},
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api.api(t, "POST", f.api.base()+"/releases",
		map[string]string{"version": "recovery-v1"}, 201), &release)
	f.releaseID = release.ID
}

func (f *restoreFixture) deploy(t *testing.T) db.Deployment {
	t.Helper()
	var deployment db.Deployment
	decodeStepLogTest(t, f.api.api(t, "POST", f.api.base()+"/deployments",
		map[string]int64{
			"release_id": f.releaseID, "environment_id": f.api.environment.ID,
		}, 201), &deployment)
	f.assertExecution(t, deployment.ID)
	return deployment
}

func (f *restoreFixture) assertExecution(t *testing.T, id int64) {
	t.Helper()
	logs := awaitStagingTerminal(t, f.api,
		fmt.Sprintf("/api/v1/deployments/%d", id), "succeeded")
	if !strings.Contains(string(logs), "restore-execution-ok") ||
		strings.Contains(string(logs), f.secret) {
		t.Fatal("recovered secret execution or log redaction failed")
	}
}
