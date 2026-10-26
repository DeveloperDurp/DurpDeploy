//go:build e2e && agentstaging

package agentserver_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func stagingEncryptionRelease(
	t *testing.T,
	s *stagingE2E,
) (int64, int64, int64) {
	t.Helper()
	var project, env, release struct{ ID int64 }
	decode := func(data []byte, into any) {
		t.Helper()
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	decode(
		s.api(
			t,
			"POST",
			"/projects",
			map[string]string{"name": "staging-encryption"},
			201,
		),
		&project,
	)
	decode(
		s.api(
			t,
			"POST",
			"/environments",
			map[string]string{"name": "staging-encryption"},
			201,
		),
		&env,
	)
	s.api(
		t,
		"POST",
		"/admin/agents/test-agent/environments",
		map[string]int64{"environment_id": env.ID},
		204,
	)
	base := fmt.Sprintf("/projects/%d", project.ID)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "encryption producer", "script_body": `head -c 33554432 /dev/zero > "$DURPDEPLOY_STAGE_DIR/payload"; echo encryption-producer-ok`,
		"interpreter": "bash", "execution_target": "agent", "agent_execution_mode": "host", "agent_selectors": []string{"test-agent"}, "timeout_seconds": 120,
	}, 201)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "encryption consumer", "script_body": `test "$(stat -c %s "$DURPDEPLOY_STAGE_DIR/payload")" = 33554432 && echo encryption-consumer-ok`,
		"interpreter": "bash", "execution_target": "local", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	decode(
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "v1"},
			201,
		),
		&release,
	)
	return project.ID, env.ID, release.ID
}

func TestDeploymentStagingEncryptionCancellationAPIE2E(t *testing.T) {
	pause := newStagingEncryptionPause(t)
	s := newStagingE2EWithTransfers(t, "", pause.wrap)
	defer pause.release()
	project, env, release := stagingEncryptionRelease(t, s)
	start := func() int64 {
		t.Helper()
		var deployment struct{ ID int64 }
		if err := json.Unmarshal(s.api(t, "POST", fmt.Sprintf("/projects/%d/deployments", project), map[string]int64{"release_id": release, "environment_id": env}, 201), &deployment); err != nil {
			t.Fatal(err)
		}
		return deployment.ID
	}
	id := start()
	var stats stagingEncryptionStats
	select {
	case stats = <-pause.ready:
	case <-time.After(30 * time.Second):
		t.Fatal("agent upload did not reach encryption")
	}
	started := time.Now()
	s.api(t, "POST", fmt.Sprintf("/deployments/%d/cancel", id), nil, 200)
	select {
	case <-pause.done:
	case <-time.After(30 * time.Second):
		t.Fatal("agent cancellation did not close its upload request")
	}
	t.Logf(
		"canceled agent upload with %d spools, smallest=%d bytes; request release=%s",
		stats.Spools,
		stats.Smallest,
		time.Since(started),
	)
	if stats.Smallest < 1<<20 || stats.Smallest > 2<<20 {
		t.Fatalf("agent upload encrypted beyond first chunk: %+v", stats)
	}
	s.wait(t, id, "cancelled")
	key := db.GetStagingSnapshotParams{DeploymentID: id, Producer: "test-agent"}
	if _, err := s.f.repo.Queries.GetStagingSnapshot(t.Context(), key); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("canceled output published: %v", err)
	}
	s.assertTransferPurged(t, "encryption-cancel", id)
	s.assertRuntimeStagingPurged(t)
	// The same immutable release still transfers every byte when not canceled.
	id = start()
	s.wait(t, id, "succeeded")
	logs := string(
		s.api(t, "GET", fmt.Sprintf("/deployments/%d/logs.txt", id), nil, 200),
	)
	if !strings.Contains(logs, "encryption-consumer-ok") {
		t.Fatal("complete uncanceled transfer did not reach downstream")
	}
	s.assertTransferPurged(t, "encryption-retry", id)
	s.assertRuntimeStagingPurged(t)
}
