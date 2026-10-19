package mssqldriver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWrappedConnTransactionFailures(t *testing.T) {
	for _, operation := range []string{"begin", "commit", "rollback"} {
		for _, failure := range []bool{false, true} {
			t.Run(
				operation+"/failure="+strconv.FormatBool(failure),
				func(t *testing.T) {
					var want error
					if failure {
						want = errors.New("transaction response unconfirmed")
					}
					upstream := &transactionTestConn{
						operation: operation,
						err:       want,
					}
					conn := &wrappedConn{Conn: upstream}
					tx, err := conn.BeginTx(t.Context(), driver.TxOptions{})
					if operation == "commit" {
						require.NoError(t, err)
						err = tx.Commit()
					}
					if operation == "rollback" {
						require.NoError(t, err)
						err = tx.Rollback()
					}
					require.Equal(
						t,
						want,
						err,
						"preserve the original operation error",
					)
					require.Equal(t, !failure, conn.IsValid())
					upstream.invalid = true
					require.False(
						t,
						conn.IsValid(),
						"preserve upstream validation",
					)
				},
			)
		}
	}
}

func TestWrappedConnLegacyBeginFailure(t *testing.T) {
	want := errors.New("legacy begin failed")
	conn := &wrappedConn{Conn: &legacyTransactionTestConn{err: want}}
	_, err := conn.BeginTx(t.Context(), driver.TxOptions{})
	require.Equal(t, want, err)
	require.False(t, conn.IsValid())
}

func TestCancelledTransactionDiscardsConnection(t *testing.T) {
	connector := &transactionTestConnector{}
	db := sql.OpenDB(wrappedConnector{
		connector: connector,
		driver:    wrappedDriver{upstream: connector.Driver()},
	})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	cancel()

	// database/sql must finish automatic rollback before the next operation.
	probeCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	result, err := db.ExecContext(probeCtx, "SELECT 1")
	require.NoError(t, err)
	connectionID, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(2), connectionID,
		"a cancelled rollback must not return its connection to the pool")
}

type legacyTransactionTestConn struct {
	connectorTestConn
	err error
}

func (c legacyTransactionTestConn) Begin() (driver.Tx, error) {
	return nil, c.err
}

type transactionTestConnector struct {
	connections atomic.Int64
}

func (c *transactionTestConnector) Connect(
	context.Context,
) (driver.Conn, error) {
	return &transactionTestConn{id: c.connections.Add(1)}, nil
}

func (*transactionTestConnector) Driver() driver.Driver {
	return &connectorTestDriver{}
}

type transactionTestConn struct {
	connectorTestConn
	operation string
	err       error
	invalid   bool
	id        int64
	ctx       context.Context
}

func (c *transactionTestConn) BeginTx(
	ctx context.Context,
	_ driver.TxOptions,
) (driver.Tx, error) {
	c.ctx = ctx
	if c.operation == "begin" && c.err != nil {
		return nil, c.err
	}
	return c, nil
}

func (c *transactionTestConn) Commit() error {
	if c.operation == "commit" {
		return c.err
	}
	return nil
}

func (c *transactionTestConn) Rollback() error {
	if c.operation == "rollback" {
		return c.err
	}
	return c.ctx.Err()
}

func (c *transactionTestConn) IsValid() bool { return !c.invalid }

func (*transactionTestConn) ResetSession(context.Context) error { return nil }

func (c *transactionTestConn) ExecContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Result, error) {
	return driver.RowsAffected(c.id), nil
}
