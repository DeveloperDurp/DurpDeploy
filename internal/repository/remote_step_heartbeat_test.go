package repository_test

import (
	"bytes"
	"testing"

	"durpdeploy/internal/repository"
)

func TestRemoteStepHeartbeatRollsBackWhenAgentHealthCannotUpdate(t *testing.T) {
	repo := remoteFixture(t)
	tokenHash := bytes.Repeat([]byte{1}, 32)
	if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
		(deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,
		 claim_expires_at,last_heartbeat_at,started_at,updated_at)
		VALUES (1,0,'a','started',?,'ciphertext',200,1,1,1)`, tokenHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(
		"UPDATE agents SET status='disabled' WHERE id='a'",
	); err != nil {
		t.Fatal(err)
	}
	_, handled, err := repo.HeartbeatRemoteStep(
		t.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID: 1, AgentID: "a", ClaimTokenHash: tokenHash,
		},
	)
	if err == nil || !handled {
		t.Fatalf("heartbeat handled=%v error=%v", handled, err)
	}
	var lastHeartbeat int64
	if err := repo.DB.QueryRow(`SELECT last_heartbeat_at FROM remote_step_runs
		WHERE deployment_id=1 AND step_index=0 AND agent_id='a'`).
		Scan(&lastHeartbeat); err != nil {
		t.Fatal(err)
	}
	if lastHeartbeat != 1 {
		t.Fatalf(
			"remote heartbeat committed without agent health: %d",
			lastHeartbeat,
		)
	}
}
