package migrate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"durpdeploy/internal/db"

	"github.com/stretchr/testify/require"
)

func TestSQLServer_CancelledTransactionConnection(t *testing.T) {
	conn := newSQLServerTestDB(t)
	conn.SetMaxOpenConns(1)
	queries := db.New(conn)
	for range 25 {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		tx, err := conn.BeginTx(ctx, nil)
		require.NoError(t, err)
		project, err := queries.WithTx(tx).CreateProject(ctx,
			db.CreateProjectParams{Name: "cancelled-transaction"})
		require.NoError(t, err)
		cancel()

		// The next query waits for automatic rollback on this one-connection pool.
		probeCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
		_, err = queries.GetProject(probeCtx, project.ID)
		stop()
		require.ErrorIs(t, err, sql.ErrNoRows)
		require.ErrorIs(t, tx.Rollback(), sql.ErrTxDone)
	}
}
