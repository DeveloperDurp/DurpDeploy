package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
)

func TestPostgres_RemoteAgentRuntimeParity(t *testing.T) {
	testRemoteAgentRuntimeParity(t, postgresRuntimeParityDSN(t))
}

func TestMSSQL_RemoteAgentRuntimeParity(t *testing.T) {
	testRemoteAgentRuntimeParity(t, sqlServerRuntimeParityDSN(t))
}

func testRemoteAgentRuntimeParity(t *testing.T, dsn string) {
	t.Helper()
	fixture := newRuntimeParityFixture(t, dsn)
	fixture.activate(t, "agent-a", fixture.agentIdentity)
	runtimeAddEligibleAgent(t, fixture, "agent-a")

	t.Run("sequenced-log", func(t *testing.T) {
		deploymentID, token := claimRuntimeDeployment(t, fixture, "log")
		if response := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"start",
			claimBody(token),
		); response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"start status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
		logs := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"logs",
			`{"protocol":"agent/1","claim_token":"`+token+`","events":[{"sequence":2,"line":"two"},{"sequence":1,"line":"one"},{"sequence":2,"line":"two"}]}`,
		)
		if logs.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"logs status = %d, want %d",
				logs.StatusCode,
				http.StatusNoContent,
			)
		}
		assertLogSequences(t, fixture, deploymentID, []int64{1, 2})
		if response := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"result",
			`{"protocol":"agent/1","claim_token":"`+token+`","state":"succeeded"}`,
		); response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"result status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		deploymentID, token := claimRuntimeDeployment(t, fixture, "cancel")
		if response := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"start",
			claimBody(token),
		); response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"start status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
		if _, err := fixture.repo.Queries.RequestDeploymentDispatchCancellation(
			context.Background(),
			db.RequestDeploymentDispatchCancellationParams{
				CancelRequestedAt: sql.NullInt64{
					Int64: fixture.now.Unix(),
					Valid: true,
				},
				DeploymentID: deploymentID,
				CurrentState: "started",
			},
		); err != nil {
			t.Fatalf("request cancellation: %v", err)
		}
		heartbeat := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"heartbeat",
			claimBody(token),
		)
		var body agentproto.HeartbeatResponse
		if err := json.NewDecoder(heartbeat.Body).
			Decode(&body); err != nil || heartbeat.StatusCode != http.StatusOK ||
			!body.CancelRequested {
			t.Fatalf(
				"heartbeat cancellation = status %d body %#v error %v",
				heartbeat.StatusCode,
				body,
				err,
			)
		}
		if response := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"cancelled",
			claimBody(token),
		); response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"cancel acknowledgement status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
		assertDispatchState(t, fixture, deploymentID, "cancelled")
	})

	t.Run("lost", func(t *testing.T) {
		deploymentID, token := claimRuntimeDeployment(t, fixture, "lost")
		if response := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"start",
			claimBody(token),
		); response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"start status = %d, want %d",
				response.StatusCode,
				http.StatusNoContent,
			)
		}
		fixture.now = fixture.now.Add(agentproto.LostThreshold)
		if err := fixture.listener.Maintain(context.Background()); err != nil {
			t.Fatalf("maintain: %v", err)
		}
		if err := fixture.listener.Maintain(context.Background()); err != nil {
			t.Fatalf("repeat maintain: %v", err)
		}
		late := fixture.lifecycle(
			t,
			fixture.agentIdentity,
			deploymentID,
			"result",
			`{"protocol":"agent/1","claim_token":"`+token+`","state":"failed"}`,
		)
		if late.StatusCode != http.StatusConflict {
			t.Fatalf(
				"late result status = %d, want %d",
				late.StatusCode,
				http.StatusConflict,
			)
		}
		assertDispatchState(t, fixture, deploymentID, "lost")
		assertDeploymentStatus(t, fixture, deploymentID, "failed")
	})

	t.Run("claim-race", func(t *testing.T) {
		secondIdentity := loadTestIdentity(t)
		fixture.activate(t, "agent-b", secondIdentity)
		runtimeAddEligibleAgent(t, fixture, "agent-b")
		deploymentID := runtimeCreateWaitingDeployment(t, fixture, "race")
		start := make(chan struct{})
		results := make(chan int, 2)
		var group sync.WaitGroup
		for index, agentID := range []string{"agent-a", "agent-b"} {
			group.Add(1)
			go func(index int, agentID string) {
				defer group.Done()
				<-start
				results <- runtimeClaimStatus(fixture, deploymentID, agentID, "race-"+string(rune('a'+index)))
			}(index, agentID)
		}
		close(start)
		group.Wait()
		close(results)
		claims := 0
		for status := range results {
			if status == http.StatusOK {
				claims++
			}
		}
		if claims != 1 {
			t.Fatalf("successful concurrent claims = %d, want 1", claims)
		}
	})
}

func claimRuntimeDeployment(
	t *testing.T,
	fixture *pollFixture,
	payload string,
) (int64, string) {
	t.Helper()
	deploymentID := runtimeCreateWaitingDeployment(t, fixture, payload)
	token := "runtime-parity-token-" + payload
	if status := runtimeClaimStatus(
		fixture,
		deploymentID,
		"agent-a",
		token,
	); status != http.StatusOK {
		t.Fatalf("claim status = %d, want %d", status, http.StatusOK)
	}
	return deploymentID, token
}

func runtimeAddEligibleAgent(
	t *testing.T,
	fixture *pollFixture,
	agentID string,
) {
	t.Helper()
	if _, err := fixture.repo.DB.ExecContext(context.Background(),
		"INSERT INTO agent_pool_memberships (pool_id, agent_id) VALUES (?, ?)",
		fixture.poolID, agentID); err != nil {
		t.Fatalf("add agent %q to pool: %v", agentID, err)
	}
}

func runtimeCreateWaitingDeployment(
	t *testing.T,
	fixture *pollFixture,
	payload string,
) int64 {
	t.Helper()
	ctx := context.Background()
	deployment, err := fixture.repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     fixture.releaseID,
			EnvironmentID: fixture.envID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	ciphertext, err := fixture.box.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt payload: %v", err)
	}
	if _, err := fixture.repo.DB.ExecContext(
		ctx,
		"INSERT INTO deployment_payloads (deployment_id, ciphertext) VALUES (?, ?)",
		deployment.ID,
		ciphertext,
	); err != nil {
		t.Fatalf("create payload: %v", err)
	}
	if _, err := fixture.repo.DB.ExecContext(
		ctx,
		"INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state) VALUES (?, 'remote', ?, '', 'waiting')",
		deployment.ID,
		fixture.poolID,
	); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}
	return deployment.ID
}

func runtimeClaimStatus(
	fixture *pollFixture,
	deploymentID int64,
	agentID, token string,
) int {
	hash := sha256.Sum256([]byte(token))
	result, err := fixture.repo.DB.ExecContext(context.Background(), `
UPDATE deployment_dispatches
SET state = 'claimed', agent_id = ?, claim_token_hash = ?, claim_expires_at = ?, last_heartbeat_at = ?
WHERE deployment_id = ? AND state = 'waiting'`, agentID, hash[:], fixture.now.AddDate(0, 0, 1).Unix(), fixture.now.Unix(), deploymentID)
	if err != nil {
		return http.StatusInternalServerError
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return http.StatusNoContent
	}
	return http.StatusOK
}
