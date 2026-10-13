package httpstream

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/auth"
)

func streamLogs(w http.ResponseWriter, r *http.Request, options logStream) {
	r, cancel := watchLogAccess(r, options)
	defer cancel()
	stream, err := options.open(w, r)
	if err != nil {
		return
	}
	ch := options.broker.Subscribe(options.deploymentID)
	defer options.broker.Unsubscribe(options.deploymentID, ch)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.Now()
	if err := options.replay(r, stream); err != nil {
		return
	}
	for {
		state, err := options.poll(r, stream)
		if err != nil {
			return
		}
		switch state {
		case logPollComplete:
			return
		case logPollPending:
			continue
		case logPollWaiting:
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-ticker.C:
			if err := options.keepalive(stream, &heartbeat); err != nil {
				return
			}
		}
	}
}

func (options logStream) open(
	w http.ResponseWriter,
	r *http.Request,
) (*Writer, error) {
	if _, err := options.repo.Queries.GetDeployment(
		r.Context(), options.deploymentID,
	); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		auth.RenderRequestError(w, r, status, http.StatusText(status))
		return nil, err
	}
	if options.format == "ndjson" {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	stream, err := New(w)
	if err != nil {
		auth.RenderRequestError(
			w, r, http.StatusInternalServerError, "Streaming unsupported",
		)
		return nil, err
	}
	if !auth.CanStreamDeployment(r, options.repo, options.deploymentID) {
		return nil, errStreamAccessRevoked
	}
	if options.format == "structured" {
		if _, err := fmt.Fprint(stream, ": connected\n\n"); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

func (options logStream) keepalive(
	stream *Writer,
	heartbeat *time.Time,
) error {
	if options.format == "ndjson" || time.Since(*heartbeat) < 15*time.Second {
		return nil
	}
	if _, err := fmt.Fprint(stream, ": keepalive\n\n"); err != nil {
		return err
	}
	*heartbeat = time.Now()
	return nil
}
