package httpstream

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func streamLogs(w http.ResponseWriter, r *http.Request, options logStream) {
	r, cancel := watchLogAccess(r, options)
	defer cancel()
	repo, broker, deploymentID, after := options.repo, options.broker, options.deploymentID, options.after
	if _, err := repo.Queries.GetDeployment(
		r.Context(),
		deploymentID,
	); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		auth.RenderRequestError(w, r, status, http.StatusText(status))
		return
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
			w,
			r,
			http.StatusInternalServerError,
			"Streaming unsupported",
		)
		return
	}
	if !auth.CanStreamDeployment(r, repo, deploymentID) {
		return
	}
	if options.format == "structured" {
		if _, err := fmt.Fprint(stream, ": connected\n\n"); err != nil {
			return
		}
	}
	ch := broker.Subscribe(deploymentID)
	defer broker.Unsubscribe(deploymentID, ch)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.Now()
	authorizedAt := time.Time{}
	authorize := func() bool {
		if r.Context().Err() != nil {
			return false
		}
		if time.Since(authorizedAt) < time.Second {
			return true
		}
		if !auth.CanStreamDeployment(r, repo, deploymentID) {
			return false
		}
		authorizedAt = time.Now()
		return true
	}
	if options.format != "structured" && after == 0 {
		err := repo.ForEachDeploymentLogByDeploymentAsc(
			r.Context(),
			deploymentID,
			func(log db.DeploymentLog) error {
				if !authorize() {
					return errStreamAccessRevoked
				}
				if err := writeStreamLog(
					stream,
					log,
					options.format,
					false,
				); err != nil {
					return err
				}
				if log.ID > after {
					after = log.ID
				}
				return nil
			},
		)
		if err != nil {
			return
		}
		if options.format == "sse" && after > 0 {
			if !authorize() {
				return
			}
			if _, err := fmt.Fprintf(stream, "id: %d\n\n", after); err != nil {
				return
			}
		}
	}
	for {
		if !auth.CanStreamDeployment(r, repo, deploymentID) {
			return
		}
		authorizedAt = time.Now()
		logs, err := options.readLogs(r.Context(), after)
		if err != nil {
			return
		}
		for _, log := range logs {
			if !authorize() {
				return
			}
			if err := options.writeLog(stream, log); err != nil {
				return
			}
			after = log.ID
		}
		if len(logs) == 256 ||
			(options.preview && len(logs) == repository.LogPreviewBatchLimit) {
			continue
		}
		deployment, err := repo.Queries.GetDeployment(r.Context(), deploymentID)
		if err != nil {
			return
		}
		switch deployment.Status {
		case "succeeded",
			"failed",
			"cancelled",
			"rejected",
			"expired",
			"cleanup_unconfirmed":
			if broker.DeploymentActive(deploymentID) {
				break
			}
			// Re-read after observing terminal status: a final log may have
			// committed between the previous read and this status read.
			remaining, err := options.readLogs(r.Context(), after)
			if err != nil {
				return
			}
			if len(remaining) > 0 {
				continue
			}
			data, err := json.Marshal(
				map[string]string{"status": deployment.Status},
			)
			if err != nil {
				return
			}
			if options.format != "ndjson" {
				if _, err := fmt.Fprintf(
					stream,
					"event: complete\ndata: %s\n\n",
					data,
				); err != nil {
					return
				}
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-ticker.C:
			if options.format != "ndjson" &&
				time.Since(heartbeat) >= 15*time.Second {
				if _, err := fmt.Fprint(stream, ": keepalive\n\n"); err != nil {
					return
				}
				heartbeat = time.Now()
			}
		}
	}
}
