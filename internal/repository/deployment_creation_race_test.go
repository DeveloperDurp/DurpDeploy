package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestRevocationCreateRaceAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(
		t,
		func(t *testing.T, engineName string) {
			t.Run("RevocationFirst", func(t *testing.T) {
				engine := newDeploymentCreationEngine(t, engineName)
				creator, revoker := openDeploymentCreationEngine(t, engine)
				ctx := context.Background()
				assignment, err := creator.Queries.GetEnvironmentAgentAssignment(
					ctx,
					1,
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := revokeRaceAgent(ctx, revoker, nil); err != nil {
					t.Fatal(err)
				}
				var result DeploymentResult
				err = creator.WithTx(ctx, func(q *db.Queries) error {
					var createErr error
					result, createErr = creator.createDeployment(
						ctx,
						q,
						db.CreateDeploymentParams{
							ReleaseID: 1, EnvironmentID: 1, Status: "pending",
						},
						sql.NullString{String: assignment.AgentID, Valid: true},
					)
					return createErr
				})
				if !errors.Is(
					err,
					ErrDeploymentRoutingConflict,
				) {
					t.Fatalf(
						"revocation-first result=%+v error=%v",
						result,
						err,
					)
				}
				assertDeploymentCreationRaceRows(t, creator, 0, 0)
			})

			t.Run("CreateFirst", func(t *testing.T) {
				engine := newDeploymentCreationEngine(t, engineName)
				creator, revoker := openDeploymentCreationEngine(t, engine)
				ctx := context.Background()
				assignment, err := creator.Queries.GetEnvironmentAgentAssignment(
					ctx,
					1,
				)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := creator.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				result, err := creator.createDeployment(
					ctx,
					creator.Queries.WithTx(tx),
					db.CreateDeploymentParams{
						ReleaseID: 1, EnvironmentID: 1, Status: "pending",
					},
					sql.NullString{String: assignment.AgentID, Valid: true},
				)
				if err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				begun := make(chan struct{})
				revoked := make(chan error, 1)
				go func() {
					revoked <- revokeRaceAgent(ctx, revoker, begun)
				}()
				<-begun
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := <-revoked; err != nil {
					t.Fatal(err)
				}
				if result.Mode != ExecutionRemote {
					t.Fatalf("create-first mode=%q", result.Mode)
				}
				assertDeploymentCreationRaceRows(t, creator, 1, 1)
				deployment, err := creator.Queries.GetDeployment(
					ctx,
					result.Deployment.ID,
				)
				if err != nil || deployment.Status != "failed" {
					t.Fatalf(
						"terminal deployment=%+v error=%v",
						deployment,
						err,
					)
				}
			})
		},
	)
}

func revokeRaceAgent(
	ctx context.Context,
	repo *Repository,
	begun chan<- struct{},
) error {
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if begun != nil {
		close(begun)
	}
	q := repo.Queries.WithTx(tx)
	locked, err := q.LockClaimAgent(ctx, "race-agent")
	if err != nil {
		return err
	}
	if locked != 1 {
		return sql.ErrNoRows
	}
	if _, err := q.SetAgentStatus(ctx, db.SetAgentStatusParams{
		ID: "race-agent", Status: "revoked",
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM
environment_agent_assignments WHERE agent_id = ?`, "race-agent"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE remote_deployment_claims
SET state='cancelled', reason='agent_revoked', finished_at=unixepoch(),
cancel_requested_at=unixepoch(), updated_at=unixepoch()
WHERE agent_id=? AND state='waiting'`, "race-agent"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='failed',
finished_at=unixepoch() WHERE assigned_agent_id=?
AND status IN ('pending','running')`, "race-agent"); err != nil {
		return err
	}
	return tx.Commit()
}

func assertDeploymentCreationRaceRows(
	t *testing.T,
	repo *Repository,
	deployments int,
	claims int,
) {
	t.Helper()
	var gotDeployments, gotClaims int
	if err := repo.DB.QueryRow(
		"SELECT COUNT(*) FROM deployments",
	).Scan(&gotDeployments); err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.QueryRow(
		"SELECT COUNT(*) FROM remote_deployment_claims",
	).Scan(&gotClaims); err != nil {
		t.Fatal(err)
	}
	if gotDeployments != deployments || gotClaims != claims {
		t.Fatalf("rows deployments=%d claims=%d, want %d/%d",
			gotDeployments, gotClaims, deployments, claims)
	}
}
