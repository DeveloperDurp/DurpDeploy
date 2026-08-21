package runner

import (
	"context"
	"time"
)

// Step is one immutable bash step from a release snapshot.
type Step struct {
	Name           string `json:"name"`
	ScriptBody     string `json:"script_body"`
	SortOrder      int64  `json:"sort_order"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	MaxRetries     int64  `json:"max_retries"`
}

// ExecutionConfig supplies a deployment's immutable step snapshot and its
// environment to the sandboxed executor.
type ExecutionConfig struct {
	DeploymentID     int64
	Steps            []Step
	Environment      map[string]string
	Secrets          []string
	CallbacksForStep func(Step) Callbacks
}

// ExecuteSteps runs steps in order, stopping at the first failed or cancelled
// step. Both the server and pull agents use this path so process, sandbox,
// redaction, retry, and cancellation behavior remains identical.
func (e *Executor) ExecuteSteps(
	ctx context.Context,
	config ExecutionConfig,
) error {
	for _, step := range config.Steps {
		callbacks := Callbacks{}
		if config.CallbacksForStep != nil {
			callbacks = config.CallbacksForStep(step)
		}
		if err := e.Execute(ctx, NewJob(JobConfig{
			DeploymentID: config.DeploymentID,
			Name:         step.Name,
			ScriptBody:   step.ScriptBody,
			Timeout:      time.Duration(step.TimeoutSeconds) * time.Second,
			MaxRetries:   int(step.MaxRetries),
			Environment:  config.Environment,
			Secrets:      config.Secrets,
		}), callbacks); err != nil {
			return err
		}
	}
	return nil
}
