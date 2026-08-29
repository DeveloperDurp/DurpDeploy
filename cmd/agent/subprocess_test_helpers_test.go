package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (fixture *agentSubprocessFixture) assertNoSecretFiles(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(fixture.stateDir)
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	for _, entry := range entries {
		contents, err := os.ReadFile(filepath.Join(fixture.stateDir, entry.Name()))
		if err != nil {
			t.Fatalf("read state file: %v", err)
		}
		if strings.Contains(string(contents), "subprocess-secret") ||
			strings.Contains(string(contents), "test-claim") {
			t.Fatalf("state file %q contains plaintext secret or claim", entry.Name())
		}
	}
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		contents, err := os.ReadFile(path)
		if err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(contents), "%d", &pid); err == nil &&
				pid > 0 {
				return pid
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("child PID was not written: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
