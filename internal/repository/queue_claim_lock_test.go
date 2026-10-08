package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
	mssql "github.com/microsoft/go-mssqldb"
)

func TestEnvironmentQueueSQLServerPayloadRevocationLocks(t *testing.T) {
	repo, revoker := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, "SQLServer"),
	)
	for _, kind := range []string{"deployment", "step"} {
		t.Run(kind, func(t *testing.T) {
			// Given: an eligible agent claim owns its environment during payload preparation.
			fixture := seedRevocationRaceFixture(t, repo, "payload-"+kind)
			deployment, err := repo.Queries.GetDeployment(
				t.Context(),
				fixture.deploymentID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "step" {
				if err := repo.SnapshotDeploymentSteps(t.Context(), fixture.deploymentID, []DeploymentStepSnapshot{
					{CreateDeploymentStepParams: db.CreateDeploymentStepParams{Name: "remote", ScriptBody: "echo remote", Interpreter: "bash", ExecutionTarget: "agent"}},
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := repo.DB.ExecContext(t.Context(), "DELETE FROM remote_deployment_claims WHERE deployment_id=?", fixture.deploymentID); err != nil {
					t.Fatal(err)
				}
				if _, err := repo.DB.ExecContext(t.Context(), "UPDATE deployments SET status='running',assigned_agent_id=NULL WHERE id=?", fixture.deploymentID); err != nil {
					t.Fatal(err)
				}
				if _, err := repo.Queries.AddAgentInterpreter(t.Context(), db.AddAgentInterpreterParams{AgentID: fixture.agentID, Interpreter: "bash"}); err != nil {
					t.Fatal(err)
				}
				if _, err := repo.Queries.AddAgentEnvironmentLabel(t.Context(), db.AddAgentEnvironmentLabelParams{AgentID: fixture.agentID, EnvironmentID: deployment.EnvironmentID}); err != nil {
					t.Fatal(err)
				}
				if count, err := repo.QueueRemoteStepRuns(t.Context(), fixture.deploymentID, 0); err != nil ||
					count != 1 {
					t.Fatalf("queue=%d: %v", count, err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			ready, resume := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			}()
			prepare := func(RemotePayloadSnapshot) (RemotePreparedClaim, error) {
				close(ready)
				select {
				case <-resume:
				case <-ctx.Done():
					return RemotePreparedClaim{}, ctx.Err()
				}
				return RemotePreparedClaim{
					TokenHash:  fixture.identity.ClaimTokenHash,
					Ciphertext: []byte("sealed"),
				}, nil
			}
			claimed := make(chan error, 1)
			go func() {
				claim := repo.ClaimRemoteDeploymentPayload
				if kind == "step" {
					claim = repo.ClaimRemoteStepPayload
				}
				_, ok, err := claim(ctx, fixture.agentID, prepare)
				if err == nil && !ok {
					err = fmt.Errorf("eligible %s was not claimed", kind)
				}
				claimed <- err
			}()
			select {
			case <-ready:
			case err := <-claimed:
				t.Fatalf("prepare: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// The real claim transaction must hold the environment, before its agent lock.
			_, err = revoker.DB.ExecContext(
				ctx,
				"UPDATE environments WITH (NOWAIT) SET name=name WHERE id=?",
				deployment.EnvironmentID,
			)
			var lockError mssql.Error
			if !errors.As(err, &lockError) || lockError.Number != 1222 {
				t.Fatalf("payload did not retain environment lock: %v", err)
			}
			// When: public revocation competes with that actual payload transaction.
			revoked := make(chan error, 1)
			go func() {
				changed, err := revoker.RevokeAgent(ctx, fixture.agentID)
				if err == nil && !changed {
					err = errors.New("agent was not revoked")
				}
				revoked <- err
			}()
			for {
				var waiting int
				if err := repo.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.dm_exec_requests
WHERE database_id=DB_ID() AND wait_type LIKE 'LCK_M%'`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting > 0 {
					break
				}
				select {
				case err := <-revoked:
					t.Fatalf("revocation did not wait: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(20 * time.Millisecond):
				}
			}
			close(resume)
			// Then: both transactions complete without a SQL Server deadlock.
			if err := <-claimed; err != nil {
				t.Fatal(err)
			}
			if err := <-revoked; err != nil {
				t.Fatal(err)
			}
		})
	}
}
