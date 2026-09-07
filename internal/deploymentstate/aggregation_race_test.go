package deploymentstate

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

type delayedParentDB struct {
	*sql.DB
	beforeWrite func()
}

func (d *delayedParentDB) ExecContext(
	ctx context.Context, query string, args ...interface{},
) (sql.Result, error) {
	if d.beforeWrite != nil {
		hook := d.beforeWrite
		d.beforeWrite = nil
		hook()
	}
	return d.DB.ExecContext(ctx, query, args...)
}

func TestRecomputeParent_PreservesTerminalAfterDelayedWrite(t *testing.T) {
	for _, status := range []string{"cancelled", "failed", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			// Given a running child snapshot and a later committed completion.
			conn, err := migrate.Run(filepath.Join(t.TempDir(), "race.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			_, err = conn.Exec(`
INSERT INTO projects(id,name) VALUES(1,'project');
INSERT INTO environments(id,name) VALUES(1,'environment');
INSERT INTO releases(id,project_id,version,steps_json)
VALUES(1,1,'v1','[]');
INSERT INTO deployments(id,release_id,environment_id,status,started_at)
VALUES(1,1,1,'running',1);
INSERT INTO deployments(id,release_id,environment_id,status,started_at,
parent_deployment_id,target_agent_id,target_agent_name)
VALUES(2,1,1,'running',1,1,'agent-a','Agent A');`)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			repo := repository.New(conn)
			delayed := &delayedParentDB{DB: conn}
			delayed.beforeWrite = func() {
				err := repo.WithTx(ctx, func(q *db.Queries) error {
					if err := q.UpdateDeploymentStatus(ctx,
						db.UpdateDeploymentStatusParams{
							ID: 2, Status: status,
							StartedAt:  sql.NullInt64{Int64: 1, Valid: true},
							FinishedAt: sql.NullInt64{Int64: 2, Valid: true},
						}); err != nil {
						return err
					}
					return RecomputeParent(ctx, q, 2)
				})
				if err != nil {
					t.Fatal(err)
				}
				parent, err := repo.Queries.GetDeployment(ctx, 1)
				if err != nil || parent.Status != status {
					t.Fatalf("completion parent=%s err=%v", parent.Status, err)
				}
				t.Logf("completion committed parent=%s finished=2", status)
			}

			// When the stale recomputation resumes after completion commits.
			if err := RecomputeParent(ctx, db.New(delayed), 2); err != nil {
				t.Fatal(err)
			}

			// Then neither the terminal status nor its timestamps regresses.
			parent, err := repo.Queries.GetDeployment(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if parent.Status != status || parent.StartedAt.Int64 != 1 ||
				!parent.FinishedAt.Valid || parent.FinishedAt.Int64 != 2 {
				t.Fatalf("stale recompute parent=%s started=%v finished=%v",
					parent.Status, parent.StartedAt, parent.FinishedAt)
			}
			t.Logf("stale recompute preserved parent=%s finished=2", status)
		})
	}
}
