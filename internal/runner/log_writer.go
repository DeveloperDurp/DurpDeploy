package runner

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const maxPendingLogBytes = 1024 * 1024

var errPendingLogLimit = errors.New("pending log output exceeded safety limit")

type broadcastWriter struct {
	broker        *LogBroker
	repo          *repository.Repository
	deploymentID  int64
	stepName      string
	stepIndex     sql.NullInt64
	ctx           context.Context
	buf           bytes.Buffer
	scrubber      *Scrubber
	cancelAttempt context.CancelFunc
	outputFailed  bool
}

// Write buffers output and scrubs everything up to the last newline before
// broadcasting/persisting it. Scrubbing the whole buffer (instead of a single
// line at a time) lets the Scrubber catch secrets that span multiple Write
// calls or contain embedded newlines (e.g. a multi-line SSH key).
func (w *broadcastWriter) Write(p []byte) (n int, err error) {
	if w.outputFailed {
		return 0, errPendingLogLimit
	}
	if len(p) > maxPendingLogBytes-w.buf.Len() {
		w.outputFailed = true
		w.buf.Reset()
		if w.cancelAttempt != nil {
			w.cancelAttempt()
		}
		if err := w.writeLine(errPendingLogLimit.Error(), ""); err != nil {
			return 0, errors.Join(errPendingLogLimit, err)
		}
		return 0, errPendingLogLimit
	}
	w.buf.Write(p)
	data := w.buf.Bytes()
	safeEnd := len(data) - w.scrubber.PendingBytes(string(data))
	lastNL := bytes.LastIndexByte(data[:safeEnd], '\n')
	for lastNL >= 0 {
		pending := w.scrubber.PendingBytes(string(data[:lastNL+1]))
		if pending == 0 {
			break
		}
		lastNL = bytes.LastIndexByte(data[:lastNL+1-pending], '\n')
	}
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
	if w.outputFailed {
		return
	}
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
	if errors.Is(result, errContainerCleanup) {
		state = "failed"
	} else if cancelled {
		state = "cancelled"
	} else if result != nil {
		state = "failed"
	}
	if err := w.state(state); err != nil {
		slog.Error("persist deployment step state", "error", err)
	}
}

func (w *broadcastWriter) failStaging(result error, cancelled bool) error {
	_, err := fmt.Fprintf(w,
		"step %q: staging failed before execution: %v\n", w.stepName, result)
	w.Flush()
	w.finishState(result, cancelled)
	return err
}

func (w *broadcastWriter) writeLine(line, state string) error {
	_, err := w.repo.CreateStepDeploymentLog(
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
