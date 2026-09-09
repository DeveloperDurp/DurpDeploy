package repository

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
)

type revocationRaceFixture struct {
	agentID       string
	environmentID int64
	deploymentID  int64
	identity      RemoteLifecycleClaim
}

type revocationOperation struct {
	name  string
	setup func(*testing.T, *Repository, revocationRaceFixture)
	run   func(context.Context, *Repository, revocationRaceFixture) (bool, error)
	runTx func(context.Context, *db.Queries, revocationRaceFixture) (bool, error)
}

func TestRevocationRaceMatrixAcrossDatabases(t *testing.T) {
	operations := revocationOperations()
	forEachDeploymentCreationEngine(t, func(t *testing.T, engineName string) {
		engine := newDeploymentCreationEngine(t, engineName)
		primary, revoker := openDeploymentCreationEngine(t, engine)
		for _, operation := range operations {
			operation := operation
			for _, order := range []string{
				"RevocationFirst",
				operation.name + "First",
			} {
				order := order
				t.Run(operation.name+"/"+order, func(t *testing.T) {
					fixture := seedRevocationRaceFixture(
						t, primary, operation.name+order,
					)
					operation.setup(t, primary, fixture)
					if order == "RevocationFirst" {
						wrote, err := runRevocationFirst(
							t, primary, revoker, operation, fixture,
						)
						if wrote || err == nil && operation.name != "Poll" {
							t.Fatalf(
								"post-revocation wrote=%v err=%v",
								wrote,
								err,
							)
						}
					} else {
						wrote, err := runOperationFirst(
							t, primary, revoker, operation, fixture,
						)
						if err != nil || !wrote {
							t.Fatalf(
								"operation-first wrote=%v err=%v",
								wrote,
								err,
							)
						}
					}
					assertRevocationRaceOutcome(
						t, primary, operation.name, order, fixture,
					)
				})
			}
		}
	})
}

func revocationOperations() []revocationOperation {
	claimed := func(t *testing.T, repo *Repository, fixture revocationRaceFixture) {
		t.Helper()
		rows, err := repo.ClaimRemoteDeployment(
			t.Context(), raceClaimParams(t, repo, fixture),
		)
		if err != nil || rows != 1 {
			t.Fatalf("claim rows=%d err=%v", rows, err)
		}
	}
	started := func(t *testing.T, repo *Repository, fixture revocationRaceFixture) {
		t.Helper()
		claimed(t, repo, fixture)
		if err := repo.StartRemoteDeployment(
			t.Context(),
			fixture.identity,
		); err != nil {
			t.Fatal(err)
		}
	}
	return []revocationOperation{
		{name: "Assignment", setup: noRaceSetup,
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				err := repo.AssignEnvironmentAgent(
					ctx,
					fixture.environmentID,
					fixture.agentID,
				)
				return err == nil, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				err := assignEnvironmentAgent(
					ctx, q, fixture.environmentID, fixture.agentID,
				)
				return err == nil, err
			}},
		{name: "Poll", setup: noRaceSetup,
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				rows, err := repo.ClaimRemoteDeployment(
					ctx,
					raceClaimParamsContext(ctx, repo, fixture),
				)
				return rows == 1, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				rows, err := claimRemoteDeployment(
					ctx, q, raceClaimParamsQueries(ctx, q, fixture),
				)
				return rows == 1, err
			}},
		{name: "Start", setup: claimed,
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				err := repo.StartRemoteDeployment(ctx, fixture.identity)
				return err == nil, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				err := startRemoteDeployment(ctx, q, fixture.identity)
				return err == nil, err
			}},
		{name: "Heartbeat", setup: started,
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				_, err := repo.HeartbeatRemoteDeployment(ctx, fixture.identity)
				return err == nil, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				_, err := heartbeatRemoteDeployment(ctx, q, fixture.identity)
				return err == nil, err
			}},
		{name: "Logs", setup: started,
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				logs, err := repo.AppendRemoteDeploymentLogs(
					ctx,
					fixture.identity,
					[]RemoteLogEvent{{Sequence: 1, Line: "committed"}},
				)
				return len(logs) == 1, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				logs, err := appendRemoteDeploymentLogs(
					ctx, q, fixture.identity,
					[]RemoteLogEvent{{Sequence: 1, Line: "committed"}},
				)
				return len(logs) == 1, err
			}},
		{name: "Result", setup: started,
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				result, err := repo.FinishRemoteDeploymentLifecycle(
					ctx,
					fixture.identity,
					"succeeded",
				)
				return result.Changed, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				result, err := finishRemoteDeploymentLifecycle(
					ctx, q, fixture.identity, "succeeded",
				)
				return result.Changed, err
			}},
		{
			name: "Cancelled",
			setup: func(t *testing.T, repo *Repository, fixture revocationRaceFixture) {
				started(t, repo, fixture)
				if err := repo.CancelAssignedRemoteDeployment(
					t.Context(),
					RemoteAssignedDeployment{
						DeploymentID: fixture.deploymentID, AgentID: fixture.agentID,
					},
				); err != nil {
					t.Fatal(err)
				}
			},
			run: func(ctx context.Context, repo *Repository, fixture revocationRaceFixture) (bool, error) {
				changed, err := repo.AcknowledgeRemoteCancellation(
					ctx,
					fixture.identity,
				)
				return changed, err
			},
			runTx: func(ctx context.Context, q *db.Queries, fixture revocationRaceFixture) (bool, error) {
				return acknowledgeRemoteCancellation(ctx, q, fixture.identity)
			},
		},
	}
}

func noRaceSetup(*testing.T, *Repository, revocationRaceFixture) {}
