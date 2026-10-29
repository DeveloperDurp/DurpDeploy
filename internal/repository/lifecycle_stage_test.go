package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
	"github.com/stretchr/testify/require"
)

func lifecycleReorderFixture(
	t *testing.T,
	backend string,
	positions ...int64,
) (*repository.Repository, int64, []db.LifecycleStage) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "reorder.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)"
	if backend != "SQLite" {
		dsn = repository.RepositoryDatabaseDSN(t, backend)
	}
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	repo := repository.New(conn)
	lc, err := repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: "reorder"})
	if err != nil {
		t.Fatal(err)
	}
	for i, position := range positions {
		env, err := repo.Queries.CreateEnvironment(t.Context(),
			db.CreateEnvironmentParams{Name: fmt.Sprint("stage-", i)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.Queries.CreateLifecycleStage(t.Context(),
			db.CreateLifecycleStageParams{
				LifecycleID: lc.ID, EnvironmentID: env.ID,
				SortOrder: position, RequiresApproval: int64(i % 2),
			})
		if err != nil {
			t.Fatal(err)
		}
	}
	stages, err := repo.Queries.ListLifecycleStages(t.Context(), lc.ID)
	if err != nil {
		t.Fatal(err)
	}
	return repo, lc.ID, stages
}

func TestLifecycleReorderRollback(t *testing.T) {
	for _, mode := range []string{"move", "permutation", "missing-row"} {
		t.Run(mode, func(t *testing.T) {
			repo, id, before := lifecycleReorderFixture(t, "SQLite", 5, 10)
			trigger := fmt.Sprintf(`CREATE TRIGGER fail_reorder
BEFORE UPDATE OF sort_order ON lifecycle_stages
WHEN OLD.id = %d AND NEW.sort_order = 1
BEGIN SELECT RAISE(ABORT, 'reorder failure'); END`, before[1].ID)
			if mode == "move" {
				trigger = fmt.Sprintf(`CREATE TRIGGER fail_reorder
BEFORE UPDATE OF sort_order ON lifecycle_stages
WHEN OLD.id = %d AND NEW.sort_order = 5
BEGIN SELECT RAISE(ABORT, 'reorder failure'); END`, before[1].ID)
			}
			if mode == "missing-row" {
				trigger = fmt.Sprintf(`CREATE TRIGGER fail_reorder
BEFORE UPDATE OF sort_order ON lifecycle_stages WHEN OLD.id = %d
BEGIN DELETE FROM lifecycle_stages WHERE id = OLD.id; END`, before[0].ID)
			}
			_, err := repo.DB.ExecContext(t.Context(), trigger)
			require.NoError(t, err)
			if mode == "move" {
				err = repo.MoveLifecycleStage(
					t.Context(),
					id,
					before[0].ID,
					false,
				)
			} else {
				var result []db.LifecycleStage
				result, err = repo.ReorderLifecycleStages(t.Context(), id,
					[]int64{before[1].ID, before[0].ID})
				require.Nil(t, result, "failed reorder returned an uncommitted list")
			}
			require.Error(t, err)
			if mode == "missing-row" {
				require.ErrorIs(t, err, sql.ErrNoRows)
			}
			after, readErr := repo.Queries.ListLifecycleStages(t.Context(), id)
			require.NoError(t, readErr)
			require.Equal(t, before, after, "partial reorder survived rollback")
		})
	}
}

func TestLifecycleReorderOccupiedNegatives(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			repo, id, before := lifecycleReorderFixture(t, backend,
				math.MinInt64, -2, -1, 5, math.MaxInt64)
			if err := repo.MoveLifecycleStage(
				t.Context(),
				id,
				before[1].ID,
				false,
			); err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(before)
			want[1], want[2] = want[2], want[1]
			want[1].SortOrder, want[2].SortOrder = -2, -1
			after, err := repo.Queries.ListLifecycleStages(t.Context(), id)
			if err != nil || !slices.Equal(want, after) {
				t.Fatalf("move changed stage metadata: %+v, %v", after, err)
			}
			ids := make([]int64, len(before))
			for i := range before {
				ids[i] = before[len(before)-1-i].ID
			}
			after, err = repo.ReorderLifecycleStages(t.Context(), id, ids)
			if err != nil {
				t.Fatal(err)
			}
			for i, stage := range after {
				want := before[len(before)-1-i]
				want.SortOrder = int64(i + 1)
				if stage != want {
					t.Fatalf("permutation changed stage metadata: %+v", after)
				}
			}
		})
	}
}

func TestLifecycleReorderConcurrentAppend(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			for _, move := range []bool{false, true} {
				t.Run(fmt.Sprint("move=", move), func(t *testing.T) {
					testLifecycleReorderConcurrentAppend(t, backend, move)
				})
			}
		})
	}
}

func testLifecycleReorderConcurrentAppend(
	t *testing.T,
	backend string,
	move bool,
) {
	t.Helper()
	repo, id, before := lifecycleReorderFixture(t, backend, 1, 10)
	env, err := repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: "append"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	appendDone, reorderDone := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		_, err := repo.CreateLifecycleStage(ctx, db.CreateLifecycleStageParams{
			LifecycleID: id, EnvironmentID: env.ID, RequiresApproval: 1,
		})
		appendDone <- err
	}()
	go func() {
		<-start
		if move {
			reorderDone <- repo.MoveLifecycleStage(ctx, id, before[0].ID, false)
			return
		}
		_, err := repo.ReorderLifecycleStages(ctx, id,
			[]int64{before[1].ID, before[0].ID})
		reorderDone <- err
	}()
	close(start)
	require.NoError(t, waitLifecycleReorder(ctx, appendDone))
	err = waitLifecycleReorder(ctx, reorderDone)
	if !move && errors.Is(err, repository.ErrLifecycleStageIDsIncomplete) {
		err = nil
	}
	require.NoError(t, err)
	after, err := repo.Queries.ListLifecycleStages(ctx, id)
	require.NoError(t, err)
	require.Len(t, after, 3)
	if move {
		before[0], before[1] = before[1], before[0]
		before[0].SortOrder, before[1].SortOrder = 1, 10
	} else if after[0].ID == before[1].ID {
		before[0], before[1] = before[1], before[0]
		before[0].SortOrder, before[1].SortOrder = 1, 2
	}
	require.Equal(t, before, after[:2], "nonserial reorder/append result")
	require.Equal(t, before[1].SortOrder+1, after[2].SortOrder)
	require.Equal(t, env.ID, after[2].EnvironmentID)
	require.Equal(t, int64(1), after[2].RequiresApproval)
}

func waitLifecycleReorder(ctx context.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
