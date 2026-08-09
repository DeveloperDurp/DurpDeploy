//go:build mobilebrowser

package handler_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMobileBrowserInterruptCleansOwnedResources(t *testing.T) {
	// Given
	root := repositoryRoot(t)
	requireMobileBrowserPrerequisites(t, root)
	fixtures := newMobileBrowserFixtures(t)
	readyFile := filepath.Join(t.TempDir(), "chromium-profile")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"node",
		"scripts/mobile_readability_qa.mjs",
	)
	command.Dir = root
	command.Env = append(
		os.Environ(),
		mobileBrowserEnvironment(
			fixtures,
			"admin",
			fixtures.sessions["admin"],
		)...,
	)
	command.Env = append(
		command.Env,
		"MOBILE_READY_FILE="+readyFile,
		"MOBILE_HOLD_FOR_SIGNAL=1",
	)
	var output bytes.Buffer
	command.Stderr = &output

	if err := command.Start(); err != nil {
		t.Fatalf("start interrupt harness: %v", err)
	}
	profile := awaitReadyProfile(t, readyFile)

	// When
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt harness: %v", err)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("repeat interrupt harness: %v", err)
	}
	err := command.Wait()

	// Then
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 130 {
		t.Fatalf("interrupt exit = %v, output = %s", err, output.String())
	}
	assertMobileBrowserResourcesGone(t, profile)
}

func awaitReadyProfile(t *testing.T, readyFile string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(readyFile)
		if err == nil {
			return strings.TrimSpace(string(contents))
		}
		if !os.IsNotExist(err) {
			t.Fatalf("read ready profile: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for browser ready receipt")
	return ""
}
