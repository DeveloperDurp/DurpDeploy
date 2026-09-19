package runner

import (
	"bytes"
	"context"
	"database/sql"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type broadcastWriter struct {
	broker       *LogBroker
	repo         *repository.Repository
	deploymentID int64
	stepName     string
	ctx          context.Context
	buf          bytes.Buffer
	scrubber     *Scrubber
}

// Write buffers output and scrubs everything up to the last newline before
// broadcasting/persisting it. Scrubbing the whole buffer (instead of a single
// line at a time) lets the Scrubber catch secrets that span multiple Write
// calls or contain embedded newlines (e.g. a multi-line SSH key).
func (w *broadcastWriter) Write(p []byte) (n int, err error) {
	w.buf.Write(p)
	data := w.buf.Bytes()
	safeEnd := len(data) - w.scrubber.PendingBytes(string(data))
	lastNL := bytes.LastIndexByte(data[:safeEnd], '\n')
	if lastNL == -1 {
		return len(p), nil
	}

	toScrub := string(data[:lastNL+1])
	scrubbed := w.scrubber.Scrub(toScrub)

	lines := strings.Split(strings.TrimSuffix(scrubbed, "\n"), "\n")
	for _, line := range lines {
		w.broker.Broadcast(w.deploymentID, line)
		w.writeLine(line)
	}

	w.buf.Next(lastNL + 1)
	return len(p), nil
}

func (w *broadcastWriter) Flush() {
	remaining := w.buf.String()
	if remaining != "" {
		remaining = w.scrubber.Scrub(remaining)
		w.broker.Broadcast(w.deploymentID, remaining)
		w.writeLine(remaining)
		w.buf.Reset()
	}
}

func (w *broadcastWriter) writeLine(line string) {
	_, _ = w.repo.Queries.CreateDeploymentLog(
		w.ctx,
		db.CreateDeploymentLogParams{
			DeploymentID: w.deploymentID,
			StepName:     sql.NullString{String: w.stepName, Valid: true},
			Line:         line,
		},
	)
}
