package mssqldriver

import (
	"context"
	"database/sql/driver"
	"testing"
)

var _ driver.DriverContext = wrappedDriver{}
var _ driver.Connector = wrappedConnector{}

func TestWrappedDriver_OpenConnectorForwardsDriverContext(t *testing.T) {
	upstream := &connectorTestDriver{connector: connectorTestConnector{}}
	driver := wrappedDriver{upstream: upstream}

	connector, err := driver.OpenConnector("sqlserver://test")
	if err != nil {
		t.Fatalf("OpenConnector() error = %v", err)
	}
	if upstream.name != "sqlserver://test" {
		t.Fatalf("upstream OpenConnector name = %q", upstream.name)
	}

	conn, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, ok := conn.(*wrappedConn); !ok {
		t.Fatalf("Connect() connection = %T, want *wrappedConn", conn)
	}
	if connector.Driver() != driver {
		t.Fatalf(
			"Connector.Driver() = %T, want wrappedDriver",
			connector.Driver(),
		)
	}
}

type connectorTestDriver struct {
	connector driver.Connector
	name      string
}

func (d *connectorTestDriver) Open(string) (driver.Conn, error) {
	return &connectorTestConn{}, nil
}

func (d *connectorTestDriver) OpenConnector(
	name string,
) (driver.Connector, error) {
	d.name = name
	return d.connector, nil
}

type connectorTestConnector struct{}

func (connectorTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &connectorTestConn{}, nil
}

func (connectorTestConnector) Driver() driver.Driver {
	return &connectorTestDriver{}
}

type connectorTestConn struct{}

func (*connectorTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (*connectorTestConn) Close() error { return nil }

func (*connectorTestConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }
