package httpstream

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

// StepLog is the opt-in structured SSE payload; text streams keep their format.
type StepLog struct {
	ID        int64  `json:"id"`
	Line      string `json:"line"`
	Step      string `json:"step"`
	StepIndex *int64 `json:"step_index"`
	State     string `json:"state,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

func StepLogFromRow(log db.DeploymentLog) StepLog {
	event := StepLog{
		ID: log.ID, Line: log.Line, Step: log.StepName.String,
		State: log.StepState.String, CreatedAt: log.CreatedAt,
	}
	if log.StepIndex.Valid {
		event.StepIndex = &log.StepIndex.Int64
	}
	return event
}

// StreamStepLogs uses broker notifications to wake up and persisted IDs to
// replay. Periodic reads recover dropped notifications without losing output.
func StreamStepLogs(
	w http.ResponseWriter, r *http.Request, repo *repository.Repository,
	broker *runner.LogBroker, deploymentID int64,
) {
	after, err := LogCursor(r)
	if err != nil {
		auth.RenderRequestError(
			w,
			r,
			http.StatusBadRequest,
			"Invalid log cursor",
		)
		return
	}
	streamStepLogs(w, r, repo, broker, deploymentID, after)
}

func LogCursor(r *http.Request) (int64, error) {
	cursor := r.URL.Query().Get("after")
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		cursor = last
	}
	var after int64
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			return 0, errors.New("Invalid log cursor")
		}
	}
	return after, nil
}

func streamStepLogs(
	w http.ResponseWriter, r *http.Request, repo *repository.Repository,
	broker *runner.LogBroker, deploymentID, after int64,
) {
	if _, err := repo.Queries.GetDeployment(r.Context(), deploymentID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		auth.RenderRequestError(w, r, status, http.StatusText(status))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
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
	if _, err := fmt.Fprint(stream, ": connected\n\n"); err != nil {
		return
	}
	ch := broker.Subscribe(deploymentID)
	defer broker.Unsubscribe(deploymentID, ch)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.Now()
	for {
		logs, err := repo.Queries.ListDeploymentLogsAfter(
			r.Context(),
			db.ListDeploymentLogsAfterParams{
				DeploymentID: deploymentID,
				ID:           after,
			},
		)
		if err != nil {
			return
		}
		for _, log := range logs {
			data, err := json.Marshal(StepLogFromRow(log))
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(stream, "id: %d\nevent: log\ndata: %s\n\n", log.ID, data); err != nil {
				return
			}
			after = log.ID
		}
		if len(logs) == 256 {
			continue
		}
		deployment, err := repo.Queries.GetDeployment(r.Context(), deploymentID)
		if err != nil {
			return
		}
		switch deployment.Status {
		case "succeeded", "failed", "cancelled", "cleanup_unconfirmed":
			if broker.DeploymentActive(deploymentID) {
				break
			}
			// Re-read after observing terminal status: a final log may have
			// committed between the previous read and this status read.
			remaining, err := repo.Queries.ListDeploymentLogsAfter(
				r.Context(),
				db.ListDeploymentLogsAfterParams{
					DeploymentID: deploymentID,
					ID:           after,
				},
			)
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
			_, _ = fmt.Fprintf(stream, "event: complete\ndata: %s\n\n", data)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-ticker.C:
			if time.Since(heartbeat) >= 15*time.Second {
				if _, err := fmt.Fprint(stream, ": keepalive\n\n"); err != nil {
					return
				}
				heartbeat = time.Now()
			}
		}
	}
}
