package mssqldriver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"

	"github.com/microsoft/go-mssqldb"
)

func init() {
	sql.Register(DriverName, wrappedDriver{})
}

// wrappedDriver delegates to the native SQL Server driver after rewriting
// application SQL into the syntax documented for the "sqlserver" driver.
type wrappedDriver struct {
	upstream driver.Driver
}

func (d wrappedDriver) Open(name string) (driver.Conn, error) {
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

func (d wrappedDriver) OpenConnector(name string) (driver.Connector, error) {
	upstream := d.driver()
	if driverContext, ok := upstream.(driver.DriverContext); ok {
		connector, err := driverContext.OpenConnector(name)
		if err != nil {
			return nil, err
		}
		return wrappedConnector{connector: connector, driver: d, dsn: name}, nil
	}
	return wrappedConnector{
		connector: driverConnector{driver: upstream, name: name},
		driver:    d,
	}, nil
}

func (d wrappedDriver) driver() driver.Driver {
	if d.upstream != nil {
		return d.upstream
	}
	return &mssql.Driver{}
}

type wrappedConnector struct {
	connector driver.Connector
	driver    driver.Driver
	dsn       string
}

func (c wrappedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connector, transport, err := trackConnector(c.connector, c.dsn)
	if err != nil {
		return nil, err
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		if transport != nil {
			err = errors.Join(err, transport.Close())
		}
		return nil, err
	}
	return &wrappedConn{Conn: conn, transport: transport}, nil
}

func (c wrappedConnector) Driver() driver.Driver {
	return c.driver
}

type driverConnector struct {
	driver driver.Driver
	name   string
}

func (c driverConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(c.name)
}

func (c driverConnector) Driver() driver.Driver {
	return c.driver
}

// wrappedConn embeds driver.Conn so optional interfaces provided by mssql stay
// available while the query execution paths are rewritten.
type wrappedConn struct {
	driver.Conn
	transport io.Closer
	// database/sql serializes transaction completion and validation.
	unusable bool
}

func (c *wrappedConn) Close() error {
	if c.transport == nil {
		return c.Conn.Close()
	}
	if c.unusable {
		// Native Close recycles its buffer before its reader can finish.
		// Close the sockets and let this private buffer be garbage collected.
		return c.transport.Close()
	}
	return errors.Join(c.Conn.Close(), c.transport.Close())
}

func (c *wrappedConn) Prepare(query string) (driver.Stmt, error) {
	query, err := RewriteSQL(query)
	if err != nil {
		return nil, err
	}
	return c.Conn.Prepare(query)
}

func (c *wrappedConn) PrepareContext(
	ctx context.Context,
	query string,
) (driver.Stmt, error) {
	query, err := RewriteSQL(query)
	if err != nil {
		return nil, err
	}
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
	query, err := RewriteSQL(query)
	if err != nil {
		return nil, err
	}
	return e.ExecContext(ctx, query, args)
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
	query, err := RewriteSQL(query)
	if err != nil {
		return nil, err
	}
	return q.QueryContext(ctx, query, args)
}

func (c *wrappedConn) BeginTx(
	ctx context.Context,
	opts driver.TxOptions,
) (driver.Tx, error) {
	var tx driver.Tx
	var err error
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = b.BeginTx(ctx, opts)
	} else {
		//nolint:staticcheck // Required fallback without ConnBeginTx.
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		c.unusable = true
		return nil, err
	}
	return &wrappedTx{Tx: tx, conn: c}, nil
}

func (c *wrappedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

type wrappedTx struct {
	driver.Tx
	conn *wrappedConn
}

func (tx *wrappedTx) Commit() error {
	err := tx.Tx.Commit()
	if err != nil {
		tx.conn.unusable = true
	}
	return err
}

func (tx *wrappedTx) Rollback() error {
	err := tx.Tx.Rollback()
	if err != nil {
		tx.conn.unusable = true
	}
	return err
}

func (c *wrappedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *wrappedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c *wrappedConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *wrappedConn) IsValid() bool {
	// A cancelled rollback can return before the upstream reader finishes.
	// Discard that connection without replacing ambiguous errors with ErrBadConn.
	if c.unusable {
		return false
	}
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}
