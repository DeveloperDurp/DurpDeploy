//go:build e2e

package handler_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"sync"
)

// Pause real HTTP capture after step rows close, before selector reads.
// The wrapper changes no statements, transaction options, or stored data.
type releaseCaptureBarrier struct {
	read, edit, resume chan struct{}
	readOnce, editOnce sync.Once
}

type releaseCaptureKey struct{}

type releaseCaptureOperation struct {
	barrier *releaseCaptureBarrier
	edit    bool
}

type releaseCaptureConnector struct {
	upstream driver.Driver
	dsn      string
}

func (c releaseCaptureConnector) Driver() driver.Driver { return c.upstream }

func (c releaseCaptureConnector) Connect(
	ctx context.Context,
) (driver.Conn, error) {
	var conn driver.Conn
	var err error
	if upstream, ok := c.upstream.(driver.DriverContext); ok {
		connector, openErr := upstream.OpenConnector(c.dsn)
		if openErr != nil {
			return nil, openErr
		}
		conn, err = connector.Connect(ctx)
	} else {
		conn, err = c.upstream.Open(c.dsn)
	}
	if err != nil {
		return nil, err
	}
	return &releaseCaptureConn{Conn: conn}, nil
}

type releaseCaptureConn struct{ driver.Conn }

func (c *releaseCaptureConn) BeginTx(
	ctx context.Context,
	opts driver.TxOptions,
) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *releaseCaptureConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *releaseCaptureConn) ResetSession(ctx context.Context) error {
	if reset, ok := c.Conn.(driver.SessionResetter); ok {
		return reset.ResetSession(ctx)
	}
	return nil
}

func (c *releaseCaptureConn) ExecContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *releaseCaptureConn) QueryContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	if operation, ok := ctx.Value(releaseCaptureKey{}).(releaseCaptureOperation); ok {
		barrier := operation.barrier
		if operation.edit &&
			strings.Contains(query, "-- name: UpdateStep :one") {
			barrier.editOnce.Do(func() { close(barrier.edit) })
		}
		if !operation.edit &&
			strings.Contains(
				query,
				"-- name: ListStepAgentSelectorsByStepIDs",
			) {
			barrier.readOnce.Do(func() { close(barrier.read) })
			select {
			case <-barrier.resume:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
