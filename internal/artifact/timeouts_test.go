package artifact

import (
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredArtifactClientTimeouts(t *testing.T) {
	for _, seconds := range []int64{1, 600} {
		limits := config.Defaults().Limits.Artifact
		limits.FetchTimeoutSeconds = seconds
		limits.DialTimeoutSeconds = seconds
		limits.TLSTimeoutSeconds = seconds
		limits.HeaderTimeoutSeconds = seconds
		client := NewClientWithLimits(limits)
		transport := client.HTTP.Transport.(*http.Transport)
		t.Cleanup(transport.CloseIdleConnections)
		limits.FetchTimeoutSeconds = 123
		want := time.Duration(seconds) * time.Second
		require.Equal(t, want, client.HTTP.Timeout)
		require.Equal(t, want, transport.TLSHandshakeTimeout)
		require.Equal(t, want, transport.ResponseHeaderTimeout)
		require.Nil(t, transport.Proxy)
		connection, err := transport.DialContext(
			t.Context(),
			"tcp",
			"127.0.0.1:443",
		)
		require.Nil(t, connection)
		require.ErrorIs(t, err, ErrFetch)
	}
}

func TestConfiguredArtifactTLSDeadlineAndRecovery(t *testing.T) {
	server, trusted := artifactTLSServer(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(zipFixture(t, "app.txt", 0600))
		}),
	)
	limits := config.Defaults().Limits.Artifact
	limits.TLSTimeoutSeconds = 1
	client := NewClientWithLimits(limits)
	transport := client.HTTP.Transport.(*http.Transport)
	transport.TLSClientConfig = trusted.HTTP.Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(transport.CloseIdleConnections)
	client.TempDir = t.TempDir()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	// Configure the fixture before opening any connections.
	server.TLS.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		select {
		case <-release:
		case <-hello.Context().Done():
		}
		return nil, nil
	}
	t.Cleanup(unblock)
	source := Repository{
		URLTemplate: server.URL + "/{version}.zip",
		AuthType:    "noauth",
	}
	started := time.Now()
	_, err := client.Fetch(t.Context(), source, Pin{URL: server.URL + "/1.zip"})
	require.True(t, errors.Is(err, ErrFetch))
	require.Less(t, time.Since(started), 5*time.Second)
	entries, err := os.ReadDir(client.TempDir)
	require.NoError(t, err)
	require.Empty(t, entries)
	unblock()
	download, err := client.Fetch(
		t.Context(),
		source,
		Pin{URL: server.URL + "/1.zip"},
	)
	require.NoError(t, err)
	require.NoError(t, os.Remove(download.Path))
}
