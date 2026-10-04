package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestReleaseDeleteSerializesCreationAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		first, second := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		t.Run("creation first", func(t *testing.T) {
			ctx := t.Context()
			var result DeploymentResult
			err := first.withQueueTx(ctx,
				func(ctx context.Context, q *db.Queries) error {
					var err error
					result, err = first.createDeployment(ctx, q,
						db.CreateDeploymentParams{
							ReleaseID: 1, EnvironmentID: 1, Status: "pending",
						})
					if err != nil {
						return err
					}
					blocked, cancel := context.WithTimeout(
						ctx,
						150*time.Millisecond,
					)
					defer cancel()
					if err := second.DeleteRelease(blocked, 1, 1); err == nil {
						t.Fatal("delete passed an uncommitted deployment")
					}
					if blocked.Err() == nil {
						t.Fatal("delete did not wait for the release lock")
					}
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if err := second.DeleteRelease(ctx, 1, 1); !errors.Is(
				err, ErrReleaseHasActiveDeployment,
			) {
				t.Fatalf("delete after creation=%v", err)
			}
			if err := first.Queries.UpdateDeploymentStatus(ctx,
				db.UpdateDeploymentStatusParams{
					ID: result.Deployment.ID, Status: "succeeded",
				}); err != nil {
				t.Fatal(err)
			}
		})
		t.Run("deletion first", func(t *testing.T) {
			ctx := t.Context()
			tx, err := first.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := lockRelease(
				ctx,
				first.Queries.WithTx(tx),
				1,
			); err != nil {
				t.Fatal(err)
			}
			blocked, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()
			_, err = second.CreateDeployment(blocked,
				db.CreateDeploymentParams{
					ReleaseID: 1, EnvironmentID: 1, Status: "pending",
				})
			if err == nil || blocked.Err() == nil {
				t.Fatalf("creation did not wait for deletion lock: %v", err)
			}
			for _, write := range []func(context.Context) error{
				func(ctx context.Context) error {
					_, err := second.CreateScheduledDeployment(ctx,
						db.CreateScheduledDeploymentParams{
							ProjectID: 1, ReleaseID: 1, EnvironmentID: 1,
							Cron: "0 9 * * *", NextRunAt: 9999999999,
						})
					return err
				},
				func(ctx context.Context) error {
					_, err := second.UpdateScheduledDeployment(ctx,
						db.UpdateScheduledDeploymentParams{
							ID: 1, ProjectID: 1, ReleaseID: 1, EnvironmentID: 1,
							Cron: "0 9 * * *", NextRunAt: 9999999999,
						})
					return err
				},
			} {
				blocked, cancel := context.WithTimeout(
					ctx,
					150*time.Millisecond,
				)
				err := write(blocked)
				cancel()
				if err == nil || blocked.Err() != context.DeadlineExceeded {
					t.Fatalf(
						"schedule write did not wait for release lock: %v",
						err,
					)
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := first.DeleteRelease(ctx, 1, 1); err != nil {
				t.Fatal(err)
			}
			_, err = second.CreateDeployment(ctx, db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending",
			})
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("creation after deletion=%v", err)
			}
		})
	})
}
