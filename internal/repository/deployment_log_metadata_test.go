package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/internal/db"
)

func TestLogMetadataMigrationAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		deploymentID, legacyName := seedPreMigrationLogMetadata(t, repo, name)
		step := db.CreateStepDeploymentLogParams{
			DeploymentID: deploymentID, Line: "new output",
			StepIndex: sql.NullInt64{Int64: 0, Valid: true},
			StepName:  sql.NullString{String: "step", Valid: true},
		}
		err := repo.WithTx(t.Context(), func(q *db.Queries) error {
			return appendOrdinaryLogMetadataOutput(
				t.Context(),
				q,
				step,
				legacyName,
			)
		})
		if err != nil {
			t.Fatal(err)
		}
		// Then: only first presence and explicit states occupy the sparse index.
		var count int
		if err := repo.DB.QueryRowContext(t.Context(), `
			SELECT COUNT(*) FROM deployment_logs
			WHERE deployment_id = ? AND is_step_metadata = 1`,
			deploymentID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 5 {
			t.Fatalf("metadata rows = %d, want 5 after 400 output rows", count)
		}
		step.StepState = sql.NullString{String: "succeeded", Valid: true}
		terminal, err := repo.Queries.CreateStepDeploymentLog(t.Context(), step)
		if err != nil {
			t.Fatal(err)
		}
		for cutoff, want := range map[int64]string{
			terminal.ID - 1: "running", math.MaxInt64: "succeeded",
		} {
			states, err := repo.Queries.ListDeploymentLogStateMetadata(
				t.Context(), db.ListDeploymentLogStateMetadataParams{
					DeploymentID: deploymentID, LastID: cutoff,
				})
			if err != nil {
				t.Fatal(err)
			}
			if len(states) != 4 {
				t.Fatalf("metadata lost distinct nullable scopes: %+v", states)
			}
			for _, state := range states {
				if state.StepIndex.Valid && state.StepState.String != want {
					t.Fatalf(
						"watermark %d: got %+v, want %s",
						cutoff,
						state,
						want,
					)
				}
			}
		}
	})
}

func seedPreMigrationLogMetadata(
	t *testing.T, repo *Repository, name string,
) (int64, string) {
	t.Helper()
	deployment, err := repo.Queries.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "running",
		})
	if err != nil {
		t.Fatal(err)
	}
	// Given: pre-migration logs with indexed, legacy, NULL, and empty scopes.
	if err := goose.Down(repo.DB, "."); err != nil {
		t.Fatal(err)
	}
	legacyName := "legacy @ agent"
	if name != "SQLServer" {
		// These backends allow names wider than a PostgreSQL index key.
		var suffix strings.Builder
		for i := range 80 {
			fmt.Fprintf(&suffix, "%x", sha256.Sum256(fmt.Append(nil, i)))
		}
		legacyName += suffix.String()
	}
	for range 2 {
		for _, scope := range []struct {
			index sql.NullInt64
			name  sql.NullString
			state sql.NullString
		}{
			{sql.NullInt64{Int64: 0, Valid: true},
				sql.NullString{String: "step", Valid: true},
				sql.NullString{String: "running", Valid: true}},
			{name: sql.NullString{String: legacyName, Valid: true}},
			{},
			{name: sql.NullString{Valid: true}},
		} {
			_, err := repo.DB.ExecContext(t.Context(), `
				INSERT INTO deployment_logs
				(deployment_id, line, step_index, step_name, step_state)
				VALUES (?, 'old output', ?, ?, ?)`,
				deployment.ID, scope.index, scope.name, scope.state)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// When: the backfill runs and all supported writers append ordinary output.
	if err := goose.Up(repo.DB, "."); err != nil {
		t.Fatal(err)
	}
	return deployment.ID, legacyName
}

func appendOrdinaryLogMetadataOutput(
	ctx context.Context, q *db.Queries,
	step db.CreateStepDeploymentLogParams, legacyName string,
) error {
	for range 100 {
		if _, err := q.CreateStepDeploymentLog(ctx, step); err != nil {
			return err
		}
		for _, label := range []string{legacyName, ""} {
			_, err := q.CreateDeploymentLog(ctx,
				db.CreateDeploymentLogParams{
					DeploymentID: step.DeploymentID,
					Line:         "new output",
					StepName: sql.NullString{
						String: label, Valid: true,
					},
				})
			if err != nil {
				return err
			}
		}
		_, err := q.CreateRemoteDeploymentLog(ctx,
			db.CreateRemoteDeploymentLogParams{
				DeploymentID: step.DeploymentID,
				Line:         "new output",
				CreatedAt:    1,
			})
		if err != nil {
			return err
		}
	}
	return nil
}
