package handler

import (
	"encoding/json"

	"durpdeploy/internal/db"
)

type releaseStepSnapshot struct {
	Name           string `json:"name"`
	ScriptBody     string `json:"script_body"`
	SortOrder      int64  `json:"sort_order"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	MaxRetries     int64  `json:"max_retries"`
}

// BuildReleaseStepsJSON serializes execution-ordered steps for immutable
// release snapshots. Callers keep using ListStepsByProject for execution order;
// the payload order is normalized so legacy stored gaps do not reach agents.
func BuildReleaseStepsJSON(steps []db.Step) (string, error) {
	snapshots := make([]releaseStepSnapshot, len(steps))
	for i, step := range steps {
		snapshots[i] = releaseStepSnapshot{
			Name:           step.Name,
			ScriptBody:     step.ScriptBody,
			SortOrder:      int64(i + 1),
			TimeoutSeconds: step.TimeoutSeconds,
			MaxRetries:     step.MaxRetries,
		}
	}

	stepsJSON, err := json.Marshal(snapshots)
	if err != nil {
		return "", err
	}
	return string(stepsJSON), nil
}
