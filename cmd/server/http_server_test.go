package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPServerTimeoutPolicy(t *testing.T) {
	// Given / When: the production listener is constructed.
	srv := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	// Then: all connection phases have defensive limits.
	require.Equal(t, 5*time.Second, srv.ReadHeaderTimeout)
	require.Equal(t, 30*time.Second, srv.ReadTimeout)
	require.Equal(t, time.Minute, srv.WriteTimeout)
	require.Equal(t, time.Minute, srv.IdleTimeout)
}

func TestHTTPServerTerminatesStalledInput(t *testing.T) {
	for _, scenario := range []struct {
		phase     string
		encrypted bool
	}{
		{"headers", false}, {"body", false}, {"idle", false},
		{"headers", true}, {"body", true}, {"idle", true},
	} {
		encrypted, phase := scenario.encrypted, scenario.phase
		t.Run(
			fmt.Sprintf("%s/tls=%t", phase, encrypted),
			func(t *testing.T) {
				// Given: the production policy on a real listener, scaled
				// down for the test, with a handler that consumes the body.
				srv := httptest.NewUnstartedServer(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						if _, err := io.Copy(
							io.Discard,
							r.Body,
						); err != nil {
							http.Error(
								w,
								"timeout",
								http.StatusRequestTimeout,
							)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					},
				))
				srv.Config = newHTTPServer("", srv.Config.Handler)
				srv.Config.ReadHeaderTimeout = 100 * time.Millisecond
				srv.Config.ReadTimeout = 200 * time.Millisecond
				srv.Config.IdleTimeout = 100 * time.Millisecond
				if encrypted {
					srv.StartTLS()
				} else {
					srv.Start()
				}
				defer srv.Close()
				var conn net.Conn
				var err error
				if encrypted {
					conn, err = tls.Dial(
						"tcp",
						srv.Listener.Addr().String(),
						&tls.Config{
							InsecureSkipVerify: true,
						},
					) // Test certificate.
				} else {
					conn, err = net.Dial(
						"tcp",
						srv.Listener.Addr().String(),
					)
				}
				require.NoError(t, err)
				defer conn.Close()
				require.NoError(
					t,
					conn.SetDeadline(time.Now().Add(2*time.Second)),
				)
				reader := bufio.NewReader(conn)
				// When: the client stalls in one connection phase.
				switch phase {
				case "headers":
					_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost:")
				case "body":
					_, err = io.WriteString(
						conn,
						"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 10\r\n\r\nx",
					)
				case "idle":
					_, err = io.WriteString(
						conn,
						"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n",
					)
					require.NoError(t, err)
					response, readErr := http.ReadResponse(reader, nil)
					require.NoError(t, readErr)
					require.Equal(
						t,
						http.StatusNoContent,
						response.StatusCode,
					)
					require.NoError(t, response.Body.Close())
				}
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, reader)
				// Then: the server closes, not the test client's deadline.
				require.NoError(t, err)
			},
		)
	}
}
