package main

import (
	"net/http"
	"time"

	"durpdeploy/internal/config"
)

// Both listeners enforce absolute read deadlines, even without a proxy.
// Streaming handlers replace WriteTimeout with per-event deadlines.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return newConfiguredHTTPServer(addr, handler, config.Defaults().Limits.HTTP)
}

func newConfiguredHTTPServer(
	addr string, handler http.Handler, limits config.HTTPLimits,
) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: handler,
		ReadHeaderTimeout: time.Duration(
			limits.ReadHeaderTimeoutSeconds,
		) * time.Second,
		ReadTimeout: time.Duration(
			limits.ReadTimeoutSeconds,
		) * time.Second,
		WriteTimeout: time.Duration(
			limits.WriteTimeoutSeconds,
		) * time.Second,
		IdleTimeout: time.Duration(
			limits.IdleTimeoutSeconds,
		) * time.Second,
	}
}
