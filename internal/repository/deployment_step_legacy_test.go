package repository

import (
	"errors"
	"testing"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDeploymentStepsLegacyMetadata(t *testing.T) {
	repo := New(nil)
	steps, err := repo.deploymentStepsFromRelease(`[
		{"name":"first","sort_order":"ignored","execution_target":"agent"},
		{"name":"second","sort_order":999999999999999999999999,
		 "container_image":"example.com/bash:5","variable_names":null}
	]`)
	if err != nil || len(steps) != 2 {
		t.Fatalf("legacy steps=%+v: %v", steps, err)
	}
	if steps[0].Name != "first" || steps[1].Name != "second" ||
		steps[0].AgentExecutionMode != "host" ||
		steps[1].ExecutionTarget != "local" ||
		steps[0].VariableNames != "[]" || steps[1].VariableNames != "[]" {
		t.Fatalf("legacy defaults or array order changed: %+v", steps)
	}
	_, err = repo.deploymentStepsFromRelease(`[{"name":"legacy-local"}]`)
	if !errors.Is(err, ErrLegacyServerStep) {
		t.Fatalf("legacy local step accepted: %v", err)
	}
}

func TestRefreshableReleaseLimits(t *testing.T) {
	settings := config.Defaults()
	settings.Limits.Execution = config.ExecutionLimits{
		MaxRetries:            0,
		MaxTimeoutSeconds:     3,
		DefaultTimeoutSeconds: 3,
	}
	repo := NewConfigured(nil, settings)
	for _, tc := range []struct {
		snapshot string
		want     error
	}{
		{`[{"name":"old","container_image":"example.com/bash:5","timeout_seconds":4,"max_retries":1}]`, nil},
		{`[{"name":"old","container_image":"example.com/bash:5","timeout_seconds":-1}]`, agentexecution.ErrLimits},
		{`[{"name":"old","container_image":"example.com/bash:5","max_retries":-1}]`, agentexecution.ErrLimits},
		{`[{"name":"old","timeout_seconds":4,"max_retries":1}]`, ErrLegacyServerStep},
	} {
		_, err := repo.decodeDeploymentSteps(tc.snapshot, true)
		require.ErrorIs(t, err, tc.want)
		_, err = repo.deploymentStepsFromRelease(tc.snapshot)
		require.ErrorIs(t, err, agentexecution.ErrLimits)
	}
	for _, raw := range []string{"[", `[{"execution_target":"unknown"}]`, `[{"execution_target":"agent","agent_execution_mode":"unknown"}]`} {
		_, err := repo.decodeDeploymentSteps(raw, true)
		require.Error(t, err)
	}
}
