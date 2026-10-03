package runner

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type broadcastWriter struct {
	broker       *LogBroker
	repo         *repository.Repository
	deploymentID int64
	stepName     string
	stepIndex    sql.NullInt64
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
	w.buf.Next(lastNL + 1)
	for _, line := range lines {
		if err := w.writeLine(line, ""); err != nil {
			return len(p), err
		}
	}

	return len(p), nil
}

func (w *broadcastWriter) Flush() {
	remaining := w.buf.String()
	if remaining != "" {
		remaining = w.scrubber.Scrub(remaining)
		if err := w.writeLine(remaining, ""); err != nil {
			slog.Error("persist deployment output", "error", err)
		}
		w.buf.Reset()
	}
}

func (w *broadcastWriter) state(state string) error {
	return w.writeLine(fmt.Sprintf("Step %s.", state), state)
}

func (w *broadcastWriter) finishState(result error, cancelled bool) {
	state := "succeeded"
	if cancelled {
		state = "cancelled"
	} else if result != nil {
		state = "failed"
	}
	if err := w.state(state); err != nil {
		slog.Error("persist deployment step state", "error", err)
	}
}

func (w *broadcastWriter) writeLine(line, state string) error {
	_, err := w.repo.Queries.CreateStepDeploymentLog(
		w.ctx,
		db.CreateStepDeploymentLogParams{
			DeploymentID: w.deploymentID,
			StepName:     sql.NullString{String: w.stepName, Valid: true},
			Line:         line,
			StepIndex:    w.stepIndex,
			StepState:    sql.NullString{String: state, Valid: state != ""},
		},
	)
	if err == nil {
		w.broker.Broadcast(w.deploymentID, line)
	}
	return err
}
