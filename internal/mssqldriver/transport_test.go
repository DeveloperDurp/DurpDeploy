package mssqldriver

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/stretchr/testify/require"
)

func TestSessionDialerClosesOwnedSockets(t *testing.T) {
	var sockets []*countingSocket
	owned := &sessionDialer{Dialer: testDialer(func(
		context.Context, string, string,
	) (net.Conn, error) {
		client, peer := net.Pipe()
		t.Cleanup(func() { require.NoError(t, peer.Close()) })
		socket := &countingSocket{Conn: client}
		sockets = append(sockets, socket)
		return socket, nil
	})}
	for range 2 {
		_, err := owned.DialContext(t.Context(), "tcp", "unused")
		require.NoError(t, err)
	}
	for range 2 {
		require.NoError(t, owned.Close())
	}
	for _, socket := range sockets {
		require.Equal(t, int32(1), socket.closes.Load())
	}
}

func TestSessionDialerClosesLateConnection(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() { require.NoError(t, peer.Close()) })
	socket := &countingSocket{Conn: client}
	started, release := make(chan struct{}), make(chan struct{})
	releaseDial := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseDial)
	owned := &sessionDialer{Dialer: testDialer(func(
		context.Context, string, string,
	) (net.Conn, error) {
		close(started)
		<-release
		return socket, nil
	})}
	done := make(chan error, 1)
	go func() {
		_, err := owned.DialContext(t.Context(), "tcp", "unused")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	require.NoError(t, owned.Close())
	releaseDial()
	select {
	case err := <-done:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("late dial did not finish")
	}
	require.Equal(t, int32(1), socket.closes.Load())
}

func TestTrackConnectorPreservesNativeConfiguration(t *testing.T) {
	const dsn = "sqlserver://localhost?keepAlive=17"
	native, err := mssql.NewConnector(dsn)
	require.NoError(t, err)
	native.SessionInitSQL = "SET XACT_ABORT ON"
	first, firstSockets, err := trackConnector(native, dsn)
	require.NoError(t, err)
	second, secondSockets, err := trackConnector(native, dsn)
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.NotSame(t, firstSockets, secondSockets)
	require.Nil(t, native.Dialer)
	clone := first.(*mssql.Connector)
	require.Equal(t, native.SessionInitSQL, clone.SessionInitSQL)
	dialer := clone.Dialer.(*sessionDialer).Dialer.(*net.Dialer)
	require.Equal(t, 17*time.Second, dialer.KeepAlive)
	require.NoError(t, firstSockets.Close())
	require.NoError(t, secondSockets.Close())

	native.Dialer = namedTestDialer{testDialer(nil)}
	cloned, sockets, err := trackConnector(native, dsn)
	require.NoError(t, err)
	host := cloned.(*mssql.Connector).Dialer.(mssql.HostDialer)
	require.Equal(t, "db.example.test", host.HostName())
	require.NoError(t, sockets.Close())
}

func TestWrappedConnCloseKeepsUnconfirmedBufferPrivate(t *testing.T) {
	for _, unusable := range []bool{false, true} {
		upstreamCloses, transportCloses := 0, 0
		want := errors.New("socket close failed")
		conn := &wrappedConn{
			Conn: &closeTestConn{close: func() error {
				upstreamCloses++
				return nil
			}},
			transport: testCloser(func() error {
				transportCloses++
				return want
			}),
			unusable: unusable,
		}
		require.ErrorIs(t, conn.Close(), want)
		require.Equal(t, 1, transportCloses)
		if unusable {
			require.Zero(t, upstreamCloses)
		} else {
			require.Equal(t, 1, upstreamCloses)
		}
	}
}

type testDialer func(context.Context, string, string) (net.Conn, error)

func (d testDialer) DialContext(
	ctx context.Context, network, address string,
) (net.Conn, error) {
	return d(ctx, network, address)
}

type namedTestDialer struct{ mssql.Dialer }

func (namedTestDialer) HostName() string { return "db.example.test" }

type countingSocket struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingSocket) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

type testCloser func() error

var _ io.Closer = testCloser(nil)

func (c testCloser) Close() error { return c() }

type closeTestConn struct {
	connectorTestConn
	close func() error
}

func (c *closeTestConn) Close() error { return c.close() }
