package agenttls

import (
	"crypto/tls"
	"net"
	"testing"
)

func TestNewServerConfig_acceptsOnlyPinnedSelfSignedClient(t *testing.T) {
	// Given
	server, err := LoadOrCreate(t.TempDir(), "https://server.example.test")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	client, err := LoadOrCreate(t.TempDir(), "https://agent.example.test")
	if err != nil {
		t.Fatalf("create client identity: %v", err)
	}
	config, err := NewServerConfig(
		server,
		"agent.example.test",
		client.Fingerprint,
	)
	if err != nil {
		t.Fatalf("new server config: %v", err)
	}
	address, wait := serveMutualTLS(t, config)
	clientConfig, err := NewClientConfig(
		urlForHost(t, address, "server.example.test"),
		[]Fingerprint{server.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new client config: %v", err)
	}
	clientConfig.Certificates = []tls.Certificate{client.Certificate}

	// When
	connection, err := tls.Dial("tcp", address, clientConfig)

	// Then
	if err != nil {
		t.Fatalf("dial mutually pinned peer: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
	if err := wait(); err != nil {
		t.Fatalf("server handshake: %v", err)
	}
}

func TestNewServerConfig_rejectsUnpinnedClient(t *testing.T) {
	// Given
	server, err := LoadOrCreate(t.TempDir(), "https://localhost")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	client, err := LoadOrCreate(t.TempDir(), "https://localhost")
	if err != nil {
		t.Fatalf("create client identity: %v", err)
	}
	config, err := NewServerConfig(server, "localhost", Fingerprint{})
	if err != nil {
		t.Fatalf("new server config: %v", err)
	}
	address, wait := serveMutualTLS(t, config)
	clientConfig, err := NewClientConfig(
		localURL(t, address),
		[]Fingerprint{server.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new client config: %v", err)
	}
	clientConfig.Certificates = []tls.Certificate{client.Certificate}

	// When
	connection, dialErr := tls.Dial("tcp", address, clientConfig)

	// Then
	if dialErr == nil {
		if closeErr := connection.Close(); closeErr != nil {
			t.Fatalf("close unpinned connection: %v", closeErr)
		}
	}
	if err := wait(); err == nil {
		t.Fatal("server accepted unpinned client")
	}
}

func serveMutualTLS(t *testing.T, config *tls.Config) (string, func() error) {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("listen TLS: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			tlsConnection, ok := connection.(*tls.Conn)
			if !ok {
				acceptErr = net.ErrClosed
			} else {
				acceptErr = tlsConnection.Handshake()
			}
			if closeErr := connection.Close(); closeErr != nil &&
				acceptErr == nil {
				acceptErr = closeErr
			}
		}
		done <- acceptErr
	}()
	return listener.Addr().String(), func() error {
		if err := listener.Close(); err != nil {
			return err
		}
		return <-done
	}
}
