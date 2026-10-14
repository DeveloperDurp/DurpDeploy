//go:build e2e && backuprestore && linux

package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

type restoreProcess struct {
	command *exec.Cmd
	done    chan error
	stderr  bytes.Buffer
	stopped bool
}

func (f *restoreFixture) start(t *testing.T) *restoreProcess {
	t.Helper()
	// Testing cancels t.Context before cleanup. Let bounded stop own shutdown.
	command := exec.CommandContext(context.WithoutCancel(t.Context()), f.binary)
	command.Dir = f.root
	command.Env = f.environment(t)
	// Inherit the runner's process group so its signal trap also owns this child.
	p := &restoreProcess{command: command, done: make(chan error, 1)}
	command.Stderr = &p.stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.stop(t) })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"addr"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil &&
				event.Message == "server starting" {
				ready <- "http://" + event.Address
			}
		}
		waitErr := command.Wait()
		p.done <- errors.Join(scanner.Err(), waitErr)
	}()
	select {
	case address := <-ready:
		f.api.baseURL = address
		f.api.api(t, "GET", "/api/v1/healthz", nil, 200)
	case err := <-p.done:
		p.stopped = true
		t.Fatalf("recovery server exited before readiness: %v\n%s",
			err, p.stderr.String())
	case <-time.After(30 * time.Second):
		p.stop(t)
		t.Fatal("recovery server readiness timed out")
	}
	return p
}

func (p *restoreProcess) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	if err := p.command.Process.Signal(syscall.SIGTERM); err != nil &&
		!errors.Is(err, os.ErrProcessDone) {
		t.Error(err)
	}
	select {
	case err := <-p.done:
		p.stopped = true
		if err != nil {
			t.Errorf("recovery server exit: %v\n%s", err, p.stderr.String())
		}
	case <-time.After(15 * time.Second):
		if err := p.command.Process.Kill(); err != nil &&
			!errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		select {
		case <-p.done:
			p.stopped = true
		case <-time.After(10 * time.Second):
			t.Error("recovery server did not exit after SIGKILL")
		}
		t.Error("recovery server exceeded its shutdown deadline")
	}
}
