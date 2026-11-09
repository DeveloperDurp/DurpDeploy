package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

type sqliteConstraint struct {
	code    int
	message string
}

func (e sqliteConstraint) Error() string { return e.message }
func (e sqliteConstraint) Code() int     { return e.code }

func TestForeignKeyClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite FK", sqliteConstraint{787, "constraint failed"}, true},
		{"sqlite restrict", sqliteConstraint{1811, "FOREIGN KEY constraint failed"}, true},
		{"sqlite trigger", sqliteConstraint{1811, "custom trigger failed"}, false},
		{"sqlite check", sqliteConstraint{275, "CHECK constraint failed"}, false},
		{"postgres FK", &pgconn.PgError{Code: "23503"}, true},
		{"postgres unique", &pgconn.PgError{Code: "23505"}, false},
		{"mssql FK", mssql.Error{Number: 547, Message: "conflicted with the FOREIGN KEY constraint"}, true},
		{"mssql delete", mssql.Error{Number: 547, Message: "conflicted with the REFERENCE constraint"}, true},
		{"mssql pointer", &mssql.Error{Number: 547, Message: "FOREIGN KEY constraint"}, true},
		{"mssql check", mssql.Error{Number: 547, Message: "conflicted with the CHECK constraint"}, false},
		{"connection", errors.New("connection refused"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isForeignKeyViolation(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if tc.err != nil &&
				isForeignKeyViolation(
					fmt.Errorf("write: %w", tc.err),
				) != tc.want {
				t.Fatal("wrapped classification differs")
			}
		})
	}
}

func TestForeignKeyWritesAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, checkForeignKeyWrites)
}

func checkForeignKeyWrites(t *testing.T, name string) {
	t.Helper()
	r, _ := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, name),
	)
	ctx := t.Context()
	if _, err := r.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 999, EnvironmentID: 1, Status: "pending",
	}); !errors.Is(err, ErrDeploymentParent) {
		t.Fatalf("missing release during admission: %v", err)
	}
	// Missing environments must never reach deployment insertion.
	if _, err := r.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     1,
			EnvironmentID: 999,
			Status:        "pending",
		},
	); !errors.Is(
		err,
		ErrDeploymentParent,
	) {
		t.Fatalf("missing deployment parent: %v", err)
	}
	allowed, err := r.VariableEnvironmentAllowed(
		ctx,
		1,
		sql.NullInt64{Int64: 999, Valid: true},
	)
	if err != nil || allowed {
		t.Fatalf("missing variable scope allowed=%v: %v", allowed, err)
	}
	if _, err := r.CreateVariable(
		ctx,
		db.CreateVariableParams{
			ProjectID:     1,
			Name:          "MISSING",
			EnvironmentID: sql.NullInt64{Int64: 999, Valid: true},
		},
	); !errors.Is(
		err,
		ErrVariableParent,
	) {
		t.Fatalf("variable insert FK: %v", err)
	}
	life, err := r.Queries.CreateLifecycle(
		ctx,
		db.CreateLifecycleParams{Name: "reference"},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := r.Queries.CreateLifecycleStage(
		ctx,
		db.CreateLifecycleStageParams{
			LifecycleID:   life.ID,
			EnvironmentID: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.ExecContext(
		ctx,
		"INSERT INTO agent_environment_labels(environment_id, agent_id) VALUES(?, ?)",
		1,
		"race-agent",
	); err != nil {
		t.Fatal(err)
	}
	if err := r.Queries.DeleteEnvironment(
		ctx,
		1,
	); !isForeignKeyViolation(
		err,
	) {
		t.Fatalf("real restricted delete classification: %v", err)
	}
	history, err := r.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     1,
			EnvironmentID: 1,
			Status:        "succeeded",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = r.DeleteEnvironment(ctx, 1)
	if !errors.Is(err, ErrEnvironmentReferenced) ||
		!strings.Contains(err.Error(), "1 lifecycle stages") {
		t.Fatalf("delete references: %v", err)
	}
	if _, err := r.Queries.GetDeployment(ctx, history.ID); err != nil {
		t.Fatalf("blocked delete removed history: %v", err)
	}
	if err := r.Queries.DeleteLifecycleStage(ctx, stage.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteEnvironment(ctx, 1); err != nil {
		t.Fatalf("delete after cleanup: %v", err)
	}
	if _, err := r.Queries.GetEnvironment(
		ctx,
		1,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("environment remains: %v", err)
	}
}

func TestForeignKeyAdmissionAfterConcurrentDelete(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		deleter, creator := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		tx, err := deleter.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		q := deleter.Queries.WithTx(tx)
		if _, err := q.LockDeploymentEnvironment(ctx, 1); err != nil {
			t.Fatal(err)
		}
		started, result := make(chan struct{}), make(chan error, 1)
		go func() {
			close(started)
			_, err := creator.CreateDeployment(
				ctx,
				db.CreateDeploymentParams{
					ReleaseID:     1,
					EnvironmentID: 1,
					Status:        "pending",
				},
			)
			result <- err
		}()
		<-started
		if err := q.DeleteEnvironment(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, ErrDeploymentParent) {
				t.Fatalf("concurrent delete: %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		for i := 0; i < 2; i++ {
			if _, err := creator.CreateDeploymentFromDeployment(
				ctx,
				db.CreateDeploymentParams{ReleaseID: 1, EnvironmentID: 1},
				1,
			); !errors.Is(
				err,
				ErrDeploymentParent,
			) {
				t.Fatalf("rerun missing environment: %v", err)
			}
		}
	})
}
