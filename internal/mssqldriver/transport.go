package mssqldriver

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// Each Connect owns its dialer, including parallel and routed connections.
func trackConnector(
	connector driver.Connector,
	dsn string,
) (driver.Connector, io.Closer, error) {
	native, ok := connector.(*mssql.Connector)
	if !ok {
		return connector, nil, nil
	}
	clone := *native
	upstream := native.Dialer
	if upstream == nil {
		config, err := msdsn.Parse(dsn)
		if err != nil {
			return nil, nil, err
		}
		keepAlive := config.KeepAlive
		if keepAlive == 0 {
			keepAlive = 30 * time.Second
		}
		upstream = &net.Dialer{KeepAlive: keepAlive}
	}
	owned := &sessionDialer{Dialer: upstream}
	clone.Dialer = owned
	if host, ok := upstream.(mssql.HostDialer); ok {
		clone.Dialer = sessionHostDialer{owned, host}
	}
	return &clone, owned, nil
}

type sessionDialer struct {
	mssql.Dialer
	mu      sync.Mutex
	closed  bool
	sockets []*sessionSocket
}

func (d *sessionDialer) DialContext(
	ctx context.Context,
	network, address string,
) (net.Conn, error) {
	conn, err := d.Dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	socket := &sessionSocket{Conn: conn}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.Join(net.ErrClosed, socket.Close())
	}
	d.sockets = append(d.sockets, socket)
	return socket, nil
}

func (d *sessionDialer) Close() error {
	d.mu.Lock()
	d.closed = true
	sockets := d.sockets
	d.sockets = nil
	d.mu.Unlock()
	var err error
	for _, socket := range sockets {
		err = errors.Join(err, socket.Close())
	}
	return err
}

type sessionHostDialer struct {
	*sessionDialer
	host mssql.HostDialer
}

func (d sessionHostDialer) HostName() string { return d.host.HostName() }

type sessionSocket struct {
	net.Conn
	once sync.Once
}

func (s *sessionSocket) Close() error {
	var err error
	s.once.Do(func() { err = s.Conn.Close() })
	return err
}
