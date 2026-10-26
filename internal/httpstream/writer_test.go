package httpstream

import (
	"durpdeploy/internal/testutil"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
)

// A pipe provides a real blocking write with no OS socket-buffer dependence.
type pipeWriter struct {
	net.Conn
	header http.Header
}

func (w *pipeWriter) Header() http.Header { return w.header }
func (w *pipeWriter) WriteHeader(int)     {}
func (w *pipeWriter) FlushError() error   { return nil }

func TestWriterTerminatesBlockedWrite(t *testing.T) {
	// Given: a client that does not read, behind the same chi wrapper used
	// by request logging and auditing.
	server, client := net.Pipe()
	defer testutil.Close(t, server)
	defer testutil.Close(t, client)
	w := &pipeWriter{Conn: server, header: make(http.Header)}
	stream, err := NewWithTimeout(
		middleware.NewWrapResponseWriter(w, 1),
		20*time.Millisecond,
	)
	require.NoError(t, err)
	// When: an event is written to the stalled client.
	_, err = stream.Write([]byte("data: event\n\n"))
	// Then: the server-side write deadline ends the operation.
	var timeout net.Error
	require.ErrorAs(t, err, &timeout)
	require.True(t, timeout.Timeout())
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestConfiguredWriterDeadlines(t *testing.T) {
	for _, timeout := range []time.Duration{time.Second, 2 * time.Minute} {
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		stream, err := NewWithTimeout(w, timeout)
		require.NoError(t, err)
		before := time.Now()
		_, err = stream.Write([]byte("data: event\n\n"))
		require.NoError(t, err)
		require.Len(t, w.deadlines, 3)
		require.True(t, w.deadlines[0].IsZero())
		require.WithinDuration(
			t,
			before.Add(timeout),
			w.deadlines[1],
			time.Second,
		)
		require.True(t, w.deadlines[2].IsZero())
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		_, err := NewWithTimeout(httptest.NewRecorder(), timeout)
		require.Error(t, err)
	}
}

type failingFlushWriter struct{ *pipeWriter }

var errFlush = errors.New("flush failed")

func (w *failingFlushWriter) FlushError() error { return errFlush }
func (w *failingFlushWriter) Flush()            {}

func TestWriterReturnsFlushErrorThroughChi(t *testing.T) {
	// Given: an error-aware transport behind two legacy chi wrappers.
	server, client := net.Pipe()
	defer testutil.Close(t, server)
	defer testutil.Close(t, client)
	w := &failingFlushWriter{
		&pipeWriter{Conn: server, header: make(http.Header)},
	}
	wrapped := middleware.NewWrapResponseWriter(
		middleware.NewWrapResponseWriter(w, 1), 1,
	)
	stream, err := New(wrapped)
	require.NoError(t, err)
	go func() { _, _ = io.Copy(io.Discard, client) }()
	// When: the buffered event flush fails.
	_, err = stream.Write([]byte("event"))
	// Then: the error is not swallowed by chi's http.Flusher interface.
	require.ErrorIs(t, err, errFlush)
}
