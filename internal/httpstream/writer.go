// Package httpstream bounds each streaming write without limiting stream life.
package httpstream

import (
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/config"
)

type Writer struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
}

// New fails closed if middleware prevents control of the socket deadline.
func New(w http.ResponseWriter) (*Writer, error) {
	return NewWithTimeout(
		w,
		time.Duration(
			config.Defaults().Limits.HTTP.StreamWriteTimeoutSeconds,
		)*time.Second,
	)
}

// NewWithTimeout applies one positive deadline to each event write.
func NewWithTimeout(
	w http.ResponseWriter,
	timeout time.Duration,
) (*Writer, error) {
	if timeout <= 0 {
		return nil, errors.New("stream write timeout must be positive")
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &Writer{
		w: w, controller: controller, timeout: timeout,
	}, nil
}

// Write flushes one event under a fresh deadline. Clear it while waiting for
// the next event, so quiet streams do not expire. Callers must stop on error.
func (w *Writer) Write(p []byte) (int, error) {
	if err := w.controller.SetWriteDeadline(
		time.Now().Add(w.timeout),
	); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	if err != nil {
		return n, err
	}
	if err := Flush(w.w); err != nil {
		return n, err
	}
	return n, w.controller.SetWriteDeadline(time.Time{})
}

// Flush retains error-aware buffering wrappers, but unwraps chi's legacy
// http.Flusher wrappers, which would otherwise discard socket flush errors.
func Flush(w http.ResponseWriter) error {
	if f, ok := w.(interface{ FlushError() error }); ok {
		return f.FlushError()
	}
	if u, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
		return Flush(u.Unwrap())
	}
	return http.NewResponseController(w).Flush()
}
