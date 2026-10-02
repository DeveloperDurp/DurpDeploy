package main

import (
	"net/http"
	"time"
)

// Both listeners enforce absolute read deadlines, even without a proxy.
// Streaming handlers replace WriteTimeout with per-event deadlines.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      time.Minute,
		IdleTimeout:       time.Minute,
	}
}
