package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutor_Succeeds_when_script_exits_zero(t *testing.T) {
	// Given
	var logs []string
	executor := NewExecutor()
	job := NewJob(JobConfig{Name: "ok", ScriptBody: "echo complete"})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(line string) error {
				logs = append(logs, line)
				return nil
			},
		}),
	)

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(logs, "\n"); got != "complete" {
		t.Fatalf("logs = %q, want %q", got, "complete")
	}
}

func TestExecutor_ReturnsFailure_when_script_exits_nonzero(t *testing.T) {
	// Given
	executor := NewExecutor()
	job := NewJob(JobConfig{Name: "fail", ScriptBody: "exit 7"})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{}),
	)

	// Then
	if err == nil {
		t.Fatal("execute succeeded, want failure")
	}
}

func TestExecutor_Retries_when_prior_attempt_fails(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "attempted")
	var logs []string
	executor := NewExecutor()
	job := NewJob(JobConfig{
		Name:       "retry",
		ScriptBody: "test -f " + marker + " || { touch " + marker + "; exit 1; }; echo complete",
		MaxRetries: 1,
	})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(line string) error {
				logs = append(logs, line)
				return nil
			},
		}),
	)

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(
		logs,
		"\n",
	); !strings.Contains(
		got,
		"retrying (attempt 2 of 2)",
	) {
		t.Fatalf("logs = %q, want retry message", got)
	}
}

func TestExecutor_ReturnsTimeout_when_step_exceeds_deadline(t *testing.T) {
	// Given
	executor := NewExecutor()
	job := NewJob(JobConfig{
		Name:       "timeout",
		ScriptBody: "sleep 1",
		Timeout:    20 * time.Millisecond,
	})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{}),
	)

	// Then
	if err == nil {
		t.Fatal("execute succeeded, want timeout")
	}
}

func TestExecutor_StopsRetries_when_cancelled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executor := NewExecutor()
	job := NewJob(
		JobConfig{Name: "cancel", ScriptBody: "exit 1", MaxRetries: 1},
	)

	// When
	err := executor.Execute(ctx, job, NewCallbacks(CallbacksConfig{
		Cancelled: func() bool { return true },
	}))

	// Then
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("execute error = %v, want ErrCancelled", err)
	}
}

func TestExecutor_RedactsSecrets_when_writing_logs(t *testing.T) {
	// Given
	secret := "executor-secret"
	var logs []string
	executor := NewExecutor()
	job := NewJob(JobConfig{
		Name:       "redaction",
		ScriptBody: "printf '%s\\n' " + secret,
		Secrets:    []string{secret},
	})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(line string) error {
				logs = append(logs, line)
				return nil
			},
		}),
	)

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(
		logs,
		"\n",
	); strings.Contains(got, secret) ||
		!strings.Contains(got, "[REDACTED]") {
		t.Fatalf("logs = %q, want redacted secret", got)
	}
}

func TestExecutor_Fails_when_log_persistence_fails(t *testing.T) {
	// Given
	persistenceErr := errors.New("persist deployment log")
	executor := NewExecutor()
	job := NewJob(JobConfig{Name: "persist", ScriptBody: "echo complete"})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(string) error { return persistenceErr },
		}),
	)

	// Then
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("execute error = %v, want persistence error", err)
	}
}
