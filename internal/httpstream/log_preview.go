package httpstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

var errInvalidLogPreview = errors.New("preview must be true or false")

func LogPreviewRequested(r *http.Request) (bool, error) {
	switch r.URL.Query().Get("preview") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errInvalidLogPreview
	}
}

func (options logStream) readLogs(
	ctx context.Context, after int64,
) ([]repository.LogPreview, error) {
	if options.preview {
		return options.repo.ReadLogPreviewsAfter(
			ctx,
			options.deploymentID,
			after,
		)
	}
	rows, err := options.repo.Queries.ListDeploymentLogsAfter(ctx,
		db.ListDeploymentLogsAfterParams{
			DeploymentID: options.deploymentID, ID: after,
			BatchLimit: options.repo.ResourceLimits().Logs.ExportBatch,
		})
	if err != nil {
		return nil, err
	}
	logs := make([]repository.LogPreview, len(rows))
	for i, row := range rows {
		logs[i] = repository.LogPreview{DeploymentLog: row}
	}
	return logs, nil
}

func (options logStream) writeLog(
	w io.Writer, log repository.LogPreview,
) error {
	if !options.preview {
		return writeStreamLog(w, log.DeploymentLog, options.format, true)
	}
	event := StepLogFromRow(log.DeploymentLog)
	event.Truncated = log.Truncated
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", log.ID, data)
	return err
}
