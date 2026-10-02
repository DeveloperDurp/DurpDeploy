package api_test

import (
	"net/http/httptest"
	"testing"
	"time"
)

// A broken stream must fail the test, not hang httptest.Server.Close forever.
func closeStreamServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	srv.CloseClientConnections()
	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("stream handler did not exit during server shutdown")
	}
}
