package httpstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type logPollState uint8

const (
	logPollWaiting logPollState = iota
	logPollPending
	logPollComplete
)

func (options *logStream) canRead(r *http.Request) bool {
	if r.Context().Err() != nil {
		return false
	}
	if time.Since(options.authorizedAt) < time.Second {
		return true
	}
	if !auth.CanStreamDeployment(r, options.repo, options.deploymentID) {
		return false
	}
	options.authorizedAt = time.Now()
	return true
}

func (options *logStream) replay(r *http.Request, stream *Writer) error {
	if options.format == "structured" || options.after != 0 {
		return nil
	}
	err := options.repo.ForEachDeploymentLogByDeploymentAsc(
		r.Context(), options.deploymentID,
		func(log db.DeploymentLog) error {
			if !options.canRead(r) {
				return errStreamAccessRevoked
			}
			if err := writeStreamLog(stream, log, options.format, false); err != nil {
				return err
			}
			if log.ID > options.after {
				options.after = log.ID
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if options.format != "sse" || options.after == 0 {
		return nil
	}
	if !options.canRead(r) {
		return errStreamAccessRevoked
	}
	_, err = fmt.Fprintf(stream, "id: %d\n\n", options.after)
	return err
}

func (options *logStream) poll(
	r *http.Request,
	stream *Writer,
) (logPollState, error) {
	if !auth.CanStreamDeployment(r, options.repo, options.deploymentID) {
		return logPollComplete, errStreamAccessRevoked
	}
	options.authorizedAt = time.Now()
	logs, err := options.readLogs(r.Context(), options.after)
	if err != nil {
		return logPollComplete, err
	}
	for _, log := range logs {
		if !options.canRead(r) {
			return logPollComplete, errStreamAccessRevoked
		}
		if err := options.writeLog(stream, log); err != nil {
			return logPollComplete, err
		}
		options.after = log.ID
	}
	if len(logs) == 256 ||
		(options.preview && len(logs) == repository.LogPreviewBatchLimit) {
		return logPollPending, nil
	}
	return options.completion(r, stream)
}

func (options logStream) completion(
	r *http.Request,
	stream *Writer,
) (logPollState, error) {
	deployment, err := options.repo.Queries.GetDeployment(
		r.Context(), options.deploymentID,
	)
	if err != nil {
		return logPollComplete, err
	}
	switch deployment.Status {
	case "succeeded",
		"failed",
		"cancelled",
		"rejected",
		"expired",
		"cleanup_unconfirmed":
	default:
		return logPollWaiting, nil
	}
	if options.broker.DeploymentActive(options.deploymentID) {
		return logPollWaiting, nil
	}
	// Re-read after terminal status: the final log may have committed since
	// the previous read. Drain it before sending the completion event.
	remaining, err := options.readLogs(r.Context(), options.after)
	if err != nil {
		return logPollComplete, err
	}
	if len(remaining) > 0 {
		return logPollPending, nil
	}
	data, err := json.Marshal(map[string]string{"status": deployment.Status})
	if err != nil {
		return logPollComplete, err
	}
	if options.format != "ndjson" {
		if _, err := fmt.Fprintf(stream, "event: complete\ndata: %s\n\n", data); err != nil {
			return logPollComplete, err
		}
	}
	return logPollComplete, nil
}
