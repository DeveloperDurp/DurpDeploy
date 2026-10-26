package agentserver

import (
	"durpdeploy/internal/testutil"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStagingIOBoundsStalls(t *testing.T) {
	for _, direction := range []string{"read", "write"} {
		t.Run(direction, func(t *testing.T) {
			// Given a real socket with a short transfer idle deadline.
			done := make(chan error, 1)
			listener := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					stream, err := newStagingIO(w, r, 50*time.Millisecond)
					if err == nil {
						if direction == "read" {
							_, err = stream.Read(make([]byte, 1))
						} else {
							_, err = stream.Write(make([]byte, 8<<20))
						}
					}
					done <- err
				},
			))
			defer listener.Close()
			connection, err := net.DialTimeout("tcp",
				strings.TrimPrefix(listener.URL, "http://"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer testutil.Close(t, connection)
			if err := connection.(*net.TCPConn).SetReadBuffer(
				1024,
			); err != nil {
				t.Fatal(err)
			}
			length := 0
			if direction == "read" {
				length = 1
			}

			// When the peer sends no body bytes and reads no response bytes.
			if _, err := fmt.Fprintf(
				connection,
				"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\n\r\n",
				length,
			); err != nil {
				t.Fatal(err)
			}

			// Then stalled I/O ends with a socket deadline error.
			select {
			case err := <-done:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("stalled %s error=%v", direction, err)
				}
			case <-time.After(3 * time.Second):
				testutil.Close(t, connection)
				t.Fatal("stalled transfer did not stop")
			}
		})
	}
}

var _ io.ReadWriter = (*stagingIO)(nil)
