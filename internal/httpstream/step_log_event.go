package httpstream

import (
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// StepLog is the opt-in structured SSE payload; text streams keep their format.
type StepLog struct {
	ID               int64  `json:"id"`
	Line             string `json:"line"`
	Step             string `json:"step"`
	StepIndex        *int64 `json:"step_index"`
	State            string `json:"state,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	DisplayTimestamp string `json:"display_timestamp"`
	Truncated        bool   `json:"truncated,omitempty"`
}

func StepLogFromRow(log db.DeploymentLog) StepLog {
	event := StepLog{
		ID: log.ID, Line: log.Line, Step: log.StepName.String,
		State: log.StepState.String, CreatedAt: log.CreatedAt,
		DisplayTimestamp: time.Unix(log.CreatedAt, 0).
			Format("2006-01-02 15:04:05"),
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
	preview, err := LogPreviewRequested(r)
	if err != nil {
		auth.RenderRequestError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	streamLogs(
		w,
		r,
		logStream{
			repo:         repo,
			broker:       broker,
			deploymentID: deploymentID,
			after:        after,
			format:       "structured",
			preview:      preview,
		},
	)
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
			//nolint:staticcheck // Preserve the public validation message.
			return 0, errors.New("Invalid log cursor")
		}
	}
	return after, nil
}
