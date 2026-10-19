package mssqldriver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/testutil"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/stretchr/testify/require"
)

type bufferProofKey struct{}
type bufferProofLogger struct {
	armed            atomic.Bool
	reached, release chan struct{}
	once             sync.Once
	messages         atomic.Int64
	done             chan struct{}
	failed           chan string
}

func (l *bufferProofLogger) Log(
	ctx context.Context,
	category msdsn.Log,
	msg string,
) {
	if strings.Contains(msg, "got token") {
		l.messages.Add(1)
	}
	if ctx.Value(bufferProofKey{}) != nil {
		if category == msdsn.LogErrors {
			select {
			case l.failed <- msg:
			default:
			}
		}
		if strings.HasPrefix(msg, "got DONE or DONEPROC") {
			select {
			case l.done <- struct{}{}:
			default:
			}
		}
	}
	if msg == "got token tokenEnvChange" &&
		l.armed.CompareAndSwap(true, false) {
		close(l.reached)
		<-l.release
	}
}
func TestWrappedDriver_RollbackBufferHandoff(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	dsn := testutil.SQLServerDSN(t) + "&log=64"
	// Race builds can discard sync.Pool entries instead of reusing them.
	for iteration := range 16 {
		if !t.Run(strconv.Itoa(iteration), func(t *testing.T) {
			checkRollbackBufferHandoff(t, dsn)
		}) {
			return
		}
	}
}

func checkRollbackBufferHandoff(t *testing.T, dsn string) {
	t.Helper()
	probeCtx, stop := context.WithTimeout(t.Context(), 30*time.Second)
	defer stop()
	logger := &bufferProofLogger{
		reached: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}, 1),
		failed:  make(chan string, 1),
	}
	native := &mssql.Driver{}
	native.SetContextLogger(logger)
	connector, err := (wrappedDriver{upstream: native}).OpenConnector(dsn)
	require.NoError(t, err)
	pool := sql.OpenDB(connector)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	t.Cleanup(func() { logger.once.Do(func() { close(logger.release) }) })
	pool.SetMaxOpenConns(1)
	sqlConn, err := pool.Conn(probeCtx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlConn.Close() })
	ctx, cancel := context.WithCancel(
		context.WithValue(probeCtx, bufferProofKey{}, true),
	)
	defer cancel()
	var rawTx driver.Tx
	require.NoError(t, sqlConn.Raw(func(c any) error {
		var e error
		rawTx, e = c.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
		return e
	}))
	select {
	case <-logger.done:
	case <-time.After(5 * time.Second):
		t.Fatal("BEGIN reader did not finish")
	}
	logger.armed.Store(true)
	cancel()
	require.ErrorIs(t, rawTx.Rollback(), context.Canceled)
	select {
	case <-logger.reached:
	case <-time.After(5 * time.Second):
		t.Fatalf(
			"reader barrier not reached; tokens=%d",
			logger.messages.Load(),
		)
	}
	require.NoError(t, sqlConn.Close())
	// A new physical connection must not reuse memory still owned by that reader.
	var value int
	require.NoError(
		t,
		pool.QueryRowContext(probeCtx, "SELECT 1").Scan(&value),
	)
	require.Equal(t, 1, value)
	logger.once.Do(func() { close(logger.release) })
	// Keep the replacement connection active as the paused parser resumes.
	for range 50 {
		require.NoError(
			t,
			pool.QueryRowContext(probeCtx, "SELECT 1").Scan(&value),
		)
	}
	select {
	case <-logger.done:
	case message := <-logger.failed:
		t.Fatalf("retired reader parsed recycled buffer: %s", message)
	case <-time.After(5 * time.Second):
		t.Fatal("retired reader did not finish its rollback response")
	}
}
