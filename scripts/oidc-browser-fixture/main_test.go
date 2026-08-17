//go:build oidctest

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type readiness struct {
	AppURL string `json:"app_url"`
	IDPURL string `json:"idp_url"`
	PID    int    `json:"pid"`
}

func TestBrowserFixtureSelfTest(t *testing.T) {
	// Given
	binary := buildHarness(t)

	// When
	output, err := exec.Command(binary, "--self-test").CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("--self-test error = %v\n%s", err, output)
	}
	if strings.Contains(string(output), "fixture-secret") ||
		strings.Contains(string(output), "fixture-access") {
		t.Fatal("--self-test output exposed fixture credentials")
	}
}

func TestBrowserFixtureWritesSanitizedReadinessAndStopsOnSIGTERM(t *testing.T) {
	// Given
	binary := buildHarness(t)
	readyFile := filepath.Join(t.TempDir(), "ready.json")
	cmd, done := startHarness(t, binary, readyFile)
	state := waitForReadiness(t, readyFile, done)
	response, err := (&http.Client{Timeout: time.Second}).Get(
		state.AppURL + "/healthz",
	)
	if err != nil {
		t.Fatalf("GET app healthz: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"app healthz status = %d, want %d",
			response.StatusCode,
			http.StatusOK,
		)
	}

	// When
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	err = <-done

	// Then
	if err != nil {
		t.Fatalf("harness exit after SIGTERM = %v", err)
	}
	if _, err := os.Stat(readyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readiness file remains after shutdown: %v", err)
	}
}

func buildHarness(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "oidc-browser-fixture")
	command := exec.Command(
		"go",
		"build",
		"-tags",
		"oidctest",
		"-o",
		binary,
		".",
	)
	command.Dir = "."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build browser harness: %v\n%s", err, output)
	}
	return binary
}

func startHarness(
	t *testing.T,
	binary string,
	readyFile string,
	args ...string,
) (*exec.Cmd, <-chan error) {
	t.Helper()
	command := exec.Command(
		binary,
		append([]string{"--ready-file", readyFile}, args...)...,
	)
	if err := command.Start(); err != nil {
		t.Fatalf("start browser harness: %v", err)
	}
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- command.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		select {
		case <-finished:
			return
		default:
		}
		if err := command.Process.Signal(syscall.SIGTERM); err != nil &&
			!errors.Is(err, os.ErrProcessDone) {
			t.Errorf("send cleanup SIGTERM to browser harness: %v", err)
		}
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		select {
		case <-finished:
			return
		case <-timeout.C:
		}
		if err := command.Process.Kill(); err != nil &&
			!errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill unresponsive browser harness: %v", err)
		}
		<-finished
	})
	return command, done
}

func waitForReadiness(
	t *testing.T,
	path string,
	done <-chan error,
) readiness {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		body, err := os.ReadFile(path)
		if err == nil {
			return parseReadiness(t, body)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read readiness: %v", err)
		}
		select {
		case err := <-done:
			t.Fatalf("browser harness exited before readiness: %v", err)
		case <-timeout.C:
			t.Fatal("timed out waiting for browser readiness")
		case <-poll.C:
		}
	}
}

func parseReadiness(t *testing.T, body []byte) readiness {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("parse readiness JSON: %v", err)
	}
	if len(fields) != 3 || fields["app_url"] == nil ||
		fields["idp_url"] == nil ||
		fields["pid"] == nil {
		t.Fatalf(
			"readiness fields = %#v, want only app_url, idp_url, pid",
			fields,
		)
	}
	var state readiness
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decode readiness JSON: %v", err)
	}
	if state.PID < 1 {
		t.Fatalf("readiness PID = %d, want process ID", state.PID)
	}
	assertLoopbackURL(t, state.AppURL, "http")
	assertLoopbackURL(t, state.IDPURL, "https")
	for _, value := range []string{
		"secret", "token", "fixture-access", "fixture-code", "fixture-secret",
	} {
		if strings.Contains(strings.ToLower(string(body)), value) {
			t.Fatalf("readiness JSON exposed sensitive value %q", value)
		}
	}
	return state
}

func assertLoopbackURL(t *testing.T, rawURL, scheme string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %s URL: %v", scheme, err)
	}
	if parsed.Scheme != scheme || parsed.Hostname() != "127.0.0.1" {
		t.Fatalf("URL = %q, want %s loopback URL", rawURL, scheme)
	}
}
