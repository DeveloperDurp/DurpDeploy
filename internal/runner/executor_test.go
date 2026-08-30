//go:build linux

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newExecutorForTest(t *testing.T) *Executor {
	t.Helper()
	previousBinds := chrootBinds
	previousOptionalBinds := optionalChrootBinds
	chrootBinds = nil
	optionalChrootBinds = nil
	t.Cleanup(func() {
		chrootBinds = previousBinds
		optionalChrootBinds = previousOptionalBinds
	})
	return &Executor{sandbox: &Sandbox{
		uid:                 uint32(os.Getuid()),
		gid:                 uint32(os.Getgid()),
		enabled:             true,
		applyCredentialFn:   func(*exec.Cmd) {},
		clearCapabilitiesFn: func(*exec.Cmd, bool) error { return nil },
		createCgroupFn:      func(int64) (*cgroup, error) { return &cgroup{}, nil },
		configureCgroupFn:   func(*exec.Cmd, *cgroup) error { return nil },
		removeCgroupFn:      func(*cgroup) error { return nil },
	}}
}

func TestExecutor_Succeeds_when_script_exits_zero(t *testing.T) {
	// Given
	var logs []string
	executor := newExecutorForTest(t)
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
	executor := newExecutorForTest(t)
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
	executor := newExecutorForTest(t)
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
	executor := newExecutorForTest(t)
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
	executor := newExecutorForTest(t)
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
	executor := newExecutorForTest(t)
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
	executor := newExecutorForTest(t)
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

func TestExecutor_FailsBeforeStarting_when_capability_drop_fails(t *testing.T) {
	// Given
	capabilityErr := errors.New("setpriv missing")
	marker := filepath.Join(t.TempDir(), "script-ran")
	previousBinds := chrootBinds
	previousOptionalBinds := optionalChrootBinds
	chrootBinds = nil
	optionalChrootBinds = nil
	t.Cleanup(func() {
		chrootBinds = previousBinds
		optionalChrootBinds = previousOptionalBinds
	})
	executor := &Executor{sandbox: &Sandbox{
		enabled:             true,
		clearCapabilitiesFn: func(*exec.Cmd, bool) error { return capabilityErr },
	}}
	job := NewJob(JobConfig{
		Name:       "capability failure",
		ScriptBody: "touch " + marker,
	})

	// When
	err := executor.Execute(context.Background(), job, Callbacks{})

	// Then
	if !errors.Is(err, capabilityErr) {
		t.Fatalf("execute error = %v, want capability error", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("script marker error = %v, want not exist", statErr)
	}
}

func TestExecutor_FailsBeforeStarting_when_bind_mount_fails(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "script-ran")
	previousBinds := chrootBinds
	chrootBinds = []string{"/does-not-exist"}
	t.Cleanup(func() { chrootBinds = previousBinds })
	executor := &Executor{sandbox: &Sandbox{enabled: true}}
	job := NewJob(JobConfig{
		Name:       "bind failure",
		ScriptBody: "touch " + marker,
	})

	// When
	err := executor.Execute(context.Background(), job, Callbacks{})

	// Then
	if err == nil {
		t.Fatal("execute succeeded after bind mount failure")
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("script marker error = %v, want not exist", statErr)
	}
}

func TestExecutor_FailsBeforeStarting_when_cgroup_setup_fails(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "script-ran")
	cgroupErr := errors.New("cgroup root unavailable")
	executor := newExecutorForTest(t)
	executor.sandbox.createCgroupFn = func(int64) (*cgroup, error) {
		return nil, cgroupErr
	}
	job := NewJob(JobConfig{
		DeploymentID: 1,
		Name:         "cgroup failure",
		ScriptBody:   "touch " + marker,
	})

	// When
	err := executor.Execute(context.Background(), job, Callbacks{})

	// Then
	if !errors.Is(err, cgroupErr) {
		t.Fatalf("execute error = %v, want cgroup error", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("script marker error = %v, want not exist", statErr)
	}
}

func TestExecutor_FailsBeforeFork_when_cgroup_placement_fails(t *testing.T) {
	// Given
	placementErr := errors.New("cgroup placement failed")
	marker := filepath.Join(t.TempDir(), "forked-before-cgroup")
	executor := newExecutorForTest(t)
	executor.sandbox.configureCgroupFn = func(*exec.Cmd, *cgroup) error {
		return placementErr
	}
	job := NewJob(JobConfig{
		DeploymentID: 1,
		Name:         "cgroup placement failure",
		ScriptBody:   "touch " + marker + "; sleep 1",
	})

	// When
	err := executor.Execute(context.Background(), job, Callbacks{})

	// Then
	if !errors.Is(err, placementErr) {
		t.Fatalf("execute error = %v, want placement error", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("forked marker error = %v, want not exist", statErr)
	}
}

func TestExecutor_Fails_when_cgroup_cleanup_fails(t *testing.T) {
	// Given
	cleanupErr := errors.New("remove cgroup")
	executor := newExecutorForTest(t)
	executor.sandbox.removeCgroupFn = func(*cgroup) error { return cleanupErr }
	job := NewJob(JobConfig{Name: "cgroup cleanup failure", ScriptBody: "true"})

	// When
	err := executor.Execute(context.Background(), job, Callbacks{})

	// Then
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("execute error = %v, want cleanup error", err)
	}
}

func TestExecutor_ExecuteSteps_stops_after_first_failed_step(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "second-step-ran")
	executor := newExecutorForTest(t)

	// When
	err := executor.ExecuteSteps(context.Background(), ExecutionConfig{
		DeploymentID: 1,
		Steps: []Step{
			{Name: "fail", ScriptBody: "exit 1"},
			{
				Name:       "must not run",
				ScriptBody: "touch " + marker,
			},
		},
	})

	// Then
	if err == nil {
		t.Fatal("execute steps succeeded, want failure")
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("second step marker error = %v, want not exist", statErr)
	}
}
