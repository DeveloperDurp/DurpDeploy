package pgdriver

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"github.com/jackc/pgx/v5/stdlib"
)

func init() {
	sql.Register(DriverName, wrappedDriver{})
}

// wrappedDriver delegates to pgx's stdlib driver, rewriting every statement
// through RewriteSQL first.
type wrappedDriver struct{}

func (wrappedDriver) Open(name string) (driver.Conn, error) {
	conn, err := stdlib.GetDefaultDriver().Open(name)
	if err != nil {
		return nil, err
	}
	return &wrappedConn{conn}, nil
}

// wrappedConn embeds driver.Conn so unrelated optional interfaces (e.g.
// driver.Pinger, driver.SessionResetter) implemented by pgx's connection
// keep working unmodified; only the query-executing methods are overridden.
type wrappedConn struct {
	driver.Conn
}

func (c *wrappedConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(RewriteSQL(query))
}

func (c *wrappedConn) PrepareContext(
	ctx context.Context,
	query string,
) (driver.Stmt, error) {
	query = RewriteSQL(query)
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *wrappedConn) ExecContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return e.ExecContext(ctx, RewriteSQL(query), args)
}

func (c *wrappedConn) QueryContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return q.QueryContext(ctx, RewriteSQL(query), args)
}

func (c *wrappedConn) BeginTx(
	ctx context.Context,
	opts driver.TxOptions,
) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *wrappedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *wrappedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if chk, ok := c.Conn.(driver.NamedValueChecker); ok {
		return chk.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c *wrappedConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *wrappedConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}
